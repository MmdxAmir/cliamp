package config

import "testing"

func TestParseBool(t *testing.T) {
	tests := []struct {
		in     string
		want   bool
		wantOK bool
	}{
		{"true", true, true},
		{"True", true, true},
		{"TRUE", true, true},
		{"tRUE", true, true},
		{"1", true, true},
		{"false", false, true},
		{"False", false, true},
		{"FALSE", false, true},
		{"0", false, true},
		{"", false, false},
		{"yes", false, false},
		{`"true"`, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, ok := parseBool(tt.in)
			if got != tt.want || ok != tt.wantOK {
				t.Fatalf("parseBool(%q) = %v, %v, want %v, %v", tt.in, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

// TestLoadBoolKeysIgnoreLetterCase checks that every bool key reads its value
// the same way. Before parseBool, shuffle = True read as false while
// expanded = True read as true.
func TestLoadBoolKeysIgnoreLetterCase(t *testing.T) {
	tests := []struct {
		name string
		data string
		got  func(Config) bool
		want bool
	}{
		{"shuffle", "shuffle = True", func(c Config) bool { return c.Shuffle }, true},
		{"mono", "mono = TRUE", func(c Config) bool { return c.Mono }, true},
		{"auto_play", "auto_play = True", func(c Config) bool { return c.AutoPlay }, true},
		{"simplified", "simplified = True", func(c Config) bool { return c.Simplified }, true},
		{"hide_help_bar", "hide_help_bar = True", func(c Config) bool { return c.HideHelpBar }, true},
		{"hide_settings_pane", "hide_settings_pane = True", func(c Config) bool { return c.HideSettingsPane }, true},
		{"show_metadata", "show_metadata = True", func(c Config) bool { return c.ShowMetadata }, true},
		{"expanded", "expanded = True", func(c Config) bool { return c.Expanded }, true},
		{"low_power", "low_power = True", func(c Config) bool { return c.LowPower }, true},
		{"vis_volume_linked", "vis_volume_linked = False", func(c Config) bool { return c.VisVolumeLinked }, false},
		{"invalid keeps default", "vis_volume_linked = maybe", func(c Config) bool { return c.VisVolumeLinked }, true},
		{"navidrome scrobble", "[navidrome]\nscrobble = False", func(c Config) bool { return c.Navidrome.ScrobbleDisabled }, true},
		{"navidrome scrobble zero", "[navidrome]\nscrobble = 0", func(c Config) bool { return c.Navidrome.ScrobbleDisabled }, true},
		{"lyrion show_unplayable", "[lyrion]\nshow_unplayable = True", func(c Config) bool { return c.Lyrion.ShowUnplayable }, true},
		{"spotify enabled", "[spotify]\nenabled = False", func(c Config) bool { return c.Spotify.IsSet() }, false},
		{"qobuz enabled", "[qobuz]\nenabled = FALSE", func(c Config) bool { return c.Qobuz.IsSet() }, false},
		{"tidal enabled", "[tidal]\nenabled = False", func(c Config) bool { return c.Tidal.IsSet() }, false},
		{"ytmusic enabled", "[ytmusic]\nenabled = False", func(c Config) bool { return c.YouTubeMusic.Disabled }, true},
		{"ytmusic expand_playlist", "[ytmusic]\nexpand_playlist = False", func(c Config) bool {
			return c.YouTubeMusic.ExpandPlaylist != nil && !*c.YouTubeMusic.ExpandPlaylist
		}, true},
		{"ytmusic expand_playlist invalid", "[ytmusic]\nexpand_playlist = maybe", func(c Config) bool {
			return c.YouTubeMusic.ExpandPlaylist == nil
		}, true},
		{"soundcloud enabled", "[soundcloud]\nenabled = True", func(c Config) bool { return c.SoundCloud.Enabled }, true},
		{"mixcloud enabled", "[mixcloud]\nenabled = True", func(c Config) bool { return c.Mixcloud.Enabled }, true},
		{"netease enabled", "[netease]\nenabled = True", func(c Config) bool { return c.NetEase.Enabled }, true},
		{"yandex enabled", "[yandex]\nenabled = True", func(c Config) bool { return c.Yandex.Enabled }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := loadConfigText(t, tt.data+"\n")
			if got := tt.got(cfg); got != tt.want {
				t.Fatalf("%q: got %v, want %v", tt.data, got, tt.want)
			}
		})
	}
}
