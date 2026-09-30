package model

import tea "charm.land/bubbletea/v2"

// overlaySpec describes one overlay: the screen it shows, the handlers that
// own the keys and pasted text while it is on top, and the pieces it renders.
type overlaySpec struct {
	screen topLevelScreen
	key    func(*Model, tea.KeyPressMsg) tea.Cmd
	// paste takes pasted text. It is nil when the overlay has no text field.
	// The paste is then dropped.
	paste func(*Model, string)
	// view is zero for the full-screen visualizer, which replaces the whole
	// frame instead of the playlist region.
	view overlayView
}

// overlayStack lists the overlays from the top down. The first open overlay
// renders, gets the keys and gets pasted text, so these three always agree.
//
// An overlay that opens from inside another overlay sits above it. The
// playlist picker and the file browser open over the playlist manager, and
// the playlist picker also opens over the file browser. Provider search opens
// over the nav browser. The other overlays open only from the main keys, so
// they cannot stack. Their order only has to be the same on every route. The
// queue sits above the subscriptions overlay because the render order already
// put it there.
var overlayStack []overlaySpec

// init fills overlayStack. The key handlers reach overlayStack again through
// handleKey, so a package-level initializer would form a cycle.
func init() {
	overlayStack = []overlaySpec{
		{
			screen: screenFullVisualizer,
			key:    (*Model).handleFullVisualizerKey,
		},
		{
			screen: screenKeymap,
			key:    (*Model).handleKeymapKey,
			paste: func(m *Model, s string) {
				m.insertText("keymap", &m.keymap.search, s)
				m.updateKeymapFilter()
			},
			view: overlayView{(*Model).keymapHeaderLine, (*Model).keymapHelpLine, (*Model).renderKeymapList},
		},
		{
			screen: screenDevicePicker,
			key:    (*Model).handleDeviceKey,
			view:   overlayView{(*Model).deviceHeaderLine, (*Model).devicePickerHelpLine, (*Model).renderDeviceBody},
		},
		{
			screen: screenPlaylistPicker,
			key:    (*Model).handlePlaylistPickerKey,
			paste: func(m *Model, s string) {
				if m.plPicker.screen == plPickerNewName {
					m.insertText("playlist-picker-name", &m.plPicker.newName, s)
					m.plPicker.inputErr = ""
				}
			},
			view: overlayView{(*Model).plPickerHeaderLine, (*Model).plPickerHelpLine, (*Model).renderPlaylistPickerBody},
		},
		{
			screen: screenFileBrowser,
			key:    (*Model).handleFileBrowserKey,
			paste: func(m *Model, s string) {
				if m.fileBrowser.searching {
					m.insertText("file-browser-search", &m.fileBrowser.search, s)
					m.fbUpdateFilter()
				}
			},
			view: overlayView{(*Model).fbHeaderLine, (*Model).fbHelpLine, (*Model).renderFileBrowserBody},
		},
		{
			screen: screenSpotSearch,
			key:    (*Model).handleSpotSearchKey,
			paste: func(m *Model, s string) {
				switch m.spotSearch.screen {
				case spotSearchInput:
					m.insertText("spot-search", &m.spotSearch.query, s)
				case spotSearchNewName:
					m.insertText("spot-playlist-name", &m.spotSearch.newName, s)
				}
			},
			view: overlayView{(*Model).spotSearchHeaderLine, (*Model).spotSearchHelpLine, (*Model).renderSpotSearchBody},
		},
		{
			screen: screenNavBrowser,
			key:    (*Model).handleNavBrowserKey,
			paste: func(m *Model, s string) {
				if m.navBrowser.mode != navBrowseModeMenu && m.navBrowser.searching {
					m.insertText("nav-search", &m.navBrowser.search, s)
					m.navBrowser.cursor = 0
					m.navBrowser.scroll = 0
					m.navUpdateSearch()
				}
			},
			view: overlayView{(*Model).navHeaderLine, (*Model).navHelpLine, (*Model).renderNavBody},
		},
		{
			screen: screenThemePicker,
			key:    (*Model).handleThemeKey,
			paste: func(m *Model, s string) {
				if m.themePicker.filtering {
					m.insertText("theme-picker-filter", &m.themePicker.filter, s)
					m.themePickerRecomputeFilter()
				}
			},
			view: overlayView{(*Model).themePickerHeaderLine, (*Model).themePickerHelpLine, (*Model).renderThemeBody},
		},
		{
			screen: screenVisPicker,
			key:    (*Model).handleVisPickerKey,
			paste: func(m *Model, s string) {
				if m.visPicker.filtering {
					m.insertText("visualizer-picker-filter", &m.visPicker.filter, s)
					m.visPickerRecomputeFilter()
				}
			},
			view: overlayView{(*Model).visPickerHeaderLine, (*Model).visPickerHelpLine, (*Model).renderVisPickerList},
		},
		{
			screen: screenPlaylistManager,
			key:    (*Model).handlePlaylistManagerKey,
			paste: func(m *Model, s string) {
				switch {
				case m.plManager.screen == plMgrScreenNewName:
					m.insertText("playlist-manager-new-name", &m.plManager.newName, s)
					m.plManager.inputErr = ""
				case m.plManager.screen == plMgrScreenRename:
					m.insertText("playlist-manager-rename", &m.plManager.renameName, s)
					m.plManager.inputErr = ""
				case m.plManager.filtering:
					m.insertText("playlist-manager-filter", &m.plManager.filter, s)
					m.plManager.cursor = 0
					m.plMgrRecomputeFilter()
				}
			},
			view: overlayView{(*Model).plMgrHeaderLine, (*Model).plMgrHelpLine, (*Model).renderPlMgrBody},
		},
		{
			screen: screenQueue,
			key:    (*Model).handleQueueKey,
			view: overlayView{
				func(m *Model) string { return sepHeaderN("Queue", m.queue.cursor+1, m.playlist.QueueLen()) },
				(*Model).queueHelpLine, (*Model).renderQueueBody},
		},
		{
			screen: screenSubs,
			key:    (*Model).handleSubsKey,
			paste: func(m *Model, s string) {
				if m.subs.filtering {
					m.insertText("subs-filter", &m.subs.filter, s)
					m.updateSubsFilter()
				}
			},
			view: overlayView{(*Model).subsHeaderLine, (*Model).subsHelpLine, (*Model).renderSubsBody},
		},
		{
			screen: screenInfo,
			key:    (*Model).handleInfoKey,
			view: overlayView{
				func(*Model) string { return sepHeader("Track Info") },
				func(m *Model) string { return m.commandHelp(commandModeInfo) },
				(*Model).renderInfoBody},
		},
		{
			screen: screenLyrics,
			key:    (*Model).handleLyricsKey,
			view: overlayView{
				func(*Model) string { return sepHeader("Lyrics") },
				(*Model).lyricsHelpLine, (*Model).renderLyricsBody},
		},
		{
			screen: screenJump,
			key:    (*Model).handleJumpKey,
			paste: func(m *Model, s string) {
				m.insertText("jump", &m.jumpInput, s)
				m.jumpErr = ""
			},
			view: overlayView{
				func(*Model) string { return sepHeader("Jump to Time") },
				func(m *Model) string { return m.commandHelp(commandModeJump) },
				(*Model).renderJumpBody},
		},
		{
			screen: screenURLInput,
			key:    (*Model).handleURLInputKey,
			paste: func(m *Model, s string) {
				m.insertText("url", &m.urlInput, s)
				m.urlErr = ""
			},
			view: overlayView{
				func(m *Model) string { return m.promptHeader("url", "Load URL", m.urlInput) },
				func(m *Model) string { return m.commandHelp(commandModeURL) },
				(*Model).renderURLBody},
		},
		{
			screen: screenSearch,
			key:    (*Model).handleSearchKey,
			paste: func(m *Model, s string) {
				m.insertText("playlist-search", &m.search.query, s)
				m.updateSearch()
			},
			view: overlayView{(*Model).searchHeaderLine, (*Model).searchHelpLine, (*Model).renderSearchList},
		},
		{
			screen: screenNetSearch,
			key:    (*Model).handleNetSearchKey,
			paste: func(m *Model, s string) {
				if m.netSearch.screen == netSearchInput {
					m.insertText("net-search", &m.netSearch.query, s)
				}
			},
			view: overlayView{(*Model).netSearchHeaderLine, (*Model).netSearchHelpLine, (*Model).renderNetSearchBody},
		},
	}
}

