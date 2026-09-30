package model

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

func TestHandlePasteRoutesToActiveInput(t *testing.T) {
	tests := []struct {
		name    string
		model   Model
		content string
		check   func(t *testing.T, m *Model)
	}{
		{
			name:    "keymap search",
			model:   Model{keymap: keymapOverlay{visible: true}},
			content: "ctrl",
			check: func(t *testing.T, m *Model) {
				if m.keymap.search != "ctrl" {
					t.Fatalf("keymap.search = %q, want %q", m.keymap.search, "ctrl")
				}
			},
		},
		{
			name:    "net search",
			model:   Model{netSearch: netSearchState{active: true, query: "hello "}},
			content: "world",
			check: func(t *testing.T, m *Model) {
				if m.netSearch.query != "hello world" {
					t.Fatalf("netSearch.query = %q, want %q", m.netSearch.query, "hello world")
				}
			},
		},
		{
			name:    "search appends and filters",
			model:   Model{search: searchState{active: true, query: "ja"}, playlist: playlist.New()},
			content: "zz",
			check: func(t *testing.T, m *Model) {
				if m.search.query != "jazz" {
					t.Fatalf("search.query = %q, want %q", m.search.query, "jazz")
				}
			},
		},
		{
			name:    "jump input",
			model:   Model{jumping: true, jumpInput: "1:"},
			content: "30",
			check: func(t *testing.T, m *Model) {
				if m.jumpInput != "1:30" {
					t.Fatalf("jumpInput = %q, want %q", m.jumpInput, "1:30")
				}
			},
		},
		{
			name:    "url input",
			model:   Model{urlInputting: true},
			content: "https://example.com/song.mp3",
			check: func(t *testing.T, m *Model) {
				if m.urlInput != "https://example.com/song.mp3" {
					t.Fatalf("urlInput = %q, want %q", m.urlInput, "https://example.com/song.mp3")
				}
			},
		},
		{
			name: "playlist manager new name",
			model: Model{plManager: plManagerState{
				visible: true,
				screen:  plMgrScreenNewName,
			}},
			content: "My Playlist",
			check: func(t *testing.T, m *Model) {
				if m.plManager.newName != "My Playlist" {
					t.Fatalf("plManager.newName = %q, want %q", m.plManager.newName, "My Playlist")
				}
			},
		},
		{
			name: "spotify search input",
			model: Model{spotSearch: spotSearchState{
				visible: true,
				screen:  spotSearchInput,
			}},
			content: "arctic monkeys",
			check: func(t *testing.T, m *Model) {
				if m.spotSearch.query != "arctic monkeys" {
					t.Fatalf("spotSearch.query = %q, want %q", m.spotSearch.query, "arctic monkeys")
				}
			},
		},
		{
			name: "spotify new name",
			model: Model{spotSearch: spotSearchState{
				visible: true,
				screen:  spotSearchNewName,
			}},
			content: "New Playlist",
			check: func(t *testing.T, m *Model) {
				if m.spotSearch.newName != "New Playlist" {
					t.Fatalf("spotSearch.newName = %q, want %q", m.spotSearch.newName, "New Playlist")
				}
			},
		},
		{
			name:    "provider search (non-catalog)",
			model:   Model{provSearch: provSearchState{active: true, query: "rock"}},
			content: " ballads",
			check: func(t *testing.T, m *Model) {
				if m.provSearch.query != "rock ballads" {
					t.Fatalf("provSearch.query = %q, want %q", m.provSearch.query, "rock ballads")
				}
			},
		},
		{
			name: "subscriptions filter",
			model: Model{subs: subsOverlay{
				visible:   true,
				filtering: true,
				filter:    "dead ",
				shows:     []provider.SubscriptionInfo{{Name: "Dead Drop"}, {Name: "Part Of The Problem"}},
			}},
			content: "drop",
			check: func(t *testing.T, m *Model) {
				if m.subs.filter != "dead drop" {
					t.Fatalf("subs.filter = %q, want %q", m.subs.filter, "dead drop")
				}
				if len(m.subs.filtered) != 1 || m.subs.filtered[0] != 0 {
					t.Fatalf("subs.filtered = %v, want [0]", m.subs.filtered)
				}
			},
		},
		{
			name:    "theme picker filter",
			model:   Model{themePicker: themePickerState{visible: true, filtering: true}},
			content: "dark",
			check: func(t *testing.T, m *Model) {
				if m.themePicker.filter != "dark" {
					t.Fatalf("themePicker.filter = %q, want %q", m.themePicker.filter, "dark")
				}
			},
		},
		{
			name:    "theme picker without filter drops the paste",
			model:   Model{themePicker: themePickerState{visible: true}, search: searchState{active: true}},
			content: "dark",
			check: func(t *testing.T, m *Model) {
				if m.themePicker.filter != "" || m.search.query != "" {
					t.Fatalf("filter = %q, search = %q, want both empty", m.themePicker.filter, m.search.query)
				}
			},
		},
		{
			name:    "visualizer picker filter",
			model:   Model{visPicker: visPickerState{visible: true, filtering: true}},
			content: "bars",
			check: func(t *testing.T, m *Model) {
				if m.visPicker.filter != "bars" {
					t.Fatalf("visPicker.filter = %q, want %q", m.visPicker.filter, "bars")
				}
			},
		},
		{
			name:    "playlist picker name",
			model:   Model{plPicker: playlistPickerState{visible: true, screen: plPickerNewName, inputErr: "empty"}},
			content: "Mix",
			check: func(t *testing.T, m *Model) {
				if m.plPicker.newName != "Mix" || m.plPicker.inputErr != "" {
					t.Fatalf("plPicker newName = %q, inputErr = %q, want %q and empty", m.plPicker.newName, m.plPicker.inputErr, "Mix")
				}
			},
		},
		{
			name:    "file browser search",
			model:   Model{fileBrowser: fileBrowserState{visible: true, searching: true}},
			content: "flac",
			check: func(t *testing.T, m *Model) {
				if m.fileBrowser.search != "flac" {
					t.Fatalf("fileBrowser.search = %q, want %q", m.fileBrowser.search, "flac")
				}
			},
		},
		{
			name:    "playlist manager rename",
			model:   Model{plManager: plManagerState{visible: true, screen: plMgrScreenRename}},
			content: "Road",
			check: func(t *testing.T, m *Model) {
				if m.plManager.renameName != "Road" {
					t.Fatalf("plManager.renameName = %q, want %q", m.plManager.renameName, "Road")
				}
			},
		},
		{
			name:    "playlist manager filter",
			model:   Model{plManager: plManagerState{visible: true, filtering: true}},
			content: "jazz",
			check: func(t *testing.T, m *Model) {
				if m.plManager.filter != "jazz" {
					t.Fatalf("plManager.filter = %q, want %q", m.plManager.filter, "jazz")
				}
			},
		},
		{
			name: "nav browser search",
			model: Model{navBrowser: navBrowserState{
				visible:   true,
				mode:      navBrowseModeByAlbum,
				searching: true,
			}},
			content: "album",
			check: func(t *testing.T, m *Model) {
				if m.navBrowser.search != "album" {
					t.Fatalf("navBrowser.search = %q, want %q", m.navBrowser.search, "album")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := tt.model
			if cmd := m.handlePaste(tt.content); cmd != nil {
				t.Fatalf("handlePaste returned non-nil cmd")
			}
			tt.check(t, &m)
		})
	}
}

func TestHandlePasteEmptyContentIsNoop(t *testing.T) {
	m := Model{netSearch: netSearchState{active: true, query: "before"}}

	if cmd := m.handlePaste(""); cmd != nil {
		t.Fatalf("handlePaste(\"\") returned non-nil cmd")
	}
	if m.netSearch.query != "before" {
		t.Fatalf("query changed on empty paste: got %q", m.netSearch.query)
	}
}

func TestHandlePasteNoInputActiveIsNoop(t *testing.T) {
	m := Model{focus: focusPlaylist}

	if cmd := m.handlePaste("ignored text"); cmd != nil {
		t.Fatalf("handlePaste returned non-nil cmd when no input active")
	}
}

func TestHandlePastePriorityOrder(t *testing.T) {
	// When multiple input states are active, the top overlay wins. The
	// YouTube search opens over the nav browser, so it gets the paste.
	m := Model{
		navBrowser: navBrowserState{
			visible:   true,
			mode:      navBrowseModeByAlbum,
			searching: true,
		},
		netSearch: netSearchState{active: true},
	}

	m.handlePaste("test")

	if m.netSearch.query != "test" {
		t.Fatalf("netSearch.query = %q, want %q", m.netSearch.query, "test")
	}
	if m.navBrowser.search != "" {
		t.Fatalf("navBrowser.search = %q, want empty (lower overlay)", m.navBrowser.search)
	}
}

func TestUpdateRoutesPasteMsg(t *testing.T) {
	m := Model{netSearch: netSearchState{active: true}}

	next, cmd := m.Update(tea.PasteMsg{Content: "pasted"})
	got := next.(Model)

	if cmd != nil {
		t.Fatalf("Update(PasteMsg) cmd = %v, want nil", cmd)
	}
	if got.netSearch.query != "pasted" {
		t.Fatalf("netSearch.query = %q, want %q", got.netSearch.query, "pasted")
	}
}
