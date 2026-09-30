package model

import (
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/internal/playback"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
	"github.com/bjarneo/cliamp/ui"
)

func (m *Model) scheduleReconnect(now time.Time) {
	if !m.reconnect.at.IsZero() || m.reconnect.attempts >= 5 {
		return
	}
	delay := time.Second << m.reconnect.attempts
	m.reconnect.at = now.Add(delay)
	m.reconnect.attempts++
	m.reconnect.notice = fmt.Errorf("reconnecting in %s", delay)
	m.err = m.reconnect.notice
}

// Update handles messages: key presses, ticks, and window resizes. After each
// message it drops a gapless preload that no longer matches the next track,
// and it tells the media controls and plugins when the playback state
// changed.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(spinnerTickMsg); ok {
		m.spinnerTicking = m.spinnerVisible()
		if !m.spinnerTicking {
			return m, nil
		}
		return m, spinnerTickCmd()
	}
	spinning := m.spinnerVisible()
	next, cmd := m.update(msg)
	if nm, ok := next.(Model); ok {
		nm.dropStalePreload()
		nm.notifyPlaybackChange()
		// A load that starts now gets its own redraws at once. The main tick
		// can still wait up to ui.TickIdle before it runs at the spinner rate.
		if !spinning && !nm.spinnerTicking && nm.spinnerVisible() {
			nm.spinnerTicking = true
			cmd = tea.Batch(cmd, spinnerTickCmd())
		}
		next = nm
	}
	return next, cmd
}

