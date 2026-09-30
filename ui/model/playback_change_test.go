package model

import (
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/internal/playback"
	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// countingReporter counts the scrobbles of each track.
type countingReporter struct {
	plainProv
	mu        sync.Mutex
	scrobbles []string
}

func (r *countingReporter) CanReportPlayback(playlist.Track) bool { return true }

func (r *countingReporter) ReportNowPlaying(playlist.Track, time.Duration, bool) error { return nil }

func (r *countingReporter) ReportScrobble(track playlist.Track, _, _ time.Duration, _ bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.scrobbles = append(r.scrobbles, track.Path)
	return nil
}

func (r *countingReporter) scrobbled() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.scrobbles...)
}

// playbackChange is the Model and fakes that one playback-changing message
// runs against.
type playbackChange struct {
	m        Model
	engine   *playbackFakeEngine
	reporter *countingReporter
}

// newPlaybackChange returns a Model that plays a.mp3 at 150 of 180 seconds,
// past the scrobble threshold, with b.mp3 and c.mp3 after it.
func newPlaybackChange() playbackChange {
	engine := &playbackFakeEngine{playing: true, position: 150 * time.Second, duration: 180 * time.Second}
	reporter := &countingReporter{}
	m := newColumnTestModel(100, 30)
	m.playlist.Replace([]playlist.Track{
		{Title: "A", Path: "a.mp3", DurationSecs: 180},
		{Title: "B", Path: "b.mp3", DurationSecs: 180},
		{Title: "C", Path: "c.mp3", DurationSecs: 180},
	})
	m.playlist.SetIndex(0)
	m.player = engine
	m.providers = []provider.Entry{{Key: "p", Name: "P", Provider: reporter}}
	m.playingTrack, m.playingTrackActive, m.playingTrackStarted = playlist.Track{Title: "A", Path: "a.mp3", DurationSecs: 180}, true, true
	return playbackChange{m: m, engine: engine, reporter: reporter}
}

