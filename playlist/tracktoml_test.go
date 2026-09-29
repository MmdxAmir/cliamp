package playlist

import (
	"reflect"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/internal/tomlutil"
)

// fullTOMLTrack sets every exported Track field, so the tests show which
// fields the codec keeps and which it drops.
func fullTOMLTrack() Track {
	return Track{
		Path:           "https://music.example.com/rest/stream?id=42",
		Title:          `Say "Hi"`,
		Artist:         "Artist",
		Album:          "Album",
		Genre:          "Rock",
		Year:           1979,
		TrackNumber:    3,
		Stream:         true,
		Realtime:       true,
		Feed:           true,
		DurationSecs:   208,
		Bookmark:       true,
		Unplayable:     true,
		DirSourced:     true,
		EmbeddedLyrics: "[00:01.00]Line",
		AlbumArtURL:    "file:///tmp/cover.jpg",
		ProviderMeta: map[string]string{
			"radio.name":   "Station",
			"navidrome.id": "42",
			"podcast.feed": "https://feed.example.com/rss",
		},
	}
}

// A new Track field must be added to fullTOMLTrack, and the author must then
// decide if the codec persists it.
func TestFullTOMLTrackSetsEveryField(t *testing.T) {
	v := reflect.ValueOf(fullTOMLTrack())
	for i := range v.NumField() {
		f := v.Type().Field(i)
		if f.IsExported() && v.Field(i).IsZero() {
			t.Errorf("fullTOMLTrack does not set %s", f.Name)
		}
	}
}

func roundTripTOML(t *testing.T, in Track) Track {
	t.Helper()
	var b strings.Builder
	b.WriteString("[[entry]]\n")
	WriteTrackTOML(&b, in)
	var got []Track
	tomlutil.ParseSections([]byte(b.String()), "entry", func(f map[string]string) {
		got = append(got, TrackFromTOML(f))
	})
	if len(got) != 1 {
		t.Fatalf("parsed %d sections, want 1:\n%s", len(got), b.String())
	}
	return got[0]
}

func TestTrackTOMLRoundTrip(t *testing.T) {
	full := fullTOMLTrack()
	tests := []struct {
		name string
		in   Track
		want Track
	}{
		{
			name: "every field",
			in:   full,
			want: Track{
				Path:         full.Path,
				Title:        full.Title,
				Artist:       full.Artist,
				Album:        full.Album,
				Genre:        full.Genre,
				Year:         full.Year,
				TrackNumber:  full.TrackNumber,
				Stream:       true,
				Realtime:     true,
				Feed:         true,
				DurationSecs: full.DurationSecs,
				ProviderMeta: full.ProviderMeta,
			},
		},
		{
			name: "path only",
			in:   Track{Path: "/music/a.mp3"},
			want: Track{Path: "/music/a.mp3"},
		},
		{
			name: "stream follows the path",
			in:   Track{Path: "http://radio.example.com/live", Title: "Live"},
			want: Track{Path: "http://radio.example.com/live", Title: "Live", Stream: true},
		},
		{
			name: "empty meta map loads as nil",
			in:   Track{Path: "/a.mp3", ProviderMeta: map[string]string{}},
			want: Track{Path: "/a.mp3"},
		},
		{
			name: "escapes survive",
			in: Track{
				Path:         `/music/a "b" \ c.mp3`,
				Title:        "line one\nline two",
				Artist:       "Björk",
				ProviderMeta: map[string]string{"x.id": `a = "b"`, "x.empty": ""},
			},
			want: Track{
				Path:         `/music/a "b" \ c.mp3`,
				Title:        "line one\nline two",
				Artist:       "Björk",
				ProviderMeta: map[string]string{"x.id": `a = "b"`, "x.empty": ""},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := roundTripTOML(t, tt.in); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("round trip:\n got %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestWriteTrackTOMLGolden(t *testing.T) {
	const want = `path = "https://music.example.com/rest/stream?id=42"
title = "Say \"Hi\""
artist = "Artist"
album = "Album"
genre = "Rock"
year = 1979
track_number = 3
duration_secs = 208
feed = true
realtime = true
provider_meta.navidrome.id = "42"
provider_meta.podcast.feed = "https://feed.example.com/rss"
provider_meta.radio.name = "Station"
`
	// Map order is random, so write more than once to catch unsorted keys.
	for range 20 {
		var b strings.Builder
		WriteTrackTOML(&b, fullTOMLTrack())
		if got := b.String(); got != want {
			t.Fatalf("WriteTrackTOML:\n got:\n%s\nwant:\n%s", got, want)
		}
	}
}

func TestTrackFromTOMLIgnoresUnknownKeysAndBadNumbers(t *testing.T) {
	got := TrackFromTOML(map[string]string{
		"path":         "/a.mp3",
		"year":         "not a year",
		"played_at":    "2026-05-06T22:09:11Z",
		"feed":         "yes",
		"provider_met": "typo",
	})
	want := Track{Path: "/a.mp3"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("TrackFromTOML = %+v, want %+v", got, want)
	}
}
