package model

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
	"github.com/bjarneo/cliamp/tracksave"
)

// quit shuts down the player and signals the TUI to exit.
func (m *Model) quit() tea.Cmd {
	// Only save resume for seekable tracks:
	// - local files (not stream)
	// - HTTP streams with known duration (podcast MP3s, seek-by-reconnect)
	// - finite Mixcloud shows (yt-dlp tracks with a counted PCM position)
	// Other yt-dlp sites and real-time live streams remain excluded.
	if track, _ := m.currentPlaybackTrack(); track.Path != "" &&
		(!playlist.IsYTDL(track.Path) || playlist.IsMixcloudURL(track.Path)) &&
		!track.IsLive() &&
		m.player.IsPlaying() && !m.buffering && !m.player.GaplessAdvanced() {
		if secs := int(m.player.Position().Seconds()); secs > 0 {
			context, contextIndex := m.playbackContextFor(track)
			m.exitResume.path = track.Path
			m.exitResume.secs = secs
			m.exitResume.playlist = m.loadedPlaylist
			m.exitResume.context = cloneTracks(context)
			m.exitResume.contextIndex = contextIndex
		}
	}

	m.flushPendingSpeedSave()
	m.flushPendingEQSave()
	m.player.Close()
	m.clearPlaybackTrack()
	m.quitting = true
	return tea.Quit
}

func (m *Model) handleSpeedKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "q", "ctrl+c":
		return m.quit()
	case "]", "right", "l", "up", "k":
		m.changeSpeed(0.25)
	case "[", "left", "h", "down", "j":
		m.changeSpeed(-0.25)
	case "tab":
		m.focus = m.nextMainFocus(focusSpeed)
	case "shift+tab", "esc", "backspace":
		m.focus = m.previousMainFocus(focusSpeed)
	case "space":
		return m.togglePlayPause()
	}
	return nil
}

func (m *Model) providerScrollStep() int {
	return max(1, m.effectivePlaylistVisible())
}

func (m *Model) providerMaybeAdjustScroll() {
	visible := m.providerScrollStep()
	total := len(m.providerLists)
	if total == 0 {
		m.provScroll = 0
		return
	}

	if m.provCursor < m.provScroll {
		m.provScroll = m.provCursor
	}

	if m.provScroll >= total {
		m.provScroll = max(0, total-1)
	}

	// Provider lists can add radio-prefix or PlaylistInfo.Section headers. Keep
	// the logical cursor visible in their rendered-row viewport.
	for m.provScroll < total && m.providerRowsFromScroll(m.provScroll, m.provCursor) > visible {
		m.provScroll++
	}
}

func (m *Model) providerRowsFromScroll(scroll, cursor int) int {
	total := len(m.providerLists)
	if total == 0 || cursor < scroll || scroll < 0 || cursor >= total {
		return 0
	}

	rows := 0
	sl, isRadio := m.provider.(provider.SectionedList)
	// Must resolve the same heading the renderer does, or the two disagree on
	// how many rows a window holds and the cursor scrolls out of view.
	headerAt := func(i int) string {
		if isRadio {
			return m.providerSectionTitle(sl.IDPrefix(m.providerLists[i].ID))
		}
		return m.providerLists[i].Section
	}

	prevHeader := ""
	if scroll > 0 {
		prevHeader = headerAt(scroll - 1)
	}

	for i := scroll; i <= cursor && i < total; i++ {
		header := headerAt(i)
		if header != "" && header != prevHeader {
			rows++ // section header row
		}
		rows++ // item row
		prevHeader = header
	}
	return rows
}

func (m *Model) providerMoveUp() {
	if m.provCursor > 0 {
		m.provCursor--
	} else if len(m.providerLists) > 0 {
		m.provCursor = len(m.providerLists) - 1
	}
	m.providerMaybeAdjustScroll()
}

