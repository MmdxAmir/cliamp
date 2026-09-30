package model

import (
	"math"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui"
)

type tickMsg time.Time
type autoPlayMsg struct{}

// spinnerTickMsg redraws the view so that a loading spinner advances. It runs
// beside the main tick, which can still wait up to ui.TickIdle when a load
// starts.
type spinnerTickMsg struct{}

func spinnerTickCmd() tea.Cmd {
	return teaTick(spinnerInterval, func(time.Time) tea.Msg { return spinnerTickMsg{} })
}

var teaTick = tea.Tick

func tickCmd() tea.Cmd {
	return tickCmdAt(ui.TickFast)
}

func tickCmdAt(d time.Duration) tea.Cmd {
	return teaTick(d, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m Model) visualizerVisible() bool {
	if m.simplified || m.vis == nil || m.vis.Mode == ui.VisNone || m.vis.Rows <= 0 || m.vis.Cols <= 0 || m.layout.tooSmall() {
		return false
	}
	if m.fullVis {
		return true
	}
	return m.layout.visualizerRows > 0 && !m.usesContentFirstLayout()
}

func (m *Model) visualizerPlaying() bool {
	return m.player != nil && m.visualizerVisible() &&
		!m.isOverlayActive() && m.player.IsPlaying() && !m.player.IsPaused()
}

func (m *Model) visualizerPaused() bool {
	return m.player != nil && m.visualizerVisible() &&
		!m.isOverlayActive() && m.player.IsPlaying() && m.player.IsPaused()
}

// visualizerSettlingPaused reports whether playback is paused and the
// visualizer still has spectrum content easing to zero. While true the tick
// stays above the idle cadence so the bars fall; once settled the model can
// return to the fully-idle cadence.
func (m *Model) visualizerSettlingPaused() bool {
	if m.player == nil || !m.player.IsPaused() || m.isOverlayActive() {
		return false
	}
	if !m.visualizerVisible() {
		return false
	}
	return m.vis.PausedDecayPending(m.visualizerTickContext(time.Time{}))
}

func (m *Model) visualizerTickContext(now time.Time) ui.VisTickContext {
	sampled := false
	samplesRead := 0
	sampledSize := 0
	cache := map[ui.VisAnalysisSpec][]float64{}

	return ui.VisTickContext{
		Now:           now,
		Playing:       m.visualizerPlaying(),
		Paused:        m.visualizerPaused(),
		OverlayActive: m.isOverlayActive(),
		StereoSamplesInto: func(dst [][2]float64) int {
			if m.player == nil || m.vis == nil || m.vis.Mode == ui.VisNone {
				return 0
			}
			n := m.player.StereoSamplesInto(dst)
			gain := 1.0
			if m.visVolumeLinked {
				gain = math.Pow(10, m.player.Volume()/20)
			}
			mono := m.player.Mono()
			for i := range n {
				if mono {
					mixed := (dst[i][0] + dst[i][1]) / 2
					dst[i] = [2]float64{mixed, mixed}
				}
				dst[i][0] *= gain
				dst[i][1] *= gain
			}
			return n
		},
		Analyze: func(spec ui.VisAnalysisSpec) []float64 {
			spec = ui.NormalizeAnalysisSpec(spec)
			if m.player == nil || m.vis == nil || m.vis.Mode == ui.VisNone {
				return nil
			}
			if bands, ok := cache[spec]; ok {
				return bands
			}
			if m.player.IsPaused() {
				// Paused playback yields no new samples; feed silence so
				// spectrum content eases down to rest instead of freezing on
				// the last played frame held in the audio tap.
				bands := m.vis.Analyze(nil, spec)
				cache[spec] = bands
				return bands
			}
			buf := m.vis.EnsureSampleBuf(spec.FFTSize)
			if !sampled || spec.FFTSize > sampledSize {
				if spec.Tap == ui.VisTapAudible {
					samplesRead = m.player.WaveformSamplesInto(buf)
				} else {
					samplesRead = m.player.SamplesInto(buf)
				}
				if m.visVolumeLinked {
					gain := math.Pow(10, m.player.Volume()/20)
					for i := range samplesRead {
						buf[i] *= gain
					}
				}
				sampled = true
				sampledSize = spec.FFTSize
			}
			start := max(0, samplesRead-spec.FFTSize)
			bands := m.vis.Analyze(buf[start:samplesRead], spec)
			cache[spec] = bands
			return bands
		},
	}
}

func (m *Model) tickDelta(now time.Time) time.Duration {
	dt := m.tickInterval()
	if !now.IsZero() && !m.lastTickAt.IsZero() {
		dt = now.Sub(m.lastTickAt)
	}
	if dt <= 0 {
		dt = ui.TickFast
	}
	if !now.IsZero() {
		m.lastTickAt = now
	}
	return dt
}

func advanceTickUnits(counter *int, elapsed *time.Duration, dt, quantum time.Duration) int {
	if *counter <= 0 {
		*elapsed = 0
		return 0
	}
	*elapsed += dt
	if *elapsed < quantum {
		return 0
	}
	steps := min(int(*elapsed/quantum), *counter)
	*counter -= steps
	if *counter == 0 {
		*elapsed = 0
		return steps
	}
	*elapsed -= time.Duration(steps) * quantum
	return steps
}

func (m *Model) tickInterval() time.Duration {
	if m.termTitle.introActive {
		return ui.TickFast
	}
	// Fully idle: stopped or paused with nothing self-animating. Drop to the
	// idle cadence so the CPU can sit in a low P-state between user actions.
	// Bubbletea still wakes immediately on key / IPC / MPRIS / plugin events.
	if m.isFullyIdle() {
		return ui.TickIdle
	}
	d := ui.TickSlow
	if m.visualizerVisible() {
		d = m.vis.TickInterval(m.visualizerTickContext(time.Time{}))
	}
	if m.spinnerVisible() {
		d = min(d, spinnerInterval)
	}
	// Keep the seek bar / time counter smooth while audio is playing, even
	// when the visualizer driver wants a slow cadence (VisNone, classic peak
	// idle, etc.). Overlays, paused, and stopped playback keep the slower
	// cadence to save CPU.
	if !m.isOverlayActive() && !m.buffering && m.player != nil &&
		m.player.IsPlaying() && !m.player.IsPaused() {
		if m.lowPower {
			return ui.TickLowPowerPlaying
		}
		if m.visualizerVisible() && (m.visualizer60FPS || m.vis.UsesRawSamples()) {
			return ui.TickAnim
		}
		if m.visualizerVisible() && m.vis.DriverOwnsCadence() {
			return min(d, ui.TickFast)
		}
		return ui.TickFast
	}
	// Paused visualizer content still easing to rest: run at the fast cadence
	// so the bars fall smoothly instead of in ~5 fps steps, then drop to idle
	// once the content has settled.
	if m.visualizerSettlingPaused() {
		if m.visualizer60FPS {
			return ui.TickAnim
		}
		return ui.TickFast
	}
	return max(d, ui.TickFast)
}

// isFullyIdle reports whether the model has nothing changing on its own.
// When true, the tick can run at ui.TickIdle since any state change will
// arrive as an explicit message (key press, IPC, MPRIS, plugin send).
func (m *Model) isFullyIdle() bool {
	if m.player == nil {
		return false
	}
	if m.player.IsPlaying() && !m.player.IsPaused() {
		return false
	}
	if m.visualizerSettlingPaused() {
		return false
	}
	if m.isOverlayActive() || m.buffering || m.termTitle.introActive || m.spinnerVisible() {
		return false
	}
	if !m.status.expiresAt.IsZero() || len(m.logLines) > 0 {
		return false
	}
	if !m.reconnect.at.IsZero() {
		return false
	}
	return true
}

// spinnerVisible reports whether a loading spinner is on the screen. The tick
// then redraws at least every spinnerInterval so that the frames advance.
func (m *Model) spinnerVisible() bool {
	return m.provLoading || m.provSearch.loading || m.catalogBatch.loading || m.feedLoading ||
		(m.lyrics.visible && m.lyrics.loading) ||
		(m.netSearch.active && m.netSearch.loading) ||
		(m.spotSearch.visible && (m.spotSearch.loading || m.spotSearch.albumLoading)) ||
		(m.navBrowser.visible && (m.navBrowser.loading || m.navBrowser.albumLoading)) ||
		(m.devicePicker.visible && m.devicePicker.loading) ||
		(m.subs.visible && m.subs.loading)
}

func (m *Model) tickVisualizer(now time.Time) {
	if m.vis == nil {
		return
	}
	if !m.visualizerVisible() {
		m.vis.Suspend()
		return
	}
	m.vis.Tick(m.visualizerTickContext(now))
}

func (m Model) refreshVisualizerIfPending() {
	if m.vis == nil {
		return
	}
	if !m.visualizerVisible() {
		m.vis.Suspend()
		return
	}
	if !m.vis.ConsumeRefresh() {
		return
	}
	m.tickVisualizer(time.Now())
}

func (m Model) maybeRequestVisualizerRefresh(msg tea.Msg, wasScreen topLevelScreen, wasVisible bool, wasMode ui.VisMode, wasPlaying, wasPaused bool) {
	if m.vis == nil {
		return
	}
	if _, ok := msg.(tickMsg); ok {
		return
	}
	screen := m.activeScreen()
	if !m.visualizerVisible() {
		m.vis.Suspend()
		return
	}

	playing := false
	paused := false
	if m.player != nil {
		playing = m.player.IsPlaying()
		paused = m.player.IsPaused()
	}
	if paused {
		m.vis.Suspend()
		return
	}

	if !wasVisible ||
		wasScreen != screen ||
		wasMode != m.vis.Mode ||
		(!wasPlaying && playing) ||
		(wasPaused && !paused) {
		m.vis.RequestRefresh()
	}
}

// handleTick samples the player, runs the timed jobs, advances past a
// finished track and schedules the next tick.
func (m *Model) handleTick(msg tickMsg) tea.Cmd {
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
			return tea.Batch(batch...)
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
			return tea.Batch(cmds...)
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
	return tea.Batch(cmds...)
}
