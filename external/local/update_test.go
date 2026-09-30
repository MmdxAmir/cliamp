package local

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/bjarneo/cliamp/favorites"
	"github.com/bjarneo/cliamp/history"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

func TestUpdatePlaylist(t *testing.T) {
	errFn := errors.New("fn failed")
	tests := []struct {
		name      string
		list      string
		fn        func([]playlist.Track) ([]playlist.Track, error)
		wantErr   error // nil when UpdatePlaylist must succeed
		anyErr    bool  // UpdatePlaylist must fail with any error
		wantPaths []string
		wantSame  bool // the file keeps its bytes
	}{
		{
			name: "fn changes the tracks",
			list: "Mix",
			fn: func(tracks []playlist.Track) ([]playlist.Track, error) {
				tracks[0].DurationSecs = 99
				return slices.Delete(tracks, 1, 2), nil
			},
			wantPaths: []string{"/a.mp3"},
		},
		{
			name: "fn reports no change",
			list: "Mix",
			fn: func([]playlist.Track) ([]playlist.Track, error) {
				return nil, provider.ErrPlaylistUnchanged
			},
			wantPaths: []string{"/a.mp3", "/b.mp3"},
			wantSame:  true,
		},
		{
			name: "fn fails",
			list: "Mix",
			fn: func([]playlist.Track) ([]playlist.Track, error) {
				return nil, errFn
			},
			wantErr:   errFn,
			wantPaths: []string{"/a.mp3", "/b.mp3"},
			wantSame:  true,
		},
		{name: "missing playlist", list: "Nope", anyErr: true, wantPaths: []string{"/a.mp3", "/b.mp3"}, wantSame: true},
		{name: "favorites", list: favorites.PlaylistName, wantErr: errReservedFavoritesName, wantPaths: []string{"/a.mp3", "/b.mp3"}, wantSame: true},
		{name: "history", list: history.PlaylistName, wantErr: errReservedHistoryName, wantPaths: []string{"/a.mp3", "/b.mp3"}, wantSame: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newTestProvider(t)
			if err := p.SavePlaylist("Mix", []playlist.Track{{Path: "/a.mp3", Title: "A"}, {Path: "/b.mp3", Title: "B"}}); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(filepath.Join(p.dir, "Mix.toml"))
			if err != nil {
				t.Fatal(err)
			}
			called := false
			fn := func(tracks []playlist.Track) ([]playlist.Track, error) {
				called = true
				if tt.fn == nil {
					return tracks, nil
				}
				return tt.fn(tracks)
			}

			err = p.UpdatePlaylist(tt.list, fn)
			switch {
			case tt.anyErr && err == nil:
				t.Fatal("UpdatePlaylist succeeded, want an error")
			case !tt.anyErr && !errors.Is(err, tt.wantErr):
				t.Fatalf("UpdatePlaylist error = %v, want %v", err, tt.wantErr)
			}
			if (tt.wantErr == errReservedFavoritesName || tt.wantErr == errReservedHistoryName) && called {
				t.Fatal("fn ran for a virtual playlist")
			}
			after, err := os.ReadFile(filepath.Join(p.dir, "Mix.toml"))
			if err != nil {
				t.Fatal(err)
			}
			if same := string(before) == string(after); same != tt.wantSame {
				t.Fatalf("file unchanged = %v, want %v:\n%s", same, tt.wantSame, after)
			}
			tracks, err := p.Tracks("Mix")
			if err != nil {
				t.Fatal(err)
			}
			if got := paths(tracks); !slices.Equal(got, tt.wantPaths) {
				t.Fatalf("tracks = %v, want %v", got, tt.wantPaths)
			}
		})
	}
}

// fn gets the directory tracks in document order, and the save keeps the
// [[dir]] section. fn cannot write a directory track as an explicit track.
func TestUpdatePlaylistKeepsDirSources(t *testing.T) {
	p := newTestProvider(t)
	music := t.TempDir()
	if err := os.WriteFile(filepath.Join(music, "dir.mp3"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := p.SavePlaylist("Mix", []playlist.Track{{Path: "/a.mp3", Title: "A"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.AddDirSources("Mix", []string{music}); err != nil {
		t.Fatal(err)
	}

	var seen []playlist.Track
	err := p.UpdatePlaylist("Mix", func(tracks []playlist.Track) ([]playlist.Track, error) {
		seen = slices.Clone(tracks)
		return append(tracks, playlist.Track{Path: "/b.mp3", Title: "B"}), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 || seen[0].Path != "/a.mp3" || !seen[1].DirSourced {
		t.Fatalf("fn got %+v, want /a.mp3 and the directory track", seen)
	}
	dirs, err := p.DirSources("Mix")
	if err != nil || len(dirs) != 1 {
		t.Fatalf("dir sources = %+v, %v, want the one source", dirs, err)
	}
	doc, err := p.loadDocByName("Mix")
	if err != nil {
		t.Fatal(err)
	}
	if got := paths(doc.tracks); !slices.Equal(got, []string{"/a.mp3", "/b.mp3"}) {
		t.Fatalf("explicit tracks = %v, want /a.mp3 and /b.mp3", got)
	}
}
