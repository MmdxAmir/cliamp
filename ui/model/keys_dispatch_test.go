package model

import (
	"testing"

	tea "charm.land/bubbletea/v2"

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
