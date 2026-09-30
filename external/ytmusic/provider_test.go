package ytmusic

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/playlist"
)

func TestRefreshInvalidatesAllCaches(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	b := newBase(nil, "client-id", "client-secret", false)

	b.allPlaylists = []playlistEntry{{ID: "p1", Name: "One", TrackCount: 5}}
	b.classified = map[string]bool{"p1": true}
	b.trackCache["p1"] = []playlist.Track{{Path: "https://example/v", Title: "t"}}

	dc := b.ensureDiskCache()
	dc.setPlaylists(b.allPlaylists)
	dc.setTracks("p1", b.trackCache["p1"])
	saveSnapshot(dc.snapshot())

	if !dc.playlistsFresh() {
		t.Fatal("disk cache should be fresh before refresh")
	}

	b.refresh()

	if b.allPlaylists != nil {
		t.Error("allPlaylists not cleared")
	}
	if b.classified != nil {
		t.Error("classified not cleared")
	}
	if len(b.trackCache) != 0 {
		t.Errorf("trackCache not cleared: %d entries", len(b.trackCache))
	}

	if b.disk.playlistsFresh() {
		t.Error("disk cache still reports fresh after refresh")
	}
	if !b.disk.PlaylistsAt.IsZero() {
		t.Errorf("PlaylistsAt should be zero, got %v", b.disk.PlaylistsAt)
	}
	if len(b.disk.Playlists) != 0 {
		t.Errorf("disk Playlists not cleared: %d entries", len(b.disk.Playlists))
	}
	if len(b.disk.Tracks) != 0 {
		t.Errorf("disk Tracks not cleared: %d entries", len(b.disk.Tracks))
	}

	reloaded := loadYTCache(storedOAuthCacheScope("client-id"))
	if reloaded.playlistsFresh() {
		t.Error("reloaded disk cache still fresh after refresh")
	}
	if !reloaded.PlaylistsAt.Equal(time.Time{}) {
		t.Errorf("reloaded PlaylistsAt should be zero, got %v", reloaded.PlaylistsAt)
	}
	if len(reloaded.Tracks) != 0 {
		t.Errorf("reloaded disk Tracks not cleared: %d entries", len(reloaded.Tracks))
	}
}

// The three OAuth providers share one base. The Close of each one ends the
// sign-in in progress and drops the session, and every later Close on any of
// the three does nothing more. So a shutdown that closes all three is safe.
func TestProvidersCloseSharedBase(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, tt := range []struct {
		name  string
		close func(Providers)
	}{
		{name: "music", close: func(p Providers) { p.Music.Close() }},
		{name: "video", close: func(p Providers) { p.Video.Close() }},
		{name: "all", close: func(p Providers) { p.All.Close() }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			provs := New(&Session{}, "client-id", "client-secret", false)
			cancels := 0
			provs.Music.base.authCancel = func() { cancels++ }

			tt.close(provs)
			if cancels != 1 {
				t.Fatalf("sign-in cancels = %d, want 1", cancels)
			}
			if provs.Music.base.session != nil {
				t.Fatal("session still set after Close")
			}

			provs.Music.Close()
			provs.Video.Close()
			provs.All.Close()
			if cancels != 1 {
				t.Fatalf("sign-in cancels after a second Close = %d, want 1", cancels)
			}
		})
	}
}

// TestAuthenticateCancelsEarlierFlow runs three overlapping sign-ins. Each
// new call must cancel the one before it, also after an older call returns
// late, and Close must cancel the last one.
func TestAuthenticateCancelsEarlierFlow(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	type flow struct {
		ctx     context.Context
		release chan struct{}
	}
	started := make(chan flow)
	orig := signIn
	t.Cleanup(func() { signIn = orig })
	signIn = func(ctx context.Context, _, _ string) (*Session, error) {
		f := flow{ctx, make(chan struct{})}
		started <- f
		<-f.release
		return nil, ctx.Err()
	}

	p := New(nil, "client-id", "client-secret", false).Music
	errs := make(chan error, 3)
	var flows []flow
	// finish lets flow i return and checks that it ended as canceled.
	finish := func(i int) {
		t.Helper()
		close(flows[i].release)
		if err := <-errs; !errors.Is(err, context.Canceled) {
			t.Fatalf("sign-in %d error = %v, want context.Canceled", i+1, err)
		}
	}
	for i := range 3 {
		go func() { errs <- p.Authenticate() }()
		flows = append(flows, <-started)
		if i == 0 {
			continue
		}
		if flows[i-1].ctx.Err() == nil {
			t.Fatalf("sign-in %d did not cancel sign-in %d", i+1, i)
		}
		// The older call returns after the newer call took over.
		finish(i - 1)
	}
	p.Close()
	if flows[2].ctx.Err() == nil {
		t.Fatal("Close did not cancel the last sign-in")
	}
	finish(2)
}