func (m *Model) providerMoveDown() {
	if m.provCursor < len(m.providerLists)-1 {
		m.provCursor++
	} else if len(m.providerLists) > 0 {
		m.provCursor = 0
	}
	m.providerMaybeAdjustScroll()
}

func (m *Model) providerPageUp() {
	step := m.providerScrollStep()
	if m.provCursor > 0 {
		m.provCursor -= min(m.provCursor, step)
	}
	// Top-anchor behavior: place cursor at top of viewport when paging up.
	m.provScroll = m.provCursor
	m.providerMaybeAdjustScroll()
}

func (m *Model) providerPageDown() {
	step := m.providerScrollStep()
	if m.provCursor < len(m.providerLists)-1 {
		m.provCursor = min(len(m.providerLists)-1, m.provCursor+step)
	}
	// Bottom-anchor behavior: bias viewport so cursor lands near bottom when paging down.
	m.provScroll = max(0, m.provCursor-step+1)
	m.providerMaybeAdjustScroll()
}

func (m *Model) providerToTop() {
	m.provCursor = 0
	m.providerMaybeAdjustScroll()
}

func (m *Model) providerToBottom() {
	if len(m.providerLists) > 0 {
		m.provCursor = len(m.providerLists) - 1
	}
	m.providerMaybeAdjustScroll()
}

func normalizeShiftedLetter(msg tea.KeyPressMsg) tea.KeyPressMsg {
	if msg.Text != "" || msg.Mod != tea.ModShift ||
		msg.Code < 'a' || msg.Code > 'z' ||
		msg.ShiftedCode < 'A' || msg.ShiftedCode > 'Z' {
		return msg
	}
	msg.Text = string(msg.ShiftedCode)
	return msg
}

