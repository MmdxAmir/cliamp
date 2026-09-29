package cmd

import (
	"slices"
	"testing"

	"github.com/bjarneo/cliamp/config"
)

// TestSetupBodyRoundTrip saves each provider body with values that need
// escapes and checks that config.Load reads the same values back.
func TestSetupBodyRoundTrip(t *testing.T) {
	const (
		backslash = `a\b`
		quote     = `p"w`
		single    = `'x'`
		mixed     = `p"w\#1 x`
		nbsp      = "pass\u00a0word"
	)
	tests := []struct {
		section string
		values  map[string]string
		got     func(config.Config) []string
		want    []string
	}{
		{
			section: "navidrome",
			values:  map[string]string{"url": "https://h/" + backslash, "user": single, "password": mixed},
			got: func(c config.Config) []string {
				return []string{c.Navidrome.URL, c.Navidrome.User, c.Navidrome.Password}
			},
			want: []string{"https://h/" + backslash, single, mixed},
		},
		{
			section: "lyrion",
			values:  map[string]string{"url": "http://nas:9000", "user": backslash + `\`, "password": nbsp},
			got: func(c config.Config) []string {
				return []string{c.Lyrion.URL, c.Lyrion.User, c.Lyrion.Password}
			},
			want: []string{"http://nas:9000", backslash + `\`, nbsp},
		},
		{
			section: "plex",
			values:  map[string]string{"url": "http://plex:32400", "token": mixed, "libraries": `Mus"ic, Ja\zz`},
			got: func(c config.Config) []string {
				return append([]string{c.Plex.URL, c.Plex.Token}, c.Plex.Libraries...)
			},
			want: []string{"http://plex:32400", mixed, `Mus"ic`, `Ja\zz`},
		},
		{
			section: "jellyfin",
			values:  map[string]string{keyJellyfinAuth: "password", "url": "https://jf", "user": single, "password": mixed},
			got: func(c config.Config) []string {
				return []string{c.Jellyfin.URL, c.Jellyfin.User, c.Jellyfin.Password}
			},
			want: []string{"https://jf", single, mixed},
		},
		{
			section: "emby",
			values:  map[string]string{keyEmbyAuth: "token", "url": "https://emby", "token": mixed, "user": quote},
			got: func(c config.Config) []string {
				return []string{c.Emby.URL, c.Emby.Token, c.Emby.User}
			},
			want: []string{"https://emby", mixed, quote},
		},
		{
			section: "audiobookshelf",
			values:  map[string]string{keyABSAuth: "password", "url": "https://abs", "user": backslash, "password": quote},
			got: func(c config.Config) []string {
				return []string{c.Audiobookshelf.URL, c.Audiobookshelf.User, c.Audiobookshelf.Password}
			},
			want: []string{"https://abs", backslash, quote},
		},
		{
			section: "spotify",
			values:  map[string]string{keySpotifyMode: "custom", "client_id": mixed, "bitrate": "160"},
			got: func(c config.Config) []string {
				return []string{c.Spotify.ClientID}
			},
			want: []string{mixed},
		},
		{
			section: "netease",
			values:  map[string]string{keyNetEaseBrowser: "custom", "cookies_from": `chrome:Profile "1"`, "user_id": backslash},
			got: func(c config.Config) []string {
				return []string{c.NetEase.CookiesFrom, c.NetEase.UserID}
			},
			want: []string{`chrome:Profile "1"`, backslash},
		},
		{
			section: "mixcloud",
			values: map[string]string{
				keyMixcloudBrowser: "custom",
				"username":         single,
				"access_token":     mixed,
				"cookies_from":     `firefox:` + backslash,
				"styles":           `deep"house, jazz\`,
			},
			got: func(c config.Config) []string {
				return append([]string{c.Mixcloud.Username, c.Mixcloud.AccessToken, c.Mixcloud.CookiesFrom}, c.Mixcloud.Styles...)
			},
			want: []string{single, mixed, `firefox:` + backslash, `deep"house`, `jazz\`},
		},
		{
			section: "ytmusic",
			values:  map[string]string{keyYTMusicMode: "custom", "client_id": quote, "client_secret": mixed, "cookies_from": backslash},
			got: func(c config.Config) []string {
				return []string{c.YouTubeMusic.ClientID, c.YouTubeMusic.ClientSecret, c.YouTubeMusic.CookiesFrom}
			},
			want: []string{quote, mixed, backslash},
		},
	}

	specs := map[string]providerSpec{}
	for _, p := range providers() {
		specs[p.section] = p
	}
	for _, tt := range tests {
		t.Run(tt.section, func(t *testing.T) {
			t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
			spec, ok := specs[tt.section]
			if !ok {
				t.Fatalf("no provider spec for [%s]", tt.section)
			}
			body := spec.body(tt.values)
			if err := saveSection(spec.section, body); err != nil {
				t.Fatalf("saveSection: %v", err)
			}
			cfg, err := config.Load()
			if err != nil {
				t.Fatalf("config.Load: %v", err)
			}
			if got := tt.got(cfg); !slices.Equal(got, tt.want) {
				t.Fatalf("round trip of\n%s\ngot  %q\nwant %q", body, got, tt.want)
			}
		})
	}
}