// update is Update without the stale preload check.
func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	wasScreen := m.activeScreen()
	wasVisualizerVisible := m.visualizerVisible()
	wasMode := ui.VisNone
	if m.vis != nil {
		wasMode = m.vis.Mode
	}
	wasPlaying := false
	wasPaused := false
	if m.player != nil {
		wasPlaying = m.player.IsPlaying()
		wasPaused = m.player.IsPaused()
	}
	defer func() {
		m.maybeRequestVisualizerRefresh(msg, wasScreen, wasVisualizerVisible, wasMode, wasPlaying, wasPaused)
		m.emitPluginEvents()
		m.publishIPCRuntimeState()
		m.publishPluginState()
	}()

	switch msg := msg.(type) {
	case tea.PasteMsg:
		cmd := m.handlePaste(msg.Content)
		return m, cmd

	case tea.KeyPressMsg:
		cmd := m.handleKey(msg)
		if m.quitting {
			return m, tea.Quit
		}
		m.applyHeightMode()
		m.adjustScroll()
		return m, cmd

	case autoPlayMsg:
		if m.playlist.Len() > 0 && !m.player.IsPlaying() {
			cmd := m.playCurrentTrack()
			return m, cmd
		}
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.recomputeLayout()
		m.normalizeMainFocus()
		m.clampActiveScrollState()
		return m, nil

	case seekTickMsg:
		// Async seek completed. A completion from a previous track says nothing
		// about the current one, so it must not clear its state or report on it.
		if msg.gen != m.seek.gen {
			return m, nil
		}
		m.seek.inFlight = false
		if m.seek.pending {
			// Commit the newer target even when this seek failed: the failure
			// belongs to a position the user has already moved on from.
			if msg.resume {
				// The chained seek carries no resume marker, so spend it here
				// or a later restart seeks back to the resume position.
				m.resume.path = ""
				m.resume.secs = 0
			}
			// A newer target arrived while this seek was running; land on it
			// rather than reporting this now-stale position as final.
			cmd := m.commitPendingSeek()
			return m, cmd
		}
		m.seek.pending = false
		// Only clear seekActive if no new seek keypresses arrived during loading.
		if m.seek.timer <= 0 {
			m.seek.active = false
		}
		// Grace period: suppress reconnect for a few ticks after seek completes.
		m.seek.grace = 10
		m.seek.graceFor = 0
		if msg.resume {
			// A failed resume must not be retried every time the track is opened
			// during this session. The original pipeline remains playable.
			m.resume.path = ""
			m.resume.secs = 0
		}
		if msg.err != nil {
			if msg.resume {
				m.status.Warningf(statusTTLLong, "Couldn't resume this track; playing from the previous position: %s", msg.err)
			} else {
				m.status.Warningf(statusTTLMedium, "Seek failed; playback continues from the previous position: %s", msg.err)
			}
			cmd := m.preloadNext()
			return m, cmd
		}
		if msg.resume {
			m.status.Showf(statusTTLDefault, "Resumed at %s", formatJumpClock(msg.target))
		}
		m.finishSeek()
		cmd := m.preloadNext()
		return m, cmd

	case ytdlUnpauseReconnectMsg:
		m.seek.active = false
		m.seek.timer = 0
		m.seek.timerFor = 0
		m.seek.grace = 10
		m.seek.graceFor = 0
		if msg.err != nil {
			m.err = msg.err
		} else {
			m.err = nil
			m.pausedAt = time.Time{}
		}
		return m, nil

	case tickMsg:
		now := time.Time(msg)
		dt := m.tickDelta(now)

		// Cache expensive player state once per tick so View() render
		// functions don't re-acquire speaker.Lock() multiple times.
		// PositionAndDuration() batches both reads under one speaker lock.
		if !m.buffering {
			if m.seek.active {
				m.cachedPos = m.seek.targetPos
				m.cachedDur = m.player.Duration()
			} else {
				m.cachedPos, m.cachedDur = m.player.PositionAndDuration()
				// Piped SSH streams report 0 duration — use metadata fallback.
				if m.cachedDur == 0 {
					if track, _ := m.currentPlaybackTrack(); track.DurationSecs > 0 && strings.HasPrefix(track.Path, "ssh://") {
						m.cachedDur = time.Duration(track.DurationSecs) * time.Second
					}
				}
			}
		} else {
			track, _ := m.currentPlaybackTrack()
			m.cachedDur = time.Duration(track.DurationSecs) * time.Second
			m.cachedPos = 0
		}
		m.tickVisualizer(now)
		m.tickProgressReport(now)
		// Process debounced yt-dlp seek.
		var seekCmd tea.Cmd
		if cmd := m.tickSeek(dt); cmd != nil {
			seekCmd = cmd
		}
		// Expire temporary status messages.
		wasStatus := m.status.text != ""
		if !m.status.expiresAt.IsZero() && !now.Before(m.status.expiresAt) {
			m.status.Clear()
		}
		// Drain app log buffer and expire old entries.
		wasLogs := len(m.logLines)
		m.tickLogLines(now)
		if (wasStatus && m.status.text == "") || len(m.logLines) != wasLogs {
			m.applyHeightMode()
			m.adjustScroll()
		}
		m.tickPendingSpeedSave(dt)
		m.tickPendingEQSave(dt)
		if m.pendingSeekActive && !m.pendingSeekExpiresAt.IsZero() && !now.Before(m.pendingSeekExpiresAt) {
			m.pendingSeekActive = false
			m.pendingSeekExpiresAt = time.Time{}
		}
		// Decrement seek grace period.
		advanceTickUnits(&m.seek.grace, &m.seek.graceFor, dt, ui.TickFast)
		// Surface stream errors (e.g., connection drops) and auto-reconnect streams.
		// Suppress during yt-dlp seek and grace period — killing the old pipeline
		// triggers a transient error that can persist for a few ticks.
		if err := m.player.StreamErr(); err != nil && !m.seek.active && m.seek.grace == 0 {
			track, idx := m.currentPlaybackTrack()
			isStream := idx >= 0 && (track.Stream || playlist.IsYouTubeURL(track.Path) || playlist.IsYTDL(track.Path))
			if isStream && m.reconnect.attempts < 5 {
				m.scheduleReconnect(now)
			} else {
				m.err = err
				m.reconnect.at = time.Time{}
			}
		}
		var lyricCmd tea.Cmd
		// Poll ICY stream title for live radio display.
		if title := m.player.StreamTitle(); title != "" && title != m.streamTitle {
			m.streamTitle = title
			m.resetTitleScroll()
			m.applyHeightMode()
			m.adjustScroll()
			// Auto-fetch lyrics when the stream song changes and lyrics overlay is open.
			if m.lyrics.visible && !m.lyrics.loading {
				if artist, song, ok := splitStreamTitle(title); ok {
					track, _ := m.currentPlaybackTrack()
					if q := lyricsLookupKey(track, artist, song); q != m.lyrics.query {
						m.lyrics.query = q
						m.lyrics.loading = true
						m.lyrics.lines = nil
						m.lyrics.err = nil
						m.lyrics.scroll = 0
						lyricCmd = m.fetchLyricsForTrack(track, artist, song)
					}
				}
			}
		}
		m.network.sampleFor += dt
		if m.network.sampleFor >= time.Second {
			downloaded, _ := m.player.StreamBytes()
			delta := downloaded - m.network.lastBytes
			if delta > 0 {
				// Exponential moving average for smooth display.
				instant := float64(delta) / m.network.sampleFor.Seconds() // bytes/sec
				if m.network.speed == 0 {
					m.network.speed = instant
				} else {
					m.network.speed = m.network.speed*0.6 + instant*0.4
				}
			} else if downloaded == 0 {
				m.network.speed = 0
			}
			m.network.lastBytes = downloaded
			m.network.sampleFor = 0
		}
		// Fire scheduled reconnect when the timer expires.
		if !m.reconnect.at.IsZero() && now.After(m.reconnect.at) {
			m.reconnect.at = time.Time{}
			track, idx := m.currentPlaybackTrack()
			m.player.Stop()
			if idx >= 0 {
				// playTrack resets reconnect state for every new start, so carry
				// the live-drain marker and its attempt count across this restart.
				ytdlLiveDrain, attempts := m.reconnect.ytdlLiveDrain, m.reconnect.attempts
				playCmd := m.playTrack(track)
				if ytdlLiveDrain {
					m.reconnect.ytdlLiveDrain, m.reconnect.attempts = true, attempts
				}
				// Preserve any seek/lyric commands already queued this tick
				// rather than dropping them on the early return.
				batch := []tea.Cmd{playCmd, tickCmdAt(ui.TickFast)}
				if seekCmd != nil {
					batch = append(batch, seekCmd)
				}
				if lyricCmd != nil {
					batch = append(batch, lyricCmd)
				}
				return m, tea.Batch(batch...)
			}
		}
		var cmds []tea.Cmd
		if seekCmd != nil {
			cmds = append(cmds, seekCmd)
		}
		if lyricCmd != nil {
			cmds = append(cmds, lyricCmd)
		}
		// Check gapless transition (audio already playing next track)
		gaplessAdvanced := m.player.GaplessAdvanced()
		if gaplessAdvanced {
			// Leave the track that just finished before advancing the playlist.
			// For gapless, the track played fully (100% ≥ 50%), so elapsed = duration.
			// The player stashed the finished pipeline's real duration at swap
			// time; metadata is only a fallback for tracks without it.
			finishedTrack, _ := m.currentPlaybackTrack()
			fullDur := m.player.LastPlayedDuration()
			if fullDur <= 0 {
				fullDur = time.Duration(finishedTrack.DurationSecs) * time.Second
			}
			m.leaveTrack(fullDur, fullDur)

			var newTrack playlist.Track
			var ok bool
			if m.playbackDetached {
				var idx int
				newTrack, idx = m.playlist.Current()
				ok = idx >= 0
				m.playbackDetached = false
			} else {
				newTrack, ok = m.playlist.Next()
				m.normalizeQueueOverlay()
			}
			if !ok {
				m.endQueue()
				cmds = append(cmds, tickCmdAt(m.tickInterval()))
				return m, tea.Batch(cmds...)
			}
			m.plCursor = m.playlist.Index()
			m.adjustScroll()
			var gaplessLyricCmd tea.Cmd
			newTrack, gaplessLyricCmd = m.beginPlaybackTrack(newTrack)
			if gaplessLyricCmd != nil {
				cmds = append(cmds, gaplessLyricCmd)
			}
			// The preload that just fired is consumed — clear the in-flight flag
			// so the next track can be preloaded.
			m.preloading = false
			// A stream decoder error at the track boundary (e.g., server closing
			// the connection when the preload HTTP request opens) is expected and
			// not a user-visible problem. Clear any pending error so the red
			// message doesn't flash at every track transition.
			m.err = nil
			// Gapless advances without calling playTrack(), so emit now-playing here.
			m.nowPlaying(newTrack)
			cmds = append(cmds, m.preloadNext())
		}
		m.tickResumeSave(now)
		// Check if gapless drained (end of playlist, no preloaded next).
		// Skip if already buffering a yt-dlp download to avoid advancing
		// the playlist on every tick while waiting for the resolve.
		if !gaplessAdvanced && m.player.IsPlaying() && !m.player.IsPaused() && m.player.Drained() && !m.buffering && m.reconnect.at.IsZero() {
			finishedTrack, idx := m.currentPlaybackTrack()
			if idx >= 0 && m.currentPlaybackIsLive(finishedTrack) {
				// A live stream has no natural end. A clean decoder EOF is a
				// disconnect, so retry this station instead of advancing.
				m.scheduleReconnect(now)
				m.reconnect.ytdlLiveDrain = playlist.IsYTDL(finishedTrack.Path)
			} else {
				// Track drained to end — always ≥ 50%. The player is still on
				// the finished track here, so its live duration is authoritative
				// even when playlist metadata (DurationSecs) is unknown.
				drainDur := m.player.Duration()
				if drainDur <= 0 {
					drainDur = time.Duration(finishedTrack.DurationSecs) * time.Second
				}
				m.leaveTrack(drainDur, drainDur)

				// Stop the player before dispatching the async nextTrack command.
				// This clears the gapless streamer so the finished track cannot
				// replay while waiting for a yt-dlp pipe chain to spin up.
				m.player.Stop()
				cmds = append(cmds, m.nextTrack())
			}
		}
		m.advanceTitleScroll(now)
		// Retry deferred stream preload: preloadNext() returns nil (defers) when
		// the current stream has >streamPreloadLeadTime remaining. Poll every tick
		// until we're within the window and the preload gets armed.
		// Guard with !m.preloading so we don't fire a second concurrent HTTP
		// connection while the first preloadStreamCmd goroutine is still running,
		// and with !m.tracksPaging because each page of a paged load remixes the
		// upcoming order, so anything armed now would be stale by the next one.
		if m.player.IsPlaying() && !m.player.IsPaused() && !m.buffering && !m.preloading && !m.tracksPaging && !m.player.HasPreload() {
			if cmd := m.preloadNext(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}

		m.advanceTerminalTitle()
		cmds = append(cmds, tickCmdAt(m.tickInterval()))
		return m, tea.Batch(cmds...)

	case openDefaultProviderBrowserMsg:
		if !m.openDefaultProviderOnce {
			return m, nil
		}
		m.openDefaultProviderOnce = false
		cmd := m.openDefaultProviderBrowser()
		return m, cmd

	case radioListsRefreshMsg:
		if msg.gen != m.requests.provider || m.activeProviderKey() != providerKeyRadio {
			return m, nil
		}
		cmd := m.refreshRadioLists()
		return m, cmd

	case playlistsLoadedMsg:
		if msg.gen != m.requests.provider || !m.isActiveProvider(msg.providerName) {
			return m, nil
		}
		m.provLoading = m.provSearch.loading
		if msg.err != nil {
			if errors.Is(msg.err, playlist.ErrNeedsAuth) {
				m.provSignIn = true
				m.err = nil
				return m, nil
			}
			if len(msg.playlists) == 0 {
				m.err = msg.err
				return m, nil
			}
			m.err = nil
			m.status.Warningf(statusTTLLong, "%s", msg.err)
		}
		m.replaceProviderLists(msg.playlists)
		cmd := m.startCatalogLoading()
		return m, cmd

	case tracksLoadedMsg:
		if msg.gen != m.requests.tracks || !m.isActiveProvider(msg.providerName) {
			return m, nil
		}
		m.provLoading = false
		m.tracksPaging = msg.err == nil && msg.next > 0
		if msg.err != nil {
			if errors.Is(msg.err, playlist.ErrNeedsAuth) {
				m.provSignIn = true
				m.err = nil
				return m, nil
			}
			if errors.Is(msg.err, playlist.ErrListChanged) {
				// The list moved under a paged read, so what is on screen is a
				// partial view of a list that no longer exists. Say so and let it
				// expire: reopening starts a clean load, and a persistent error
				// would sit in front of every later status message.
				m.status.Warningf(statusTTLDefault, "Playlist changed while loading — reopen current playlist to reload")
				return m, nil
			}
			m.err = msg.err
			return m, nil
		}
		if msg.offset > 0 {
			m.playlist.Add(msg.tracks...)
			m.normalizeQueueOverlay()
			m.addToHeaderState(msg.tracks)
			// Add mixes the page into the upcoming shuffle order, so an armed
			// preload may no longer be the next track. The gapless swap runs on
			// the audio thread and the model then names the new track from
			// playlist.Next(), so a stale preload would play one track while the
			// UI, scrobble and now-playing announced another. Drop it and let the
			// tick loop re-arm against the order this page produced.
			if m.player.HasPreload() || m.preloading {
				m.player.ClearPreload()
				m.preloading = false
			}
		} else {
			m.replacePlayerPlaylist(msg.tracks)
			if msg.playlistExact {
				m.setLoadedLocalPlaylist(msg.providerName, msg.playlistID)
			}
		}
		if msg.next > 0 {
			m.adjustScroll()
			if pager, ok := m.provider.(provider.TrackPager); ok {
				return m, fetchTracksPageCmd(pager, msg.providerName, msg.playlistID, msg.next, msg.gen)
			}
		}
		if msg.offset > 0 {
			msg.tracks = m.playlist.Tracks()
		}
		m.applyTracksResume(msg)
		m.adjustScroll()
		return m, nil

	case navArtistsLoadedMsg:
		if !m.isCurrentNavRequest(msg.gen) {
			return m, nil
		}
		m.navBrowser.loading = false
		if msg.err != nil {
			m.status.Errorf(statusTTLDefault, "Artist load failed: %s", msg.err)
			return m, nil
		}
		m.navBrowser.artists = msg.artists
		m.navBrowser.cursor = 0
		m.navBrowser.scroll = 0
		return m, nil

	case navAlbumsLoadedMsg:
		if !m.isCurrentNavRequest(msg.gen) {
			return m, nil
		}
		m.navBrowser.albumLoading = false
		m.navBrowser.loading = false
		if msg.err != nil {
			m.status.Errorf(statusTTLDefault, "Album load failed: %s", msg.err)
			return m, nil
		}
		if msg.offset == 0 {
			// Fresh load (new sort or drill-in): replace the list.
			m.navBrowser.albums = msg.albums
			m.navBrowser.albumDone = false
		} else {
			// Lazy-load page: append.
			m.navBrowser.albums = append(m.navBrowser.albums, msg.albums...)
		}
		if msg.isLast {
			m.navBrowser.albumDone = true
		}
		if msg.offset == 0 {
			m.navBrowser.cursor = 0
			m.navBrowser.scroll = 0
		}
		if m.navBrowser.search != "" {
			m.navUpdateSearch()
		}
		// If we just loaded the first page and it was a full menu → list transition,
		// also clear the general loading flag.
		return m, nil

	case navGenresLoadedMsg:
		if !m.isCurrentNavRequest(msg.gen) {
			return m, nil
		}
		m.navBrowser.loading = false
		if msg.err != nil {
			m.status.Errorf(statusTTLDefault, "Genre load failed: %s", msg.err)
			return m, nil
		}
		m.navBrowser.genres = msg.genres
		m.navBrowser.cursor = 0
		m.navBrowser.scroll = 0
		return m, nil

	case navTracksLoadedMsg:
		if !m.isCurrentNavRequest(msg.gen) {
			return m, nil
		}
		m.navBrowser.loading = false
		if msg.err != nil {
			m.status.Errorf(statusTTLDefault, "Track load failed: %s", msg.err)
			return m, nil
		}
		if m.navBrowser.openInPlaylist {
			if len(msg.tracks) == 0 {
				m.status.Warning("No tracks found", statusTTLDefault)
				return m, nil
			}
			m.retireTracksPaging()
			m.replacePlayerPlaylist(msg.tracks)
			m.activeProviderPlaylistID = ""
			if pr, ok := m.navBrowser.prov.(playlist.RefreshablePlaylist); ok &&
				m.isActiveProvider(m.navBrowser.prov.Name()) && pr.CanRefreshPlaylist(m.navBrowser.selAlbum.ID) {
				m.activeProviderPlaylistID = m.navBrowser.selAlbum.ID
			}
			m.navBrowser.visible = false
			m.status.Successf(statusTTLDefault, "Replaced queue with %d tracks", len(msg.tracks))
			return m, nil
		}
		m.navBrowser.tracks = msg.tracks
		m.setHeaderStateFromTracks(m.navBrowser.tracks)
		m.navBrowser.cursor = 0
		m.navBrowser.scroll = 0
		m.navBrowser.screen = navBrowseScreenTracks
		return m, nil

	case catalogBatchMsg:
		if msg.gen != m.requests.catalog || !m.isActiveProvider(msg.providerName) {
			return m, nil
		}
		m.catalogBatch.loading = false
		if msg.err != nil {
			m.catalogBatch.done = true
			m.status.Errorf(statusTTLDefault, "Catalog load failed: %s", msg.err)
			return m, nil
		}
		if msg.added == 0 {
			m.catalogBatch.done = true
			return m, nil
		}
		if err := m.refreshProviderListsNow(); err != nil {
			m.err = err
		}
		m.catalogBatch.offset += msg.added
		if msg.added < catalogBatchSize {
			m.catalogBatch.done = true
		}
		return m, nil

	case catalogSearchMsg:
		if msg.gen != m.requests.catalog || !m.isActiveProvider(msg.providerName) {
			return m, nil
		}
		m.provLoading = false
		m.provSearch.loading = false
		if msg.err != nil {
			m.status.Errorf(statusTTLDefault, "Search failed: %s", msg.err)
		} else {
			if err := m.refreshProviderListsNow(); err != nil {
				m.err = err
			}
			m.provCursor = 0
			m.provScroll = 0
			if msg.count == 0 {
				m.status.Warning("No results found", statusTTLDefault)
			}
		}
		return m, nil

	case ytdlBatchMsg:
		// Discard stale responses from a previous batch session.
		if msg.gen != m.ytdlBatch.gen {
			return m, nil
		}
		m.ytdlBatch.loading = false
		if msg.err != nil {
			m.ytdlBatch.done = true
			m.status.Errorf(statusTTLBatch, "Radio batch load failed: %v", msg.err)
			return m, nil
		}
		if len(msg.tracks) == 0 {
			m.ytdlBatch.done = true
			return m, nil
		}
		m.appendTracks(msg.tracks...)
		m.ytdlBatch.offset += len(msg.tracks)
		if len(msg.tracks) < ytdlBatchSize {
			m.ytdlBatch.done = true
			return m, nil
		}
		// Immediately fetch the next batch.
		m.ytdlBatch.loading = true
		return m, fetchYTDLBatchCmd(m.ytdlBatch.gen, m.ytdlBatch.url, m.ytdlBatch.offset, ytdlBatchSize)

	case feedTrackResolvedMsg:
		m.feedLoading = false
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		if len(msg.tracks) == 0 {
			m.status.Warning("No episodes found in feed.", statusTTLDefault)
			return m, nil
		}
		m.retireTracksPaging()
		m.replacePlaylist(msg.tracks)
		m.clearLoadedPlaylist()
		m.setHeaderStateFromTracks(msg.tracks)
		m.plCursor = 0
		m.plScroll = 0
		m.applyHeightMode()
		m.adjustScroll()
		m.status.Showf(statusTTLDefault, "Loaded %d episode(s)", len(msg.tracks))
		playCmd := m.playCurrentTrack()
		return m, playCmd

	case subsEpisodesMsg:
		cmd := m.handleSubsEpisodes(msg)
		return m, cmd

	case subsLatestAllMsg:
		cmd := m.handleSubsLatestAll(msg)
		return m, cmd

	case feedsLoadedMsg:
		m.feedLoading = false
		if msg.err != nil {
			m.err = msg.err
			applog.Warn("load URLs: %v", msg.err)
			return m, nil
		}
		if len(msg.tracks) > 0 {
			m.appendTracks(msg.tracks...)
			m.status.Showf(statusTTLDefault, "Loaded %d track(s)", len(msg.tracks))
		} else {
			m.status.Warning("No tracks found at URL.", statusTTLDefault)
		}
		if len(msg.tracks) > 0 {
			// Set up incremental loading for YouTube Radio playlists.
			// The source URLs are carried in the message so we don't
			// need to re-scan pendingURLs (which misses interactive loads).
			batchCmd := m.initYTDLBatch(msg.urls)
			if msg.autoPlay && m.playlist.Len() > 0 && !m.player.IsPlaying() {
				playCmd := m.playCurrentTrack()
				if batchCmd != nil {
					return m, tea.Batch(playCmd, batchCmd)
				}
				return m, playCmd
			}
			if batchCmd != nil {
				return m, batchCmd
			}
		}
		return m, nil

	case netSearchResultsMsg:
		if msg.gen != m.requests.netSearch || !m.netSearch.active || msg.query != m.netSearch.request {
			return m, nil
		}
		m.netSearch.loading = false
		m.netSearch.cursor = 0
		m.netSearch.scroll = 0
		if msg.err != nil {
			m.netSearch.err = msg.err.Error()
			return m, nil
		}
		m.netSearch.results = msg.tracks
		m.netSearch.cursor = 0
		m.netSearch.screen = netSearchResults
		if len(msg.tracks) == 0 {
			m.netSearch.err = "No results found"
		}
		m.applyHeightMode()
		m.clampActiveScrollState()
		return m, nil

	case lyricsLoadedMsg:
		if msg.gen != m.requests.lyrics || !m.lyrics.visible || msg.query != m.lyrics.query {
			return m, nil
		}
		m.lyrics.loading = false
		m.lyrics.err = msg.err
		m.lyrics.scroll = 0
		if msg.err == nil {
			m.lyrics.lines = msg.lines
		}
		return m, nil

	case fbTracksResolvedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		if len(msg.tracks) == 0 {
			m.status.Warning("No audio files found", statusTTLDefault)
			return m, nil
		}
		if msg.targetPlaylist != "" {
			added, skipped, err := m.writeTracksToPlaylist(msg.targetPlaylist, msg.tracks)
			if err != nil {
				m.status.Errorf(statusTTLDefault, "Add failed: %s", err)
			} else if skipped > 0 {
				m.status.Warningf(statusTTLBatch, "Added %d to %q, skipped %d duplicates", added, msg.targetPlaylist, skipped)
			} else if added > 0 {
				m.status.Showf(statusTTLDefault, "Added %d to %q", added, msg.targetPlaylist)
			} else {
				m.status.Warningf(statusTTLDefault, "Nothing added to %q", msg.targetPlaylist)
			}
			m.refreshPlaylistManagerAfterWrite(msg.targetPlaylist)
			// Track/dir counts in the provider pane come from Playlists();
			// re-pull now that the file write has landed.
			cmd := m.refreshPaneAfterLocalWrite()
			return m, cmd
		}
		if msg.toPlaylist {
			m.openPlaylistPicker(msg.tracks, fmt.Sprintf("%d tracks selected", len(msg.tracks)))
			return m, nil
		}
		if msg.replace {
			m.stopPlayback()
			m.player.ClearPreload()
			m.resetYTDLBatch()
			m.retireTracksPaging()
			m.replacePlaylist(msg.tracks)
			m.clearLoadedPlaylist()
			m.setHeaderStateFromTracks(msg.tracks)
			m.plCursor = 0
			m.plScroll = 0
		} else {
			m.appendTracks(msg.tracks...)
		}
		m.focus = focusPlaylist
		m.applyHeightMode()
		m.adjustScroll()
		if msg.replace {
			m.status.Successf(statusTTLDefault, "Replaced queue with %d track(s)", len(msg.tracks))
		} else {
			m.status.Successf(statusTTLDefault, "Added %d track(s)", len(msg.tracks))
		}
		if !m.player.IsPlaying() && m.playlist.Len() > 0 {
			if msg.replace {
				m.playlist.SetIndex(0)
			}
			cmd := m.playCurrentTrack()
			return m, cmd
		}
		return m, nil

	case streamPlayedMsg:
		track, _ := m.currentPlaybackTrack()
		if msg.gen != m.requests.stream || msg.path != track.Path {
			return m, nil
		}
		m.buffering = false
		ytdlLiveDrain := m.reconnect.ytdlLiveDrain
		m.reconnect.ytdlLiveDrain = false
		if msg.err != nil && ytdlLiveDrain {
			// The drained live stream did not restart. The cause may be a
			// network outage or the end of the broadcast, so retry with
			// backoff before giving up on it and advancing.
			m.player.Stop()
			if m.reconnect.attempts < ytdlLiveDrainRestarts {
				m.scheduleReconnect(time.Now())
				m.reconnect.ytdlLiveDrain = true
				return m, nil
			}
			m.reconnect.attempts = 0
			cmd := m.nextTrack()
			return m, cmd
		}
		var resumeCmd tea.Cmd
		if errors.Is(msg.err, playlist.ErrNeedsAuth) {
			// The provider session went stale, for example after Spotify
			// rejected the stream keys. Ask for sign-in, not a raw error.
			m.provSignIn = true
			m.err = nil
			m.status.Warningf(statusTTLLong, "Sign-in required to play %s.", track.DisplayName())
		} else if msg.err != nil {
			m.err = msg.err
			applog.Warn("play %q: %v", msg.path, msg.err)
			if track, idx := m.currentPlaybackTrack(); idx >= 0 {
				m.status.Errorf(statusTTLLong, "Couldn't play %s — track is gated, restricted, or unavailable.", track.DisplayName())
			}
		} else {
			m.err = nil
			m.reconnect.attempts = 0
			m.reconnect.at = time.Time{}
			resumeCmd = m.applyResume()
			m.nowPlaying(track)
		}
		preloadCmd := m.preloadNext()
		return m, tea.Batch(resumeCmd, preloadCmd)

	case streamPreloadedMsg:
		if msg.gen != m.requests.preload {
			return m, nil
		}
		m.preloading = false
		if msg.err != nil {
			// Playback falls back to a non-gapless start for this track.
			// Retrying on the next tick would rebuild the failing pipeline.
			m.preloadFailed = msg.path
		}
		return m, nil

	case trackSavedMsg:
		if msg.download {
			m.save.finishDownload()
		}
		switch {
		case msg.err == nil:
			m.status.Showf(statusTTLMedium, "Saved to %s", msg.path)
		case msg.download:
			m.status.Errorf(statusTTLMedium, "Download failed: %s", msg.err)
		default:
			m.status.Errorf(statusTTLShort, "Save failed: %s", msg.err)
		}
		return m, nil

	case spotSearchResultsMsg:
		if !m.isCurrentSpotRequest(msg.gen, msg.providerName) || m.spotSearch.query != msg.query {
			return m, nil
		}
		m.cancelSpotRequest()
		m.spotSearch.loading = false
		m.spotSearch.cursor = 0
		m.spotSearch.scroll = 0
		if msg.err != nil {
			m.setSpotSearchError(msg.err.Error())
			return m, nil
		}
		m.spotSearch.results = msg.tracks
		m.spotSearch.cursor = 0
		m.spotSearch.screen = spotSearchResults
		m.applyHeightMode()
		m.clampActiveScrollState()
		return m, nil

	case spotAlbumTracksMsg:
		if msg.gen != m.requests.spotAlbum {
			return m, nil
		}
		m.cancelSpotRequest()
		m.spotSearch.albumLoading = false
		if msg.err != nil {
			m.setSpotSearchError(msg.err.Error())
			return m, nil
		}
		if len(msg.tracks) == 0 {
			m.setSpotSearchError("That album has no tracks available here.")
			return m, nil
		}
		album := msg.album
		tracks := msg.tracks
		m.closeSpotSearch()
		switch msg.action {
		case spotAlbumAppend:
			cmd := m.appendAlbum(album, tracks)
			return m, cmd
		case spotAlbumQueueNext:
			cmd := m.queueAlbumNext(album, tracks)
			return m, cmd
		default:
			cmd := m.playAlbumImmediate(album, tracks)
			return m, cmd
		}

	case spotPlaylistsMsg:
		if !m.isCurrentSpotListRequest(msg.gen, msg.providerName) {
			return m, nil
		}
		m.spotSearch.loading = false
		m.spotSearch.cursor = 0
		m.spotSearch.scroll = 0
		if msg.err != nil {
			m.setSpotSearchError(msg.err.Error())
			return m, nil
		}
		m.spotSearch.playlists = msg.playlists
		m.spotSearch.cursor = 0
		m.spotSearch.screen = spotSearchPlaylist
		m.applyHeightMode()
		m.clampActiveScrollState()
		return m, nil

	case spotAddedMsg:
		if !m.isCurrentSpotMutation(msg.gen, msg.providerName) {
			return m, nil
		}
		m.cancelSpotRequest()
		m.spotSearch.loading = false
		if msg.err != nil {
			m.setSpotSearchError("Add failed: " + msg.err.Error())
			return m, nil
		}
		m.status.Showf(statusTTLDefault, "Added to %q", msg.name)
		m.closeSpotSearch()
		return m, nil

	case spotCreatedMsg:
		if !m.isCurrentSpotMutation(msg.gen, msg.providerName) {
			return m, nil
		}
		m.cancelSpotRequest()
		m.spotSearch.loading = false
		if msg.err != nil {
			m.setSpotSearchError("Create failed: " + msg.err.Error())
			return m, nil
		}
		m.status.Showf(statusTTLDefault, "Created %q & added track", msg.name)
		m.closeSpotSearch()
		return m, nil

	case provAuthDoneMsg:
		if msg.gen != m.requests.auth || !m.isActiveProvider(msg.providerName) {
			return m, nil
		}
		m.provAuthURL = ""
		if msg.err != nil {
			// Keep the sign-in prompt, so Enter retries without a restart.
			m.err = msg.err
			m.provLoading = false
			m.provSignIn = true
			return m, nil
		}
		m.err = nil
		m.provSignIn = false
		m.provLoading = true
		cmd := m.fetchProviderPlaylists()
		return m, cmd

	case ProvAuthURLMsg:
		if !m.provLoading || !m.isActiveProvider(msg.ProviderName) {
			return m, nil
		}
		m.provAuthURL = msg.URL
		return m, nil

	case devicesListedMsg:
		m.devicePicker.loading = false
		if msg.err != nil {
			m.status.Errorf(statusTTLDefault, "Device list failed: %s", msg.err)
			m.devicePicker.visible = false
		} else {
			m.devicePicker.devices = msg.devices
		}
		return m, nil

	case deviceSwitchedMsg:
		if msg.err != nil {
			m.status.Errorf(statusTTLDefault, "Switch failed: %s", msg.err)
		} else {
			m.status.Showf(statusTTLDefault, "Audio output: %s", msg.name)
			m.audioDevice = msg.name
			_ = m.saveConfigString("audio_device", msg.name)
		}
		// Invalidate cached list so the next open refreshes Active markers.
		m.devicePicker.devices = nil
		return m, nil

	case attachNotifierMsg:
		m.attachNotifier(msg.notifier)
		return m, nil

	case playback.PlayPauseMsg:
		cmd := m.togglePlayPause()
		return m, cmd

	case playback.PlayMsg:
		if !m.player.IsPlaying() || m.player.IsPaused() {
			cmd := m.togglePlayPause()
			return m, cmd
		}
		return m, nil

	case playback.PauseMsg:
		if m.player.IsPlaying() && !m.player.IsPaused() {
			m.togglePlayerPause()
		}
		return m, nil

	case playback.NextMsg:
		cmd := m.skipNext()
		return m, cmd

	case playback.PrevMsg:
		cmd := m.skipPrev()
		return m, cmd

	case playback.SeekMsg:
		cmd := m.seekRelative(msg.Offset, 0)
		return m, cmd

	case playback.SetPositionMsg:
		cmd := m.seekAbsolute(msg.Position)
		return m, cmd

	case playback.SetVolumeMsg:
		m.setVolume(msg.VolumeDB)
		return m, nil

	case playback.SetSpeedMsg:
		m.setSpeed(msg.Ratio)
		return m, nil

	case playback.ToggleMonoMsg:
		m.player.ToggleMono()
		return m, nil

	case playback.StopMsg:
		m.stopByUser()
		return m, nil

	case playback.QuitMsg:
		// Media controls and the signals of headless mode quit like the q
		// key, so the resume position is kept too.
		cmd := m.quit()
		return m, cmd

	case SetEQPresetMsg:
		m.SetEQPreset(msg.Name, msg.Bands)
		m.scheduleEQSave()
		return m, nil

	case SetEQBandMsg:
		m.setCustomEQBand(msg.Band, msg.Gain)
		return m, nil

	case PluginQueueMsg:
		cmd := m.handlePluginQueue(msg)
		return m, cmd

	case pluginQueueAddedMsg:
		cmd := m.appendPluginTracks(msg.tracks...)
		return m, cmd

	case trackFavoriteSyncedMsg:
		m.handleTrackFavoriteSynced(msg)
		return m, nil

	case ShowStatusMsg:
		ttl := statusTTLDefault
		if msg.Duration > 0 {
			ttl = statusTTL(msg.Duration)
		}
		m.status.Show(msg.Text, ttl)
		return m, nil

	case ipcProviderLoadResult:
		cmd := m.handleIPCProviderLoad(msg)
		return m, cmd

	case ipcFeedLoadResult:
		cmd := m.handleIPCFeedLoad(msg)
		return m, cmd

	case ipcURLLoadResult:
		cmd := m.handleIPCURLResult(msg)
		return m, cmd

	case V2RequestMsg:
		cmd := m.handleV2Request(msg)
		return m, cmd

	case ipcV2ResponseMsg:
		if msg.Response.OK {
			if msg.Operation == "device" {
				m.applyV2DeviceResponse(msg.Response)
			}
			m.completeV2Job(msg.Jobs, msg.JobID, msg.Response)
		} else {
			err := v2InternalError()
			err.Detail = msg.Response.Error
			m.failV2Job(msg.Jobs, msg.JobID, err)
		}
		return m, nil

	}

	return m, nil
}