// handleKey processes a single key press and returns an optional command.
func (m *Model) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	msg = normalizeShiftedLetter(msg)

	if msg.String() == "ctrl+c" {
		return m.quit()
	}
	if msg.String() == "ctrl+z" {
		var cmd tea.Cmd
		m.keepPlCursorRow(func() { cmd = m.undoPlaylistMutation() })
		return cmd
	}
	if msg.String() == "ctrl+k" && !m.keymap.visible {
		if m.fullVis {
			m.exitFullVisualizer()
		}
		m.openKeymap()
		return nil
	}
	if m.width > 0 && m.layout.tooSmall() {
		if msg.String() == "q" {
			return m.quit()
		}
		return nil
	}
	// The top overlay owns the keys.
	if spec, ok := m.topOverlay(); ok {
		return spec.key(m, msg)
	}

	if m.provSearch.active {
		return m.handleProvSearchKey(msg)
	}
	if m.focus != focusProvider {
		switch msg.String() {
		case "ctrl+i":
			m.toggleMetadata()
			return nil
		case "i":
			m.showInfo = true
			m.infoScroll = 0
			return nil
		}
	}

	if m.focus == focusProvider {
		// The location question owns the keyboard until it is answered: it is
		// a yes/no about the listener's own data, so it must not be dismissed
		// by a stray key that happens to mean something else in this pane.
		if m.provAskLoc {
			switch msg.String() {
			case "y", "Y", "enter":
				return m.answerLocationPrompt(true)
			case "n", "N", "esc":
				return m.answerLocationPrompt(false)
			case "ctrl+c":
				return m.quit()
			}
			return nil
		}

		switch msg.String() {
		case "q", "ctrl+c":
			return m.quit()
		case "F":
			if !m.openSubsOverlay() && m.luaMgr != nil {
				m.luaMgr.EmitKey(msg.String())
			}
		case "l":
			return m.loadLatestFromProviderList()
		case "a":
			return m.appendShowFromProviderList()
		case "p":
			if m.activeProviderKey() == providerKeyLocal && m.localProvider != nil {
				m.openPlaylistManager()
			}
		case "up", "k":
			m.providerMoveUp()
		case "space":
			return m.togglePlayPause()
		case "down", "j":
			m.providerMoveDown()
			// Auto-load next catalog page when scrolling near the bottom.
			return m.maybeLoadCatalogBatch()
		case "enter":
			if m.provSignIn {
				if auth, ok := m.provider.(playlist.Authenticator); ok {
					m.provSignIn = false
					m.provLoading = true
					m.err = nil
					return authenticateProviderCmd(auth, m.provider.Name(), nextRequest(&m.requests.auth))
				}
			}
			if len(m.providerLists) > 0 && !m.provLoading {
				return m.openProviderList(m.provCursor)
			}
		case "tab", "shift+tab":
			// Leave the content-first provider layout before choosing a control;
			// the playback pane may be closed or too short to show every setting.
			m.focus = focusPlaylist
			m.recomputeLayout()
			if msg.String() == "shift+tab" {
				m.focus = m.previousMainFocus(focusPlaylist)
			} else {
				m.focus = m.nextMainFocus(focusPlaylist)
			}
		case "esc", "backspace", "b":
			// Clear completed results or cancel a search still in flight.
			if m.providerCatalogSearching() {
				return m.restoreCatalog(m.provider.(provider.CatalogSearcher))
			}
			if m.playlist.Len() > 0 {
				m.focus = focusPlaylist
			}
		case "/":
			m.provSearch.active = true
			m.provSearch.query = ""
			m.provSearch.results = nil
			m.provSearch.cursor = 0
			m.provSearch.scroll = 0
		case "ctrl+r":
			if m.provider != nil && !m.provLoading {
				if r, ok := m.provider.(playlist.Refresher); ok {
					r.Refresh()
				}
				nextRequest(&m.requests.catalog)
				m.catalogBatch = catalogBatchState{}
				m.provLoading = true
				m.status.Activityf(statusTTLShort, "Refreshing %s…", m.provider.Name())
				// Re-open the current playlist in place only when the
				// provider guarantees the ID stays valid across Refresh()
				// (e.g. the Yandex "Моя волна" session). Positional IDs
				// (radio catalog indexes) must fall back to the playlists
				// pane.
				if id := m.activeProviderPlaylistID; id != "" {
					if pr, ok := m.provider.(playlist.RefreshablePlaylist); ok && pr.CanRefreshPlaylist(id) {
						return m.fetchProviderTracks(id)
					}
				}
				m.activeProviderPlaylistID = ""
				return m.fetchProviderPlaylists()
			}
		case "f":
			return m.toggleProviderFavorite()
		case "o":
			m.openFileBrowser()
		case "N":
			// Provider-pane browsing must stay scoped to the provider being
			// viewed. Falling back to another registered browser can otherwise
			// send (for example) Spotify's pane into Mixcloud.
			if providerSupportsBrowse(m.provider) {
				m.openNavBrowserWith(m.provider)
			}
		case "pgup", "ctrl+u":
			m.providerPageUp()
		case "pgdown", "ctrl+d":
			m.providerPageDown()
			return m.maybeLoadCatalogBatch()
		case "g", "home":
			m.providerToTop()
		case "G", "end":
			m.providerToBottom()
			return m.maybeLoadCatalogBatch()
		case "ctrl+j":
			m.openJumpMode()
		case "J":
			return m.switchToProvider("jellyfin")
		case "E":
			return m.switchToProvider("emby")
		case "B":
			return m.switchToProvider("audiobookshelf")
		case "S":
			return m.switchToProvider("spotify")
		case "P":
			return m.switchToProvider("plex")
		case "Y":
			return m.switchToProvider("yt")
		case "C":
			return m.switchToProvider("soundcloud")
		case "X":
			return m.switchToProvider("mixcloud")
		case "M":
			return m.switchToProvider("netease")
		case "Q":
			return m.switchToProvider("qobuz")
		case "T":
			return m.switchToProvider("tidal")
		case "L":
			return m.switchToProvider("local")
		case "R":
			return m.switchToProvider("radio")
		case "O":
			return m.switchToProvider("podcast")
		case "ctrl+x":
			m.toggleExpandedView()
		case "ctrl+f":
			m.openProviderSearch()
		}
		return nil
	}

	if m.focus == focusSpeed {
		return m.handleSpeedKey(msg)
	}

	if m.focus == focusProvPill {
		switch msg.String() {
		case "q", "ctrl+c":
			return m.quit()
		case "left", "h":
			if m.provPillIdx > 0 {
				m.provPillIdx--
			}
		case "right", "l":
			if m.provPillIdx < len(m.providers)-1 {
				m.provPillIdx++
			}
		case "enter":
			return m.switchProvider(m.provPillIdx)
		case "tab":
			m.focus = m.nextMainFocus(focusProvPill)
		case "shift+tab", "esc", "backspace":
			m.focus = m.previousMainFocus(focusProvPill)
		case "space":
			return m.togglePlayPause()
		}
		return nil
	}

	// Vim-style count prefix: a digit primes a pending percentage; the next `j`
	// jumps there (e.g. `7j` → 70%). Any other key cancels and runs normally.
	if s := msg.String(); m.focus == focusPlaylist && len(s) == 1 && s[0] >= '0' && s[0] <= '9' {
		m.pendingSeekActive = true
		m.pendingSeekPct = int(s[0] - '0')
		m.pendingSeekExpiresAt = time.Now().Add(time.Duration(statusTTLMedium))
		m.status.Activityf(statusTTLMedium, "%dj -> seek to %d%%", m.pendingSeekPct, m.pendingSeekPct*10)
		return nil
	}
	if m.pendingSeekActive {
		pct := m.pendingSeekPct
		m.pendingSeekActive = false
		m.pendingSeekExpiresAt = time.Time{}
		m.status.Clear()
		if msg.String() == "j" && m.focus == focusPlaylist {
			if dur := m.player.Duration(); dur > 0 {
				return m.seekAbsolute(dur * time.Duration(pct) / 10)
			}
			return nil
		}
	}

	// Focused settings reuse the global actions below, including notifications,
	// config persistence, and gapless rearming.
	key := msg.String()
	repeatStep := playlist.RepeatMode(1)
	switch m.focus {
	case focusVolume:
		switch key {
		case "left", "h", "down", "j":
			key = "-"
		case "right", "l", "up", "k":
			key = "+"
		}
	case focusShuffle:
		switch key {
		case "left", "h", "down", "j", "right", "l", "up", "k", "enter":
			key = "z"
		}
	case focusRepeat:
		switch key {
		case "left", "h", "down", "j":
			repeatStep = -1
			key = "r"
		case "right", "l", "up", "k", "enter":
			key = "r"
		}
	}

	switch key {
	case "q", "ctrl+c":
		return m.quit()
	case "ctrl+r":
		// Refresh in the queue/playlist view: when a refreshable provider
		// playlist (e.g. the Yandex "Моя волна" session) is open, drop its
		// cached session and reload a fresh batch in place. Providers whose
		// IDs don't survive Refresh (positional catalog indexes) are skipped.
		if m.provider != nil && !m.provLoading && m.activeProviderPlaylistID != "" {
			id := m.activeProviderPlaylistID
			pr, capable := m.provider.(playlist.RefreshablePlaylist)
			if !capable || !pr.CanRefreshPlaylist(id) {
				// Keep plugin key bindings working: ctrl+r is no longer an
				// unhandled key here, so forward it explicitly.
				if m.luaMgr != nil {
					m.luaMgr.EmitKey(msg.String())
				}
				return nil
			}
			pr.Refresh()
			nextRequest(&m.requests.catalog)
			m.catalogBatch = catalogBatchState{}
			m.provLoading = true
			m.status.Activityf(statusTTLShort, "Refreshing %s…", m.provider.Name())
			return m.fetchProviderTracks(id)
		}
	case "esc", "backspace", "b":
		if m.focus == focusPlaylist {
			// Keep current expanded/collapsed height mode when switching focus.
			m.focus = focusProvider
		} else {
			m.focus = m.previousMainFocus(m.focus)
		}

	case "space":
		return m.togglePlayPause()

	case "s":
		m.stopByUser()
		return nil

	case ">", ".":
		return m.skipNext()

	case "<", ",":
		return m.skipPrev()

	case "left":
		if m.focus == focusEQ {
			if m.eqCursor > 0 {
				m.eqCursor--
			}
		} else {
			return m.doSeek(-5 * time.Second)
		}

	case "shift+left":
		return m.doSeek(-m.seekStepLarge)

	case "right":
		if m.focus == focusEQ {
			if m.eqCursor < eqBandCount-1 {
				m.eqCursor++
			}
		} else {
			return m.doSeek(5 * time.Second)
		}

	case "shift+right":
		return m.doSeek(m.seekStepLarge)

	case "f":
		return m.togglePlaylistStar()

	case "shift+up", "shift+down":
		if m.focus == focusPlaylist {
			to := m.plCursor + 1
			if key == "shift+up" {
				to = m.plCursor - 1
			}
			cmd, _ := m.moveTrack(m.plCursor, to)
			return cmd
		}

	case "up", "k":
		if m.focus == focusEQ {
			bands := m.player.EQBands()
			m.setCustomEQBand(m.eqCursor, bands[m.eqCursor]+1)
		} else {
			if row := m.plCursorRow(); row > 0 {
				m.setPlCursorRow(row - 1)
			} else if m.playlist.Len() > 0 {
				m.setPlCursorRow(m.playlist.Len() - 1)
			}
		}

	case "down", "j":
		if m.focus == focusEQ {
			bands := m.player.EQBands()
			m.setCustomEQBand(m.eqCursor, bands[m.eqCursor]-1)
		} else {
			if row := m.plCursorRow(); row < m.playlist.Len()-1 {
				m.setPlCursorRow(row + 1)
			} else if m.playlist.Len() > 0 {
				m.setPlCursorRow(0)
			}
		}

	case "pgup", "ctrl+u":
		if row := m.plCursorRow(); m.focus == focusPlaylist && row > 0 {
			visible := max(1, m.effectivePlaylistVisible())
			m.setPlCursorRow(row - min(row, visible))
		}

	case "pgdown", "ctrl+d":
		if row := m.plCursorRow(); m.focus == focusPlaylist && row < m.playlist.Len()-1 {
			visible := max(1, m.effectivePlaylistVisible())
			m.setPlCursorRow(min(m.playlist.Len()-1, row+visible))
		}

	case "g", "home":
		if m.focus == focusPlaylist && m.plCursorRow() != 0 {
			m.setPlCursorRow(0)
		}

	case "G", "end":
		if m.focus == focusPlaylist && m.playlist.Len() > 0 && m.plCursorRow() != m.playlist.Len()-1 {
			m.setPlCursorRow(m.playlist.Len() - 1)
		}

	case "enter":
		if m.focus == focusPlaylist {
			// No-op only if this exact track is still buffering.
			if m.buffering && m.plCursor == m.playlist.Index() {
				break
			}
			return m.playIndex(m.plCursor)
		}

	case "+", "=":
		m.adjustVolume(1)

	case "-":
		m.adjustVolume(-1)

	case "r":
		const repeatModes = playlist.RepeatOne + 1
		return m.setRepeat((m.playlist.Repeat() + repeatStep + repeatModes) % repeatModes)

	case "z":
		return m.setShuffle(!m.playlist.Shuffled())

	case "tab":
		m.focus = m.nextMainFocus(m.focus)
	case "shift+tab":
		m.focus = m.previousMainFocus(m.focus)

	case "h":
		if m.focus == focusEQ && m.eqCursor > 0 {
			m.eqCursor--
		}

	case "l":
		if m.focus == focusEQ && m.eqCursor < eqBandCount-1 {
			m.eqCursor++
		}

	case "e":
		if m.simplified || m.layout.tier == layoutMinimal {
			break
		}
		m.cycleEQPreset()
		m.scheduleEQSave()

	case "a":
		if m.focus == focusPlaylist {
			if !m.playlist.Dequeue(m.plCursor) {
				m.playlist.Queue(m.plCursor)
			}
			m.normalizeQueueOverlay()
			return m.rearmPreload()
		}

	case "w":
		if m.focus == focusPlaylist && m.plCursor >= 0 && m.plCursor < m.playlist.Len() {
			if track, ok := m.playlist.Track(m.plCursor); ok {
				m.openPlaylistPicker([]playlist.Track{track}, "Track: "+track.DisplayName())
			}
		}

	case "A":
		if m.focus == focusPlaylist {
			m.queue.visible = true
			m.queue.cursor = 0
			m.queue.scroll = 0
		}

	case "F":
		// Keep plugin key bindings working: when the overlay does not open,
		// F is no longer an unhandled key here, so forward it explicitly.
		if !m.openSubsOverlay() && m.luaMgr != nil {
			m.luaMgr.EmitKey(msg.String())
		}

	case "ctrl+s":
		return m.saveTrack()
	case "S":
		return m.switchToProvider("spotify")

	case "m":
		m.player.ToggleMono()

	case "/":
		m.search.active = true
		m.search.query = ""
		m.search.results = nil
		m.search.cursor = 0
		m.search.scroll = 0
		m.prevFocus = m.focus
		m.focus = focusSearch
		// Search now renders in the playlist region; recompute chrome so the
		// search header/help are reflected in the visible-row budget.
		m.refreshChrome()
		m.applyHeightMode()

	case "ctrl+f":
		m.openProviderSearch()

	case "ctrl+j":
		m.openJumpMode()
	case "J":
		return m.switchToProvider("jellyfin")
	case "E":
		return m.switchToProvider("emby")
	case "B":
		return m.switchToProvider("audiobookshelf")
	case "p":
		if m.localProvider != nil {
			m.openPlaylistManager()
		}

	case "t":
		m.openThemePicker()

	case "y":
		m.lyrics.visible = !m.lyrics.visible
		if m.lyrics.visible {
			return m.retryLyrics()
		}

	case "o":
		m.openFileBrowser()

	case "u":
		m.urlInputting = true
		m.urlInput = ""
		m.urlErr = ""

	case "N":
		if cmd, ok := m.openSelectedTrackArtistBrowser(); ok {
			return cmd
		}
		if providerSupportsBrowse(m.provider) {
			m.openNavBrowserWith(m.provider)
		}

	case "L":
		return m.switchToProvider("local")
	case "R":
		return m.switchToProvider("radio")
	case "O":
		return m.switchToProvider("podcast")
	case "P":
		return m.switchToProvider("plex")
	case "Y":
		return m.switchToProvider("yt")
	case "C":
		return m.switchToProvider("soundcloud")
	case "X":
		return m.switchToProvider("mixcloud")
	case "M":
		return m.switchToProvider("netease")
	case "Q":
		return m.switchToProvider("qobuz")
	case "T":
		return m.switchToProvider("tidal")

	case "ctrl+h":
		m.toggleAlbumHeadersManual()
		m.adjustScroll()

	case "ctrl+g":
		m.toggleHelpBar()
		m.adjustScroll()

	case "ctrl+b":
		m.toggleSettingsPane()
		m.adjustScroll()

	case "v":
		if m.simplified {
			break
		}
		_ = m.cycleVisualizer()

	case "ctrl+v":
		if m.simplified {
			break
		}
		m.openVisPicker()

	case "V":
		if m.simplified {
			break
		}
		m.fullVis = !m.fullVis
		m.recomputeLayout()

	case "ctrl+x":
		if !m.simplified && m.focus == focusPlaylist {
			m.toggleExpandedView()
		}

	case "x":
		if m.focus == focusPlaylist {
			var cmd tea.Cmd
			m.keepPlCursorRow(func() { cmd, _ = m.removeTrack(m.plCursor, true) })
			return cmd
		}

	case "d":
		m.devicePicker.visible = true
		m.devicePicker.cursor = 0
		m.devicePicker.scroll = 0
		if len(m.devicePicker.devices) == 0 {
			m.devicePicker.loading = true
			return listDevicesCmd()
		}

	case "]":
		m.changeSpeed(0.25)

	case "[":
		m.changeSpeed(-0.25)

	case "?":
		m.openKeymap()

	default:
		if m.luaMgr != nil {
			m.luaMgr.EmitKey(msg.String())
		}
	}

	return nil
}