// Each message that leaves the track that plays scrobbles it once. A
// message that keeps the track does not scrobble it.
func TestPlaybackChangesScrobbleOnce(t *testing.T) {
	key := func(text string) tea.Msg { return tea.KeyPressMsg{Text: text} }
	tests := []struct {
		name  string
		setup func(c *playbackChange)
		msg   func(t *testing.T) tea.Msg
		// scrobble is true when the message leaves a.mp3.
		scrobble bool
	}{
		{name: "next key", msg: func(*testing.T) tea.Msg { return key(">") }, scrobble: true},
		{name: "next message", msg: func(*testing.T) tea.Msg { return playback.NextMsg{} }, scrobble: true},
		{name: "V2 next", msg: func(t *testing.T) tea.Msg { return v2Request(t, "next", ipc.Request{}) }, scrobble: true},
		{name: "prev key restarts a stream", msg: func(*testing.T) tea.Msg { return key("<") }, scrobble: true},
		{name: "prev key rewinds a file", setup: func(c *playbackChange) { c.engine.seekable = true },
			msg: func(*testing.T) tea.Msg { return key("<") }, scrobble: true},
		{name: "stop key", msg: func(*testing.T) tea.Msg { return key("s") }, scrobble: true},
		{name: "stop message", msg: func(*testing.T) tea.Msg { return playback.StopMsg{} }, scrobble: true},
		{name: "V2 stop", msg: func(t *testing.T) tea.Msg { return v2Request(t, "stop", ipc.Request{}) }, scrobble: true},
		{name: "V2 queue.play", msg: func(t *testing.T) tea.Msg { return v2Request(t, "queue.play", ipc.Request{Index: 2}) }, scrobble: true},
		{name: "V2 queue.clear", msg: func(t *testing.T) tea.Msg { return v2Request(t, "queue.clear", ipc.Request{}) }, scrobble: true},
		{name: "plugin jump", msg: func(*testing.T) tea.Msg { return PluginQueueMsg{Op: "jump", Index: 2} }, scrobble: true},
		{name: "enter on a row", setup: func(c *playbackChange) { c.m.plCursor = 2 },
			msg: func(*testing.T) tea.Msg { return tea.KeyPressMsg{Code: tea.KeyEnter} }, scrobble: true},
		{name: "url.load with play", msg: func(*testing.T) tea.Msg {
			return ipcURLLoadResult{
				request: ipcURLRequest{Play: true, Reply: make(chan ipc.Response, 1)},
				tracks:  []playlist.Track{{Title: "D", Path: "d.mp3"}},
			}
		}, scrobble: true},
		{name: "provider.load", msg: func(*testing.T) tea.Msg {
			return ipcProviderLoadResult{
				request: ipcLibraryRequest{Reply: make(chan ipc.Response, 1)},
				tracks:  []playlist.Track{{Title: "D", Path: "d.mp3"}},
			}
		}, scrobble: true},
		{name: "a file browser replace", msg: func(*testing.T) tea.Msg {
			return fbTracksResolvedMsg{tracks: []playlist.Track{{Title: "D", Path: "d.mp3"}}, replace: true}
		}, scrobble: true},
		{name: "a gapless advance", setup: func(c *playbackChange) {
			c.engine.gaplessAdvanced = true
			c.engine.lastPlayedDuration = 180 * time.Second
		}, msg: func(*testing.T) tea.Msg { return tickMsg(time.Now()) }, scrobble: true},
		{name: "a drained track", setup: func(c *playbackChange) { c.engine.drained = true },
			msg: func(*testing.T) tea.Msg { return tickMsg(time.Now()) }, scrobble: true},
		{name: "a drained last track", setup: func(c *playbackChange) {
			c.engine.drained = true
			c.m.playlist.SetIndex(2)
		}, msg: func(*testing.T) tea.Msg { return tickMsg(time.Now()) }, scrobble: true},
		{name: "pause", msg: func(*testing.T) tea.Msg { return playback.PauseMsg{} }},
		{name: "seek", setup: func(c *playbackChange) { c.engine.seekable = true },
			msg: func(*testing.T) tea.Msg { return playback.SetPositionMsg{Position: 10 * time.Second} }},
		{name: "volume", msg: func(*testing.T) tea.Msg { return playback.SetVolumeMsg{VolumeDB: -6} }},
		{name: "a tick", msg: func(*testing.T) tea.Msg { return tickMsg(time.Now()) }},
		{name: "a track that never started", setup: func(c *playbackChange) { c.m.playingTrackStarted = false },
			msg: func(*testing.T) tea.Msg { return key(">") }},
		{name: "a track under half played", setup: func(c *playbackChange) { c.engine.position = 60 * time.Second },
			msg: func(*testing.T) tea.Msg { return key(">") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newPlaybackChange()
			if tt.setup != nil {
				tt.setup(&c)
			}
			next, _ := c.m.Update(tt.msg(t))
			m := next.(Model)
			if m.reports != nil {
				m.reports.waitIdle(t)
			}
			want := 0
			if tt.scrobble {
				want = 1
			}
			got := c.reporter.scrobbled()
			if len(got) != want {
				t.Fatalf("scrobbles = %v, want %d of a.mp3", got, want)
			}
			if want == 1 && got[0] != "a.mp3" {
				t.Fatalf("scrobbles = %v, want a.mp3", got)
			}
		})
	}
}

// A track that is left once scrobbles once, also when a stop or a start of
// the next track follows, and a replay after a rewind can scrobble again.
func TestLeaveTrackReportsEachStartOnce(t *testing.T) {
	c := newPlaybackChange()
	m := c.m
	m.leaveTrack(180*time.Second, 180*time.Second)
	m.stopPlayback()
	m.leaveTrack(180*time.Second, 180*time.Second)
	if m.reports != nil {
		m.reports.waitIdle(t)
	}
	if got := c.reporter.scrobbled(); len(got) != 1 {
		t.Fatalf("scrobbles = %v, want one", got)
	}

	c = newPlaybackChange()
	c.engine.seekable = true
	m = c.m
	m.prevTrack()
	c.engine.position = 170 * time.Second
	m.nextTrack()
	if m.reports != nil {
		m.reports.waitIdle(t)
	}
	if got := c.reporter.scrobbled(); len(got) != 2 {
		t.Fatalf("scrobbles = %v, want one for the play and one for the replay", got)
	}
}
