package main

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/config"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// everyProviderConfig enables every provider that needs no external tool.
// YouTube needs yt-dlp, so it stays off.
func everyProviderConfig() config.Config {
	return config.Config{
		Navidrome:      config.NavidromeConfig{URL: "https://nd.example.com", User: "user", Password: "secret"},
		Lyrion:         config.LyrionConfig{URL: "http://lms.example.com:9000"},
		Plex:           config.PlexConfig{URL: "https://plex.example.com", Token: "token"},
		Jellyfin:       config.JellyfinConfig{URL: "https://jf.example.com", Token: "token", UserID: "user-1"},
		Emby:           config.EmbyConfig{URL: "https://emby.example.com", Token: "token", UserID: "user-1"},
		Audiobookshelf: config.AudiobookshelfConfig{URL: "https://abs.example.com", Token: "token"},
		Spotify:        config.SpotifyConfig{Enabled: true},
		Qobuz:          config.QobuzConfig{Enabled: true},
		Tidal:          config.TidalConfig{Enabled: true},
		SoundCloud:     config.SoundCloudConfig{Enabled: true},
		Mixcloud:       config.MixcloudConfig{Enabled: true},
		NetEase:        config.NetEaseConfig{Enabled: true},
		Yandex:         config.YandexConfig{Enabled: true, Token: "token"},
	}
}

// docList returns the names in a list such as "A, B, and C".
func docList(s string) []string {
	s = strings.ReplaceAll(s, ", and ", ", ")
	s = strings.ReplaceAll(s, " and ", ", ")
	return strings.Split(s, ", ")
}

// TestDocsNameProviderCapabilities checks the provider lists of the docs
// against the interfaces that each registered provider implements.
func TestDocsNameProviderCapabilities(t *testing.T) {
	isolateProviderEnv(t)
	set := buildProviders(everyProviderConfig(), false)
	t.Cleanup(set.Close)

	var keys []string
	for _, e := range set.entries {
		keys = append(keys, e.Key)
	}
	for _, pk := range providerKeys {
		if !slices.Contains([]string{"yt", "youtube", "ytmusic"}, pk.key) && !slices.Contains(keys, pk.key) {
			t.Fatalf("everyProviderConfig does not enable %s", pk.key)
		}
	}

	tests := []struct {
		name    string
		file    string
		list    *regexp.Regexp // captures the list of provider names
		capable func(playlist.Provider) bool
		skip    string // a provider key that the list leaves out on purpose
	}{
		{
			name: "playback reports",
			file: "headless.md",
			list: regexp.MustCompile(`(?m)^- (.+) get now-playing and scrobble reports\.`),
			capable: func(p playlist.Provider) bool {
				_, ok := p.(provider.PlaybackReporter)
				return ok
			},
			// Podcasts keep the listening position on this computer.
			skip: "podcast",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("docs", tt.file))
			if err != nil {
				t.Fatalf("read docs/%s: %v", tt.file, err)
			}
			m := tt.list.FindStringSubmatch(string(data))
			if m == nil {
				t.Fatalf("docs/%s has no match for %q", tt.file, tt.list)
			}
			documented := docList(m[1])
			var want []string
			for _, e := range set.entries {
				if e.Key != tt.skip && tt.capable(e.Provider) {
					want = append(want, e.Name)
				}
			}
			slices.Sort(documented)
			slices.Sort(want)
			if !slices.Equal(documented, want) {
				t.Errorf("docs/%s names %v, want %v", tt.file, documented, want)
			}
		})
	}
}