// handleInfoKey processes key presses while the track info overlay is open.
func (m *Model) handleInfoKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c":
		return m.quit()
	case "esc", "i":
		m.showInfo = false
	case "ctrl+i":
		m.showInfo = false
		m.toggleMetadata()
	case "up", "k":
		if m.infoScroll > 0 {
			m.infoScroll--
		}
	case "down", "j":
		m.infoScroll++
		m.infoMaybeAdjustScroll()
	}
	return nil
}

// handleLyricsKey processes key presses while the lyrics overlay is open.
func (m *Model) handleLyricsKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c":
		return m.quit()
	case "esc", "y":
		nextRequest(&m.requests.lyrics)
		m.lyrics.loading = false
		m.lyrics.query = ""
		m.lyrics.visible = false
	case "r":
		return m.retryLyrics()
	case "[":
		if m.lyricsSyncable() && m.lyricsHaveTimestamps() {
			return m.nudgeLyricsOffset(-250 * time.Millisecond)
		}
	case "]":
		if m.lyricsSyncable() && m.lyricsHaveTimestamps() {
			return m.nudgeLyricsOffset(250 * time.Millisecond)
		}
	case "up", "k":
		if !(m.lyricsSyncable() && m.lyricsHaveTimestamps()) && m.lyrics.scroll > 0 {
			m.lyrics.scroll--
		}
	case "down", "j":
		if !(m.lyricsSyncable() && m.lyricsHaveTimestamps()) {
			maxScroll := max(len(m.lyrics.lines)-1, 0)
			if m.lyrics.scroll < maxScroll {
				m.lyrics.scroll++
			}
		}
	case "ctrl+x":
		m.toggleExpandedView()
	}
	return nil
}

