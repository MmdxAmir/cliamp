package model

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/lyrics"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// fakeLyricsSource answers for the tracks whose path starts with prefix, as
// the Spotify provider does for spotify:track: paths.
type fakeLyricsSource struct {
	playlist.Provider
	prefix string
	lines  []lyrics.Line
	asked  []string
}

func (f *fakeLyricsSource) TrackLyrics(_ context.Context, track playlist.Track) ([]lyrics.Line, error) {
	f.asked = append(f.asked, track.Path)
	if !strings.HasPrefix(track.Path, f.prefix) {
		return nil, lyrics.ErrNotFound
	}
	return f.lines, nil
}

func TestFetchTrackLyricsCmdSources(t *testing.T) {
	spotifyLines := []lyrics.Line{{Start: time.Second, Text: "spotify line"}}
	otherLines := []lyrics.Line{{Start: time.Second, Text: "other line"}}
	tests := []struct {
		name     string
		track    playlist.Track
		wantText string
	}{
		{name: "the owning source answers", track: playlist.Track{Path: "spotify:track:abc"}, wantText: "spotify line"},
		{name: "a second source answers", track: playlist.Track{Path: "other:track:1"}, wantText: "other line"},
		{name: "embedded lyrics come first", track: playlist.Track{Path: "spotify:track:abc", EmbeddedLyrics: "[00:01.00]embedded"}, wantText: "embedded"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sources := []trackLyricsSource{
				&fakeLyricsSource{prefix: "spotify:track:", lines: spotifyLines},
				&fakeLyricsSource{prefix: "other:track:", lines: otherLines},
			}
			msg := fetchTrackLyricsCmd(tt.track, "Artist", "Title", "Artist\nTitle", 1, sources)().(lyricsLoadedMsg)
			if msg.err != nil {
				t.Fatalf("err = %v, want nil", msg.err)
			}
			if len(msg.lines) != 1 || msg.lines[0].Text != tt.wantText {
				t.Fatalf("lines = %+v, want %q", msg.lines, tt.wantText)
			}
		})
	}
}

// The Model asks every registered provider that has the capability. It
// does not pick a provider by its key.
func TestTrackLyricsSourcesFromProviders(t *testing.T) {
	spotify := &fakeLyricsSource{prefix: "spotify:track:"}
	renamed := &fakeLyricsSource{prefix: "other:track:"}
	m := Model{
		providers: []provider.Entry{
			{Key: "radio", Name: "Radio", Provider: commandsTestProvider{name: "Radio"}},
			{Key: "spotify", Name: "Spotify", Provider: spotify},
			{Key: "spotify-2", Name: "Other", Provider: renamed},
			{Key: "jellyfin", Name: "Jellyfin"}, // not configured: nil Provider
		},
	}
	got := m.trackLyricsSources()
	if len(got) != 2 || got[0] != spotify || got[1] != renamed {
		t.Fatalf("trackLyricsSources() = %v, want the two capable providers", got)
	}
	if got := (Model{}).trackLyricsSources(); len(got) != 0 {
		t.Fatalf("trackLyricsSources() without providers = %v, want none", got)
	}
}
