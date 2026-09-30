package model

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// shortcutTestModel registers a provider for every Shift+letter shortcut,
// plus navidrome, and starts on a provider that no shortcut names.
func shortcutTestModel() Model {
	m := keybindingTestModel()
	other := commandsTestProvider{name: "Other"}
	m.provider = other
	m.providers = []provider.Entry{{Key: "other", Name: "Other", Provider: other}}
	for _, key := range []string{"navidrome", "spotify", "plex", "jellyfin", "emby", "audiobookshelf",
		"yt", "soundcloud", "mixcloud", "netease", "qobuz", "tidal", "local", "radio", "podcast"} {
		m.providers = append(m.providers, provider.Entry{Key: key, Name: key, Provider: commandsTestProvider{name: key}})
	}
	return m
}

func TestProviderShortcutsSwitchFromEveryFocus(t *testing.T) {
	focuses := []struct {
		name  string
		focus focusArea
	}{
		{"playlist", focusPlaylist},
		{"provider", focusProvider},
		{"equalizer", focusEQ},
		{"volume", focusVolume},
	}
	shortcuts := map[string]string{
		"S": "spotify", "P": "plex", "J": "jellyfin", "E": "emby", "B": "audiobookshelf",
		"Y": "yt", "C": "soundcloud", "X": "mixcloud", "M": "netease", "Q": "qobuz",
		"T": "tidal", "L": "local", "R": "radio", "O": "podcast",
	}
	for _, f := range focuses {
		for key, want := range shortcuts {
			t.Run(f.name+"/"+key, func(t *testing.T) {
				m := shortcutTestModel()
				m.focus = f.focus

				if cmd := m.handleKey(tea.KeyPressMsg{Text: key}); cmd == nil {
					t.Fatal("shortcut returned no command")
				}
				if got := m.provider.Name(); got != want {
					t.Fatalf("provider = %q, want %q", got, want)
				}
				if m.focus != focusProvider {
					t.Fatalf("focus = %v, want the provider pane", m.focus)
				}
			})
		}
		// N browses the provider on screen. It never switches to Navidrome.
		t.Run(f.name+"/N", func(t *testing.T) {
			m := shortcutTestModel()
			m.focus = f.focus

			m.handleKey(tea.KeyPressMsg{Text: "N"})

			if got := m.provider.Name(); got != "Other" {
				t.Fatalf("N switched the provider to %q", got)
			}
		})
	}
}

// refreshTestProvider counts Refresh calls. Only the playlist IDs in stable
// stay valid across Refresh.
type refreshTestProvider struct {
	commandsTestProvider
	refreshes *int
	stable    map[string]bool
}

func (p refreshTestProvider) Refresh() { *p.refreshes++ }

func (p refreshTestProvider) CanRefreshPlaylist(id string) bool { return p.stable[id] }

var _ playlist.RefreshablePlaylist = refreshTestProvider{}

// refresherOnlyProvider can drop its cache, but no playlist ID stays valid.
type refresherOnlyProvider struct {
	commandsTestProvider
	refreshes *int
}

func (p refresherOnlyProvider) Refresh() { *p.refreshes++ }

func TestCtrlRRefreshesTheActiveProvider(t *testing.T) {
	const (
		none   = iota // no reload starts
		tracks        // the open playlist reloads in place
		lists         // the playlist list reloads
	)
	tests := []struct {
		name       string
		focus      focusArea
		kind       string // "stable", "refresher" or "plain"
		playlistID string
		loading    bool
		want       int
		// wantRefreshes counts Refresh calls on the provider.
		wantRefreshes int
		// wantID is activeProviderPlaylistID after the key.
		wantID string
	}{
		{name: "pane reopens a stable playlist", focus: focusProvider, kind: "stable", playlistID: "wave", want: tracks, wantRefreshes: 1, wantID: "wave"},
		{name: "pane reloads the lists for a positional ID", focus: focusProvider, kind: "stable", playlistID: "station-3", want: lists, wantRefreshes: 1},
		{name: "pane reloads the lists with no open playlist", focus: focusProvider, kind: "stable", want: lists, wantRefreshes: 1},
		{name: "pane refreshes a provider with no stable IDs", focus: focusProvider, kind: "refresher", playlistID: "wave", want: lists, wantRefreshes: 1},
		{name: "pane reloads a provider with no cache", focus: focusProvider, kind: "plain", playlistID: "wave", want: lists},
		{name: "pane waits while the provider loads", focus: focusProvider, kind: "stable", playlistID: "wave", loading: true, want: none, wantID: "wave"},
		{name: "playlist reopens a stable playlist", focus: focusPlaylist, kind: "stable", playlistID: "wave", want: tracks, wantRefreshes: 1, wantID: "wave"},
		{name: "playlist skips a positional ID", focus: focusPlaylist, kind: "stable", playlistID: "station-3", want: none, wantID: "station-3"},
		{name: "playlist skips with no open playlist", focus: focusPlaylist, kind: "stable", want: none},
		{name: "playlist skips a provider with no stable IDs", focus: focusPlaylist, kind: "refresher", playlistID: "wave", want: none, wantID: "wave"},
		{name: "playlist waits while the provider loads", focus: focusPlaylist, kind: "stable", playlistID: "wave", loading: true, want: none, wantID: "wave"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			refreshes := 0
			base := commandsTestProvider{name: "Wave"}
			var prov playlist.Provider = base
			switch tt.kind {
			case "stable":
				prov = refreshTestProvider{commandsTestProvider: base, refreshes: &refreshes, stable: map[string]bool{"wave": true}}
			case "refresher":
				prov = refresherOnlyProvider{commandsTestProvider: base, refreshes: &refreshes}
			}
			m := keybindingTestModel()
			m.provider = prov
			m.playlist.Add(playlist.Track{Title: "Song"})
			m.focus = tt.focus
			m.activeProviderPlaylistID = tt.playlistID
			m.provLoading = tt.loading
			m.catalogBatch = catalogBatchState{offset: 100, done: true}

			cmd := m.handleKey(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})

			if refreshes != tt.wantRefreshes {
				t.Errorf("Refresh calls = %d, want %d", refreshes, tt.wantRefreshes)
			}
			if m.activeProviderPlaylistID != tt.wantID {
				t.Errorf("activeProviderPlaylistID = %q, want %q", m.activeProviderPlaylistID, tt.wantID)
			}
			if tt.want == none {
				if cmd != nil {
					t.Fatal("ctrl+r started a reload")
				}
				if m.catalogBatch == (catalogBatchState{}) {
					t.Error("ctrl+r reset the catalog pages without a reload")
				}
				return
			}
			if cmd == nil {
				t.Fatal("ctrl+r started no reload")
			}
			if !m.provLoading || m.catalogBatch != (catalogBatchState{}) {
				t.Errorf("provLoading = %v, catalogBatch = %+v, want a fresh load", m.provLoading, m.catalogBatch)
			}
			switch msg := cmd().(type) {
			case tracksLoadedMsg:
				if tt.want != tracks || msg.playlistID != "wave" {
					t.Fatalf("ctrl+r reloaded playlist %q, want kind %d", msg.playlistID, tt.want)
				}
			case playlistsLoadedMsg:
				if tt.want != lists {
					t.Fatal("ctrl+r reloaded the playlist list, want the open playlist")
				}
			default:
				t.Fatalf("ctrl+r command sent %T", msg)
			}
		})
	}
}