func (m *Model) exitFullVisualizer() {
	m.fullVis = false
	m.recomputeLayout()
}

func (m *Model) handleFullVisualizerKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "q":
		return m.quit()
	case "esc", "backspace", "b", "V":
		m.exitFullVisualizer()
	case "space":
		return m.togglePlayPause()
	case ">", ".":
		return m.skipNext()
	case "<", ",":
		return m.skipPrev()
	case "left":
		return m.doSeek(-5 * time.Second)
	case "shift+left":
		return m.doSeek(-m.seekStepLarge)
	case "right":
		return m.doSeek(5 * time.Second)
	case "shift+right":
		return m.doSeek(m.seekStepLarge)
	case "+", "=":
		m.adjustVolume(1)
	case "-":
		m.adjustVolume(-1)
	case "v":
		_ = m.cycleVisualizer()
	case "t":
		// Hide the episode name so the full-screen visualizer can be put on a
		// shared screen without naming what is playing.
		m.hideTrackInfo = !m.hideTrackInfo
	case "ctrl+k", "?":
		m.exitFullVisualizer()
		m.openKeymap()

	default:
		// Plugin bindings are global: a pomodoro or sleep-timer key is about
		// the session, not about which screen happens to be open. Without
		// this, every plugin key is dead in the full-screen visualizer —
		// which is exactly where a plugin visualizer is being watched.
		if m.luaMgr != nil {
			m.luaMgr.EmitKey(msg.String())
		}
	}
	return nil
}

