package model

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/internal/playback"
	"github.com/bjarneo/cliamp/ui"
)

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
		cmd := m.handleSeekTick(msg)
		return m, cmd

	case ytdlUnpauseReconnectMsg:
		m.handleYTDLUnpauseReconnect(msg)
		return m, nil

	case tickMsg:
		cmd := m.handleTick(msg)
		return m, cmd

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
		cmd := m.handlePlaylistsLoaded(msg)
		return m, cmd

	case tracksLoadedMsg:
		cmd := m.handleTracksLoaded(msg)
		return m, cmd

	case navArtistsLoadedMsg:
		m.handleNavArtistsLoaded(msg)
		return m, nil

	case navAlbumsLoadedMsg:
		m.handleNavAlbumsLoaded(msg)
		return m, nil

	case navGenresLoadedMsg:
		m.handleNavGenresLoaded(msg)
		return m, nil

	case navTracksLoadedMsg:
		m.handleNavTracksLoaded(msg)
		return m, nil

	case catalogBatchMsg:
		m.handleCatalogBatch(msg)
		return m, nil

	case catalogSearchMsg:
		m.handleCatalogSearch(msg)
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
		cmd := m.handleStreamPlayed(msg)
		return m, cmd

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
		m.handleTrackSaved(msg)
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
		cmd := m.handleProvAuthDone(msg)
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
