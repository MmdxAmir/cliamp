package model

import (
	"testing"
	"time"

	"github.com/bjarneo/cliamp/favorites"
	"github.com/bjarneo/cliamp/history"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui"
)

// backfillTestProvider is a local playlist store that records its calls.
// With editDuringScan set, Tracks changes the playlist file, as a queue edit
// that saves during the scan does.
type backfillTestProvider struct {
	commandsTestProvider
	tracks         []playlist.Track
	doc            string
	editDuringScan bool
	trackCalls     int
	saves          [][]playlist.Track
}

func (p *backfillTestProvider) Tracks(string) ([]playlist.Track, error) {
	p.trackCalls++
	if p.editDuringScan {
		p.doc += "# edited\n"
	}
	return cloneTracks(p.tracks), nil
}

func (p *backfillTestProvider) PlaylistDocument(string) ([]byte, error) {
	return []byte(p.doc), nil
}

func (p *backfillTestProvider) RestorePlaylistDocument(_ string, data []byte) error {
	p.doc = string(data)
	return nil
}

func (p *backfillTestProvider) SavePlaylist(_ string, tracks []playlist.Track) error {
	p.saves = append(p.saves, cloneTracks(tracks))
	return nil
}

// A track start without a known duration sets the decoded duration in the
// queue. Only an explicit track of a playlist file gets the duration written
// back, and that write runs in a command, not in Update.
func TestBackfillLoadedPlaylistDuration(t *testing.T) {
	explicit := playlist.Track{Path: "/music/a.mp3", Title: "A"}
	fromDir := playlist.Track{Path: "/music/dir/b.mp3", Title: "B", DirSourced: true}
	for _, tc := range []struct {
		name           string
		loaded         string
		track          playlist.Track
		editDuringScan bool
		wantQueue      int
		wantScan       bool
		wantSaved      bool
	}{
		{name: "explicit track", loaded: "Mix", track: explicit, wantQueue: 240, wantScan: true, wantSaved: true},
		{name: "file edited during the scan", loaded: "Mix", track: explicit, editDuringScan: true, wantQueue: 240, wantScan: true},
		{name: "directory track", loaded: "Mix", track: fromDir, wantQueue: 240},
		{name: "favorites", loaded: favorites.PlaylistName, track: explicit},
		{name: "history", loaded: history.PlaylistName, track: explicit},
		{name: "no saved playlist", track: explicit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &backfillTestProvider{
				commandsTestProvider: commandsTestProvider{name: "Local"},
				tracks:               []playlist.Track{explicit, fromDir},
				doc:                  "[[track]]\n",
				editDuringScan:       tc.editDuringScan,
			}
			pl := playlist.New()
			pl.Add(tc.track)
			pl.SetIndex(0)
			m := Model{
				player:         &playbackFakeEngine{duration: 240 * time.Second},
				playlist:       pl,
				vis:            ui.NewVisualizer(44100),
				localProvider:  store,
				loadedPlaylist: tc.loaded,
			}

			cmd := m.playTrack(tc.track)
			if store.trackCalls != 0 || len(store.saves) != 0 {
				t.Fatalf("Update read the playlist %d times and saved it %d times, want none", store.trackCalls, len(store.saves))
			}
			if got, _ := m.playlist.Track(0); got.DurationSecs != tc.wantQueue {
				t.Fatalf("queue duration = %d, want %d", got.DurationSecs, tc.wantQueue)
			}
			runCmd(cmd)
			if scanned := store.trackCalls > 0; scanned != tc.wantScan {
				t.Fatalf("the command read the playlist %d times, want a read %v", store.trackCalls, tc.wantScan)
			}
			if !tc.wantSaved {
				if len(store.saves) != 0 {
					t.Fatalf("the command saved the playlist %d times, want none", len(store.saves))
				}
				return
			}
			if len(store.saves) != 1 {
				t.Fatalf("saves = %d, want 1", len(store.saves))
			}
			if got := store.saves[0][0]; got.Path != explicit.Path || got.DurationSecs != 240 {
				t.Fatalf("saved track = %+v, want %s with 240 s", got, explicit.Path)
			}
		})
	}
}
