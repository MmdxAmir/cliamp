package local

import (
	"fmt"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
)

// The TUI, the daemon and the `cliamp playlist` CLI each build their own
// Provider. Two Provider values that change one playlist at the same time
// must not lose each other's change.
func TestSeparateProvidersKeepConcurrentChanges(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fileutil.LockFile takes no lock on Windows")
	}
	const n = 15
	const name = "Mix"
	track := func(worker, i int) playlist.Track {
		return playlist.Track{Path: fmt.Sprintf("/w%d-%d.mp3", worker, i)}
	}
	addTrack := func(p *Provider, i int) error {
		_, _, err := p.AddTracks(name, []playlist.Track{track(1, i)})
		return err
	}
	addDirs := func(p *Provider, dirs []string) error {
		_, err := p.AddDirSources(name, dirs)
		return err
	}

	tests := []struct {
		name       string
		setup      func(p *Provider, dirs []string) error
		op         func(p *Provider, dirs []string, i int) error
		wantTracks int
		wantDirs   int
		wantRecurs bool
	}{
		{
			name: "AddTracks",
			op: func(p *Provider, _ []string, i int) error {
				_, _, err := p.AddTracks(name, []playlist.Track{track(0, i)})
				return err
			},
			wantTracks: 2 * n,
		},
		{
			name: "PrependTracks",
			op: func(p *Provider, _ []string, i int) error {
				_, _, _, err := p.PrependTracks(name, []playlist.Track{track(0, i)})
				return err
			},
			wantTracks: 2 * n,
		},
		{
			// Removals take index 0 and the adds append, so each removal
			// hits one of the n tracks that the setup saved.
			name: "RemoveTrack",
			setup: func(p *Provider, _ []string) error {
				base := make([]playlist.Track, n)
				for i := range base {
					base[i] = track(0, i)
				}
				return p.SavePlaylist(name, base)
			},
			op: func(p *Provider, _ []string, _ int) error {
				return p.RemoveTrack(name, 0)
			},
			wantTracks: n,
		},
		{
			name: "AddDirSources",
			op: func(p *Provider, dirs []string, i int) error {
				_, err := p.AddDirSources(name, []string{dirs[i]})
				return err
			},
			wantTracks: n,
			wantDirs:   n,
			wantRecurs: true,
		},
		{
			name: "UpdatePlaylist",
			setup: func(p *Provider, _ []string) error {
				return p.SavePlaylist(name, nil)
			},
			op: func(p *Provider, _ []string, i int) error {
				return p.UpdatePlaylist(name, func(tracks []playlist.Track) ([]playlist.Track, error) {
					return append(tracks, track(0, i)), nil
				})
			},
			wantTracks: 2 * n,
		},
		{
			name:  "RemoveDirSource",
			setup: addDirs,
			op: func(p *Provider, dirs []string, i int) error {
				return p.RemoveDirSource(name, dirs[i])
			},
			wantTracks: n,
		},
		{
			name:  "SetDirRecursive",
			setup: addDirs,
			op: func(p *Provider, dirs []string, i int) error {
				return p.SetDirRecursive(name, dirs[i], false)
			},
			wantTracks: n,
			wantDirs:   n,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name+" and AddTracks", func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "playlists")
			dirs := make([]string, n)
			for i := range dirs {
				dirs[i] = t.TempDir()
			}
			a, b := &Provider{dir: dir}, &Provider{dir: dir}
			if tt.setup != nil {
				if err := tt.setup(a, dirs); err != nil {
					t.Fatalf("setup: %v", err)
				}
			}

			errs := make(chan error, 2*n)
			var wg sync.WaitGroup
			wg.Go(func() {
				for i := range n {
					errs <- tt.op(a, dirs, i)
				}
			})
			wg.Go(func() {
				for i := range n {
					errs <- addTrack(b, i)
				}
			})
			wg.Wait()
			close(errs)
			for err := range errs {
				if err != nil {
					t.Fatalf("concurrent change: %v", err)
				}
			}

			tracks, err := a.Tracks(name)
			if err != nil {
				t.Fatalf("Tracks: %v", err)
			}
			if len(tracks) != tt.wantTracks {
				t.Errorf("got %d tracks, want %d: %v", len(tracks), tt.wantTracks, paths(tracks))
			}
			got, err := a.DirSources(name)
			if err != nil {
				t.Fatalf("DirSources: %v", err)
			}
			if len(got) != tt.wantDirs {
				t.Errorf("got %d dir sources, want %d", len(got), tt.wantDirs)
			}
			for _, src := range got {
				if src.Recursive != tt.wantRecurs {
					t.Errorf("dir source %s recursive = %v, want %v", src.Path, src.Recursive, tt.wantRecurs)
				}
			}
		})
	}
}