// overlayOpen reports whether the overlay that shows screen is open. It is a
// method and not a func field of overlaySpec, so activeScreen can check the
// overlays on each frame without moving the Model to the heap.
func (m *Model) overlayOpen(screen topLevelScreen) bool {
	switch screen {
	case screenFullVisualizer:
		return m.fullVis
	case screenKeymap:
		return m.keymap.visible
	case screenDevicePicker:
		return m.devicePicker.visible
	case screenPlaylistPicker:
		return m.plPicker.visible
	case screenFileBrowser:
		return m.fileBrowser.visible
	case screenSpotSearch:
		return m.spotSearch.visible
	case screenNavBrowser:
		return m.navBrowser.visible
	case screenThemePicker:
		return m.themePicker.visible
	case screenVisPicker:
		return m.visPicker.visible
	case screenPlaylistManager:
		return m.plManager.visible
	case screenQueue:
		return m.queue.visible
	case screenSubs:
		return m.subs.visible
	case screenInfo:
		return m.showInfo
	case screenLyrics:
		return m.lyrics.visible
	case screenJump:
		return m.jumping
	case screenURLInput:
		return m.urlInputting
	case screenSearch:
		return m.search.active
	case screenNetSearch:
		return m.netSearch.active
	}
	return false
}

// topOverlay returns the open overlay nearest the top of overlayStack.
func (m *Model) topOverlay() (overlaySpec, bool) {
	for _, spec := range overlayStack {
		if m.overlayOpen(spec.screen) {
			return spec, true
		}
	}
	return overlaySpec{}, false
}
