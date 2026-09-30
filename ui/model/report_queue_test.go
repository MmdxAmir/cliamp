package model

import (
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// waitIdle waits until q has run every report it got.
func (q *reportQueue) waitIdle(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		q.mu.Lock()
		idle := !q.running && len(q.pending) == 0
		q.mu.Unlock()
		if idle {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("report queue did not drain")
		}
		time.Sleep(time.Millisecond)
	}
}

// The reports run one at a time, in the order they were added, also when
// an early report is slow.
func TestReportQueueKeepsOrder(t *testing.T) {
	for _, tt := range []struct {
		name  string
		slow  int // the report that sleeps
		count int
	}{
		{name: "one report", slow: -1, count: 1},
		{name: "fast reports", slow: -1, count: 50},
		{name: "a slow first report", slow: 0, count: 20},
		{name: "a slow middle report", slow: 10, count: 20},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var q reportQueue
			var mu sync.Mutex
			var order []int
			var running, overlap atomic.Int32
			for i := range tt.count {
				q.add(func() {
					if running.Add(1) > 1 {
						overlap.Add(1)
					}
					defer running.Add(-1)
					if i == tt.slow {
						time.Sleep(20 * time.Millisecond)
					}
					mu.Lock()
					order = append(order, i)
					mu.Unlock()
				})
			}
			q.waitIdle(t)
			if overlap.Load() != 0 {
				t.Fatal("two reports ran at the same time")
			}
			if len(order) != tt.count {
				t.Fatalf("ran %d reports, want %d", len(order), tt.count)
			}
			for i, got := range order {
				if got != i {
					t.Fatalf("order = %v, want the add order", order)
				}
			}
		})
	}
}

// orderReporter records each report as "kind path". The first report
// sleeps, so a report that does not wait for it would finish first.
type orderReporter struct {
	plainProv
	mu      sync.Mutex
	reports []string
}

func (r *orderReporter) record(kind string, track playlist.Track) {
	r.mu.Lock()
	first := len(r.reports) == 0
	r.mu.Unlock()
	if first {
		time.Sleep(20 * time.Millisecond)
	}
	r.mu.Lock()
	r.reports = append(r.reports, kind+" "+track.Path)
	r.mu.Unlock()
}

func (r *orderReporter) CanReportPlayback(playlist.Track) bool { return true }

func (r *orderReporter) ReportNowPlaying(track playlist.Track, _ time.Duration, _ bool) error {
	r.record("now-playing", track)
	return nil
}

func (r *orderReporter) ReportScrobble(track playlist.Track, _, _ time.Duration, _ bool) error {
	r.record("scrobble", track)
	return nil
}

func (r *orderReporter) ReportProgress(track playlist.Track, _ time.Duration) error {
	r.record("progress", track)
	return nil
}

// The provider gets the now-playing, progress and scrobble reports of a
// track, and the next now-playing report, in the order the Model sent them.
func TestPlaybackReportsKeepOrder(t *testing.T) {
	a := playlist.Track{Title: "A", Path: "a.mp3", DurationSecs: 60}
	b := playlist.Track{Title: "B", Path: "b.mp3", DurationSecs: 60}
	reporter := &orderReporter{}
	m := Model{
		player:             &playbackFakeEngine{playing: true, position: 40 * time.Second, duration: time.Minute},
		playlist:           playlist.New(),
		providers:          []provider.Entry{{Key: "p", Name: "P", Provider: reporter}},
		playingTrack:       a,
		playingTrackActive: true,
	}
	m.nowPlaying(a)
	m.tickProgressReport(time.Now())
	m.maybeScrobble(a, 40*time.Second, time.Minute)
	m.nowPlaying(b)
	m.reports.waitIdle(t)

	want := []string{"now-playing a.mp3", "progress a.mp3", "scrobble a.mp3", "now-playing b.mp3"}
	reporter.mu.Lock()
	defer reporter.mu.Unlock()
	if !slices.Equal(reporter.reports, want) {
		t.Fatalf("reports = %v, want %v", reporter.reports, want)
	}
}
