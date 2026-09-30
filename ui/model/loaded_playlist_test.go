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
	"github.com/bjarneo/cliamp/provider"
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
			favs, hist := favorites.New(), history.New()
			lp := local.New(favs, hist)
			if err := lp.SavePlaylist("Mix", []playlist.Track{a, b}); err != nil {
				t.Fatal(err)
			}
			for i, track := range []playlist.Track{b, a} {
				if err := hist.Record(track, time.Unix(int64(1000+i), 0)); err != nil {
					t.Fatal(err)
				}
				if _, err := favs.Toggle(track); err != nil {
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
				providers: []provider.Entry{
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

// x on a row of a loaded playlist removes the track from the playlist file
// in one locked update. A track that another writer added after the load
// survives the removal, and Ctrl+Z puts the removed track back.
func TestQueueRemoveUpdatesTheLoadedPlaylistFile(t *testing.T) {
	a := playlist.Track{Path: "/music/a.mp3", Title: "A"}
	b := playlist.Track{Path: "/music/b.mp3", Title: "B"}
	added := playlist.Track{Path: "/music/added.mp3", Title: "Added"}
	for _, tc := range []struct {
		name      string
		otherAdds bool // another writer adds a track after the load
		wantMix   []string
		wantUndo  []string
	}{
		{name: "only this writer", wantMix: []string{a.Path}, wantUndo: []string{a.Path, b.Path}},
		// The undo save keeps the file order of the tracks it finds and
		// puts the removed track after them.
		{name: "another writer added a track", otherAdds: true, wantMix: []string{a.Path, added.Path}, wantUndo: []string{a.Path, added.Path, b.Path}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
			lp := local.New(nil, nil)
			if err := lp.SavePlaylist("Mix", []playlist.Track{a, b}); err != nil {
				t.Fatal(err)
			}
			m := Model{
				player:        &playbackFakeEngine{},
				playlist:      playlist.New(),
				vis:           ui.NewVisualizer(44100),
				provider:      lp,
				localProvider: lp,
				providers:     []provider.Entry{{Key: "local", Name: "Local", Provider: lp}},
			}
			if response := runV2(t, &m, "provider.load", ipc.Request{Provider: "local", Playlist: "Mix"}); !response.OK {
				t.Fatalf("provider.load = %+v", response)
			}
			if tc.otherAdds {
				if _, _, err := local.New(nil, nil).AddTracks("Mix", []playlist.Track{added}); err != nil {
					t.Fatal(err)
				}
			}

			m.focus = focusPlaylist
			m.plCursor = 1
			m.handleKey(tea.KeyPressMsg{Text: "x"})
			if m.status.kind == feedbackError {
				t.Fatalf("unexpected error: %s", m.status.text)
			}
			mix, err := lp.Tracks("Mix")
			if err != nil {
				t.Fatal(err)
			}
			if got := trackPaths(mix); !reflect.DeepEqual(got, tc.wantMix) {
				t.Fatalf("Mix after x = %v, want %v", got, tc.wantMix)
			}

			m.handleKey(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
			if mix, err = lp.Tracks("Mix"); err != nil {
				t.Fatal(err)
			}
			if got := trackPaths(mix); !reflect.DeepEqual(got, tc.wantUndo) {
				t.Fatalf("Mix after undo = %v, want %v", got, tc.wantUndo)
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
	favs := favorites.New()
	lp := local.New(favs, nil)
	if _, err := favs.Toggle(station); err != nil {
		t.Fatal(err)
	}
	m.provider, m.localProvider, m.favStore = lp, lp, favs
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
	if favs.IsFavorited(station.Path) {
		t.Fatal("f did not remove the station from Favorites")
	}
	if m.radioFavorites.Count() != 0 {
		t.Fatal("f toggled the station favorite for a row in Favorites")
	}
	if m.playlistTrackFavorited(row) {
		t.Fatal("the row still shows a ♥ after f")
	}
}

// The runtime snapshot names the loaded local list, or else the provider
// list of the last IPC load as key:id. cliamp status --json shows it. A
// queue change that drops the loaded list drops the name too.
func TestV2SnapshotNamesTheLoadedList(t *testing.T) {
	type step struct {
		op     string
		params ipc.Request
	}
	loadRemote := step{"provider.load", ipc.Request{Provider: "navidrome", Playlist: "42"}}
	loadMix := step{"load", ipc.Request{Playlist: "Mix"}}
	for _, tc := range []struct {
		name  string
		steps []step
		want  string
	}{
		{name: "load", steps: []step{loadMix}, want: "Mix"},
		{name: "local playlist", steps: []step{{"provider.load", ipc.Request{Provider: "local", Playlist: "Mix"}}}, want: "Mix"},
		{name: "local history", steps: []step{{"provider.load", ipc.Request{Provider: "local", Playlist: history.PlaylistName}}}, want: "local:" + history.PlaylistName},
		{name: "remote playlist", steps: []step{loadRemote}, want: "navidrome:42"},
		{name: "remote album", steps: []step{{"provider.load_album", ipc.Request{Provider: "navidrome", Album: "7"}}}, want: "navidrome:album:7"},
		{name: "remote then local", steps: []step{loadRemote, loadMix}, want: "Mix"},
		{name: "local then remote", steps: []step{loadMix, loadRemote}, want: "navidrome:42"},
		{name: "remote then queue", steps: []step{loadRemote, {"queue", ipc.Request{Path: "/music/c.mp3"}}}},
		{name: "remote then clear", steps: []step{loadRemote, {"queue.clear", ipc.Request{}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tracks := []playlist.Track{{Path: "/music/a.mp3", Title: "A"}, {Path: "/music/b.mp3", Title: "B"}}
			local := fixedTracksProvider{commandsTestProvider{name: "Local"}, tracks}
			navidrome := remoteListProvider{commandsTestProvider{name: "Navidrome"}, tracks}
			m := Model{
				player:        &playbackFakeEngine{},
				playlist:      playlist.New(),
				vis:           ui.NewVisualizer(44100),
				localProvider: local,
				providers: []provider.Entry{
					{Key: "local", Name: "Local", Provider: local},
					{Key: "navidrome", Name: "Navidrome", Provider: navidrome},
				},
			}

			for _, s := range tc.steps {
				if response := runV2(t, &m, s.op, s.params); !response.OK {
					t.Fatalf("%s response = %+v, want OK", s.op, response)
				}
			}
			if got := m.runtimeSnapshot().Playlist; got != tc.want {
				t.Fatalf("snapshot playlist = %q, want %q", got, tc.want)
			}
			if got := m.runtimeFingerprint().playlist; got != tc.want {
				t.Fatalf("fingerprint playlist = %q, want %q", got, tc.want)
			}
		})
	}
}
