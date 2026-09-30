package main

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gopxl/beep/v2"

	"github.com/bjarneo/cliamp/config"
	"github.com/bjarneo/cliamp/player"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
	"github.com/bjarneo/cliamp/ui/model"
)

// isolateProviderEnv points every config and credential lookup at a new
// directory, so buildProviders reads no real config and starts no provider
// from the environment.
func isolateProviderEnv(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	for _, name := range []string{"CLIAMP_CONFIG_DIR", "XDG_CONFIG_HOME", "NAVIDROME_URL", "LYRION_URL"} {
		t.Setenv(name, "")
	}
}

func TestBuildProviders(t *testing.T) {
	always := []string{"cliamp", "radio", "local", "podcast"}
	for _, tt := range []struct {
		name string
		cfg  config.Config
		want []string
	}{
		{name: "empty config", want: always},
		{
			name: "jellyfin only",
			cfg: config.Config{Jellyfin: config.JellyfinConfig{
				URL: "https://jf.example.com", Token: "token", UserID: "user-1",
			}},
			want: append(slices.Clone(always), "jellyfin"),
		},
		{
			name: "navidrome and plex",
			cfg: config.Config{
				Navidrome: config.NavidromeConfig{URL: "https://nd.example.com", User: "user", Password: "secret"},
				Plex:      config.PlexConfig{URL: "https://plex.example.com", Token: "token"},
			},
			want: append(slices.Clone(always), "navidrome", "plex"),
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			isolateProviderEnv(t)
			set := buildProviders(tt.cfg, false)
			t.Cleanup(set.Close)

			var keys []string
			for _, e := range set.entries {
				if e.Provider == nil {
					t.Errorf("entry %q has no provider", e.Key)
				}
				if e.Key != "local" && !slices.ContainsFunc(providerKeys, func(pk providerKey) bool { return pk.key == e.Key }) {
					t.Errorf("entry %q is not in providerKeys, so --provider rejects it", e.Key)
				}
				keys = append(keys, e.Key)
			}
			if !slices.Equal(keys, tt.want) {
				t.Fatalf("keys = %v, want %v", keys, tt.want)
			}
			if set.local == nil || set.radioFavorites == nil {
				t.Fatal("the local provider and the radio favorites must be set")
			}
			if got, want := set.jellyfin() != nil, slices.Contains(tt.want, "jellyfin"); got != want {
				t.Errorf("jellyfin() set = %v, want %v", got, want)
			}
		})
	}
}

// With no HOME, XDG_CONFIG_HOME or CLIAMP_CONFIG_DIR, cliamp has no config
// directory and no local provider. The Model then starts without one and
// does not panic.
func TestBuildProvidersWithoutConfigDir(t *testing.T) {
	for _, name := range []string{"HOME", "CLIAMP_CONFIG_DIR", "XDG_CONFIG_HOME", "NAVIDROME_URL", "LYRION_URL"} {
		t.Setenv(name, "")
	}
	set := buildProviders(config.Config{}, false)
	t.Cleanup(set.Close)
	if set.local != nil {
		t.Fatal("local provider set without a config directory")
	}
	for _, e := range set.entries {
		if e.Key == "local" {
			t.Fatal("the provider list has a local entry without a config directory")
		}
	}
	if lp := set.localPlaylists(); lp != nil {
		t.Fatalf("localPlaylists() = %#v, want a nil interface", lp)
	}
	model.New(&player.Player{}, playlist.New(), set.entries, "cliamp", set.localPlaylists(), nil, nil, config.SaveFunc{})
}

// fakeStreamer decodes fake: URIs, as Spotify decodes spotify: URIs, and
// counts its Close calls.
type fakeStreamer struct {
	closes int
}

func (*fakeStreamer) Name() string                                { return "Fake" }
func (*fakeStreamer) Playlists() ([]playlist.PlaylistInfo, error) { return nil, nil }
func (*fakeStreamer) Tracks(string) ([]playlist.Track, error)     { return nil, nil }
func (*fakeStreamer) URISchemes() []string                        { return []string{"fake:"} }
func (f *fakeStreamer) Close()                                    { f.closes++ }

func (*fakeStreamer) NewStreamer(string) (beep.StreamSeekCloser, beep.Format, time.Duration, error) {
	return nil, beep.Format{}, 0, errors.New("fake streamer opened")
}

var (
	_ provider.CustomStreamer = (*fakeStreamer)(nil)
	_ provider.Closer         = (*fakeStreamer)(nil)
)

// The player opens the URI schemes of every provider.CustomStreamer with
// that provider, and a shutdown closes every provider.Closer. No provider
// name appears in the wiring.
func TestProviderSetUsesCapabilities(t *testing.T) {
	fake := &fakeStreamer{}
	set := &providerSet{entries: []provider.Entry{
		{Key: "radio", Name: "Radio", Provider: &fakeRadio{}},
		{Key: "fake", Name: "Fake", Provider: fake},
	}}

	engine := &player.Player{}
	set.registerPlayerHooks(engine)
	err := engine.PlayAtForGeneration("fake:track:1", time.Minute, 0, 1)
	if err == nil || !strings.Contains(err.Error(), "fake streamer opened") {
		t.Fatalf("PlayAtForGeneration error = %v, want the error of the fake streamer", err)
	}

	set.Close()
	if fake.closes != 1 {
		t.Fatalf("Close calls = %d, want 1", fake.closes)
	}
}

// fakeRadio is a provider with no optional capability.
type fakeRadio struct{}

func (fakeRadio) Name() string                                { return "Radio" }
func (fakeRadio) Playlists() ([]playlist.PlaylistInfo, error) { return nil, nil }
func (fakeRadio) Tracks(string) ([]playlist.Track, error)     { return nil, nil }