// saveTrack saves the current track in the configured downloads directory.
// tracksave.SaveTo runs in a tea.Cmd, so neither a yt-dlp download nor a file
// copy blocks the Update goroutine. IPC save uses the same routine.
func (m *Model) saveTrack() tea.Cmd {
	track, idx := m.currentPlaybackTrack()
	if idx < 0 {
		m.status.Warning("Nothing to save", statusTTLShort)
		return nil
	}
	// tracksave downloads these tracks with yt-dlp, which can take minutes.
	download := playlist.IsYouTubeURL(track.Path) || playlist.IsYTDL(track.Path)
	if download {
		m.status.Clear()
		m.save.startDownload()
	}
	directory := m.downloadsDirectory
	return func() tea.Msg {
		path, err := tracksave.SaveTo(track, directory)
		return trackSavedMsg{path: path, err: err, download: download}
	}
}

func (m *Model) resetJumpInput() {
	m.jumpInput = ""
	m.jumpErr = ""
}

func (m *Model) openJumpMode() {
	m.jumping = true
	m.resetJumpInput()
}

func (m *Model) closeJumpMode() {
	m.jumping = false
	m.resetJumpInput()
}

// handleJumpKey processes key presses while in jump-time mode.
func (m *Model) handleJumpKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c":
		m.closeJumpMode()
		return m.quit()
	}

	switch msg.Code {
	case tea.KeyEscape:
		m.closeJumpMode()
		return nil
	case tea.KeyEnter:
		target, err := parseJumpTarget(m.jumpInput)
		if err != nil {
			m.jumpErr = "Invalid jump: " + err.Error()
			m.status.Warning(m.jumpErr, statusTTLDefault)
			return nil
		}
		if !m.player.Seekable() {
			m.jumpErr = "This track cannot be seeked."
			m.status.Warning(m.jumpErr, statusTTLDefault)
			return nil
		}
		if dur := m.player.Duration(); dur > 0 && target > dur {
			m.jumpErr = fmt.Sprintf("Jump exceeds track duration (%s).", formatJumpClock(dur))
			m.status.Warning(m.jumpErr, statusTTLDefault)
			return nil
		}
		cmd, err := m.trySeekAbsolute(target)
		if err != nil {
			m.jumpErr = "Seek failed: " + err.Error()
			m.status.Warning(m.jumpErr, statusTTLDefault)
			return nil
		}
		m.closeJumpMode()
		return cmd
	}

	if m.editText("jump", &m.jumpInput, msg) {
		m.jumpErr = ""
	}
	return nil
}

