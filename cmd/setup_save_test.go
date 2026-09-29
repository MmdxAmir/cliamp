package cmd

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/bjarneo/cliamp/config"
)

// TestSetupRewriteKeepsUserLines runs setup over an existing config file and
// compares the whole file. Setup edits only the keys that it owns.
func TestSetupRewriteKeepsUserLines(t *testing.T) {
	tests := []struct {
		name    string
		section string
		values  map[string]string
		initial string // "" means no config file
		want    string
	}{
		{
			name:    "navidrome keeps browse_sort, format and comments",
			section: "navidrome",
			values:  map[string]string{"url": "https://new", "user": "bob", "password": "pw2"},
			initial: "volume = -6\n\n# Home server\n[navidrome]\nurl = \"https://old\"\nuser = \"alice\"\npassword = \"pw\"\n# byYear is the default\nbrowse_sort = \"alphabeticalByName\"\nformat = \"mp3\"\n\n[plex]\nurl = \"http://plex\"\n",
			want:    "volume = -6\n\n# Home server\n[navidrome]\nurl = \"https://new\"\nuser = \"bob\"\npassword = \"pw2\"\n# byYear is the default\nbrowse_sort = \"alphabeticalByName\"\nformat = \"mp3\"\n\n[plex]\nurl = \"http://plex\"\n",
		},
		{
			name:    "youtube section is edited in place",
			section: "ytmusic",
			values:  map[string]string{keyYTMusicMode: "cookies", "cookies_from": "firefox"},
			initial: "[youtube]\n# signed in on the laptop\ncookies_from = \"chrome\"\nexpand_playlist = true\n",
			want:    "[youtube]\n# signed in on the laptop\ncookies_from = \"firefox\"\nexpand_playlist = true\nenabled = true\n",
		},
		{
			name:    "yt section turned off drops the credentials",
			section: "ytmusic",
			values:  map[string]string{keyYTMusicMode: "off"},
			initial: "[yt]\nenabled = true\nclient_id = \"id\"\nclient_secret = \"s\"\nexpand_playlist = false\n",
			want:    "[yt]\nenabled = false\nexpand_playlist = false\n",
		},
		{
			name:    "jellyfin moves from token to password",
			section: "jellyfin",
			values:  map[string]string{keyJellyfinAuth: "password", "url": "https://jf", "user": "bob", "password": "pw"},
			initial: "[jellyfin]\nurl = \"https://jf\"\ntoken = \"t\"\nuser_id = \"u1\"\n",
			want:    "[jellyfin]\nurl = \"https://jf\"\nuser = \"bob\"\npassword = \"pw\"\n",
		},
		{
			name:    "spotify setup turns the provider on",
			section: "spotify",
			values:  map[string]string{keySpotifyMode: "default", "bitrate": "320"},
			initial: "[spotify]\nenabled = false\nclient_id = \"old\"\nbitrate = 160\n",
			want:    "[spotify]\nbitrate = 320\n",
		},
		{
			name:    "no config file",
			section: "qobuz",
			values:  map[string]string{keyQobuzQuality: "27"},
			want:    "[qobuz]\nenabled = true\nquality = 27\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("CLIAMP_CONFIG_DIR", dir)
			path := filepath.Join(dir, "config.toml")
			if tt.initial != "" {
				if err := os.WriteFile(path, []byte(tt.initial), 0o600); err != nil {
					t.Fatalf("WriteFile: %v", err)
				}
			}
			saveSetup(t, tt.section, tt.values)
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("ReadFile: %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("config =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

// TestSetupOwnsEveryBodyKey checks that owned lists each key that a body
// writes in any picker mode. A key that one mode writes and another mode
// leaves out must be owned, or its old value stays after a mode change.
func TestSetupOwnsEveryBodyKey(t *testing.T) {
	for _, spec := range providers() {
		t.Run(spec.section, func(t *testing.T) {
			values := map[string]string{"user_id": "u"}
			for _, f := range spec.fields {
				values[f.key] = "x"
			}
			modes := []string{""}
			if spec.picker != nil {
				modes = modes[:0]
				for _, o := range spec.picker.options {
					modes = append(modes, o.value)
				}
			}
			for _, mode := range modes {
				if spec.picker != nil {
					values[spec.picker.key] = mode
				}
				for _, e := range spec.body(values) {
					if !slices.Contains(spec.owned, e.Key) {
						t.Errorf("mode %q writes %s, but owned does not list it", mode, e.Key)
					}
				}
			}
		})
	}
}

// saveSetup saves values for section the way the wizard does after a
// successful probe.
func saveSetup(t *testing.T, section string, values map[string]string) {
	t.Helper()
	m := newSetupModel()
	m.pidx = -1
	for i, p := range m.provs {
		if p.section == section {
			m.pidx = i
			break
		}
	}
	if m.pidx < 0 {
		t.Fatalf("no provider spec for [%s]", section)
	}
	m.values = values
	m.persistAndDone(false)
	if m.saveFailed != nil {
		t.Fatalf("save [%s]: %v", section, m.saveFailed)
	}
}

// bodyValues returns a setup body as a map from key to TOML value.
func bodyValues(body []config.KeyValue) map[string]string {
	values := make(map[string]string, len(body))
	for _, e := range body {
		values[e.Key] = e.Value
	}
	return values
}

// checkBody reports each key of want that body lacks or sets to another value.
func checkBody(t *testing.T, body []config.KeyValue, want map[string]string) {
	t.Helper()
	got := bodyValues(body)
	for key, value := range want {
		if v, ok := got[key]; !ok || v != value {
			t.Errorf("body %s = %q, want %q\nbody: %q", key, v, value, body)
		}
	}
}
