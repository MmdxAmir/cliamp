package model

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/external/local"
	"github.com/bjarneo/cliamp/favorites"
	"github.com/bjarneo/cliamp/history"
	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui"
)

// remoteListProvider serves the same tracks for every playlist and album, as
// a Navidrome server does for one list.
type remoteListProvider struct {
	commandsTestProvider
	tracks []playlist.Track
}

func (p remoteListProvider) Tracks(string) ([]playlist.Track, error)      { return p.tracks, nil }
func (p remoteListProvider) AlbumTracks(string) ([]playlist.Track, error) { return p.tracks, nil }

// dirFiles returns the names of the files in dir.
func dirFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// trackPaths returns the paths of tracks in order.
func trackPaths(tracks []playlist.Track) []string {
	paths := make([]string, len(tracks))
	for i, track := range tracks {
		paths[i] = track.Path
	}
	return paths
}

// A V2 provider.load marks only a saved local playlist as the loaded list.
// shift+down then writes the new order to that playlist file only. Favorites
// stays the loaded list for the ♥ rule, but it is not a playlist file.
func TestV2ProviderLoadKeepsWriteBacksLocal(t *testing.T) {
	a := playlist.Track{Path: "/music/a.mp3", Title: "A"}
	b := playlist.Track{Path: "/music/b.mp3", Title: "B"}
	remote := []playlist.Track{{Path: "/navidrome/1.flac", Title: "One"}, {Path: "/navidrome/2.flac", Title: "Two"}}
	for _, tc := range []struct {
		name       string
		op         string
		params     ipc.Request
		wantLoaded string
		wantMix    []string
	}{
		{name: "local playlist", op: "provider.load", params: ipc.Request{Provider: "local", Playlist: "Mix"}, wantLoaded: "Mix", wantMix: []string{b.Path, a.Path}},
		{name: "local favorites", op: "provider.load", params: ipc.Request{Provider: "local", Playlist: favorites.PlaylistName}, wantLoaded: favorites.PlaylistName, wantMix: []string{a.Path, b.Path}},
		{name: "local history", op: "provider.load", params: ipc.Request{Provider: "local", Playlist: history.PlaylistName}, wantMix: []string{a.Path, b.Path}},
		{name: "navidrome playlist", op: "provider.load", params: ipc.Request{Provider: "navidrome", Playlist: "42"}, wantMix: []string{a.Path, b.Path}},
		{name: "navidrome album", op: "provider.load_album", params: ipc.Request{Provider: "navidrome", Album: "7"}, wantMix: []string{a.Path, b.Path}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("CLIAMP_CONFIG_DIR", dir)
			lp := local.New()
			if err := lp.SavePlaylist("Mix", []playlist.Track{a, b}); err != nil {
				t.Fatal(err)
			}
			for i, track := range []playlist.Track{b, a} {
				if err := history.New().Record(track, time.Unix(int64(1000+i), 0)); err != nil {
					t.Fatal(err)
				}
				if _, err := lp.ToggleFavorite(track); err != nil {
					t.Fatal(err)
				}
			}
			navidrome := remoteListProvider{commandsTestProvider{name: "Navidrome"}, remote}
			m := Model{
				player:        &playbackFakeEngine{},
				playlist:      playlist.New(),
				vis:           ui.NewVisualizer(44100),
				provider:      lp,
				localProvider: lp,
				providers: []ProviderEntry{
					{Key: "local", Name: "Local", Provider: lp},
					{Key: "navidrome", Name: "Navidrome", Provider: navidrome},
				},
			}

			if response := runV2(t, &m, tc.op, tc.params); !response.OK || response.Total != 2 {
				t.Fatalf("response = %+v, want 2 loaded tracks", response)
			}
			if m.loadedPlaylist != tc.wantLoaded {
				t.Fatalf("loadedPlaylist = %q, want %q", m.loadedPlaylist, tc.wantLoaded)
			}

			m.focus = focusPlaylist
			m.plCursor = 0
			m.handleKey(tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModShift})
			if m.plCursor != 1 {
				t.Fatalf("plCursor = %d, want 1 after the move", m.plCursor)
			}
			if m.status.kind == feedbackError {
				t.Fatalf("unexpected error: %s", m.status.text)
			}
			if got := dirFiles(t, filepath.Join(dir, "playlists")); !reflect.DeepEqual(got, []string{"Mix.toml"}) {
				t.Fatalf("playlist files = %v, want only Mix.toml", got)
			}
			mix, err := lp.Tracks("Mix")
			if err != nil {
				t.Fatal(err)
			}
			if got := trackPaths(mix); !reflect.DeepEqual(got, tc.wantMix) {
				t.Fatalf("Mix order = %v, want %v", got, tc.wantMix)
			}
		})
	}
}

// Favorites can hold a radio station, because f on a station row in a saved
// playlist adds it there. A key load of Favorites keeps the list as the saved
// list of the ♥ rule, so f on that row removes it from Favorites and does not
// toggle the station favorite. Favorites is still no playlist file.
func TestStationRowInFavoritesTogglesTrackFavorite(t *testing.T) {
	m, _, tracks := radioFavoriteTestModel(t)
	station := tracks[0]
	lp := local.New()
	if _, err := lp.ToggleFavorite(station); err != nil {
		t.Fatal(err)
	}
	m.provider, m.localProvider, m.favMgr = lp, lp, lp
	m.refreshFavSet()

	m.requests.tracks = 1
	updated, _ := m.Update(fetchTracksCmd(lp, favorites.PlaylistName, 1)())
	m = updated.(Model)
	row, ok := m.playlist.Track(0)
	if !ok || row.Path != station.Path || !row.Realtime || row.Meta("radio.name") == "" {
		t.Fatalf("Favorites row = %+v, want the station with its radio metadata", row)
	}
	if m.loadedPlaylist != favorites.PlaylistName || m.writableLoadedPlaylist() != "" {
		t.Fatalf("loaded %q, writable %q; want Favorites as the saved list and no writable name", m.loadedPlaylist, m.writableLoadedPlaylist())
	}
	if !m.playlistTrackFavorited(row) {
		t.Fatal("the station row in Favorites shows no ♥")
	}

	m.focus = focusPlaylist
	m.plCursor = 0
	m.handleKey(tea.KeyPressMsg{Text: "f"})
	if lp.IsFavorited(station.Path) {
		t.Fatal("f did not remove the station from Favorites")
	}
	if m.radioFavorites.Count() != 0 {
		t.Fatal("f toggled the station favorite for a row in Favorites")
	}
	if m.playlistTrackFavorited(row) {
		t.Fatal("the row still shows a ♥ after f")
	}
}