// toggleExpandedView toggles the UI between default and expanded height.
func (m *Model) toggleExpandedView() {
	m.heightExpanded = !m.heightExpanded
	m.applyHeightMode()
	m.adjustScroll()
}

// handlePaste sends pasted text to the text field of the top overlay, as
// handleKey sends keys. With no overlay open, the provider filter takes it.
func (m *Model) handlePaste(content string) tea.Cmd {
	if content == "" {
		return nil
	}
	if spec, ok := m.topOverlay(); ok {
		if spec.paste != nil {
			spec.paste(m, content)
		}
		return nil
	}

	if m.provSearch.active {
		m.insertText("provider-search", &m.provSearch.query, content)
		if _, ok := m.provider.(provider.CatalogSearcher); !ok {
			m.updateProvSearch()
		}
		return nil
	}

	return nil
}

// handleURLInputKey processes key presses while in URL input mode.
func (m *Model) handleURLInputKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.Code {
	case tea.KeyEscape:
		m.urlInputting = false
	case tea.KeyEnter:
		m.urlInputting = false
		input := strings.TrimSpace(m.urlInput)
		if input != "" {
			m.feedLoading = true
			m.status.Activity("Loading URL...", statusTTLLong)
			return resolveURLCmd(input, true)
		}
		m.urlInputting = true
		m.urlErr = "Enter a stream, track, or playlist URL."
	default:
		if m.editText("url", &m.urlInput, msg) {
			m.urlErr = ""
		}
	}
	return nil
}
