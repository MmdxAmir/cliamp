package model

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/bjarneo/cliamp/internal/playback"
	"github.com/bjarneo/cliamp/luaplugin"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui"
)

// pluginStateTestMsg is a message that Update ignores. It runs the deferred
// block of update, which publishes the plugin state.
type pluginStateTestMsg struct{}

func newPluginStateModel(engine *playbackFakeEngine, tracks ...playlist.Track) Model {
	pl := playlist.New()
	pl.Replace(tracks)
	pl.SetIndex(0)
	return Model{
		player:      engine,
		playlist:    pl,
		provider:    commandsTestProvider{name: "Test"},
		vis:         ui.NewVisualizer(float64(engine.SampleRate())),
		pluginState: new(atomic.Pointer[PluginState]),
	}
}

// Lua plugins read the track that plays, as the events and the media
// controls report it, and not the current row of the playlist.
func TestPluginStateReportsThePlayingTrack(t *testing.T) {
	tests := []struct {
		name      string
		run       func(t *testing.T) Model
		wantTrack luaplugin.Track
		wantState func(t *testing.T, s PluginState)
	}{
		{
			name: "a list replaces the queue during playback",
			run: func(t *testing.T) Model {
				m := newPluginStateModel(&playbackFakeEngine{playing: true},
					playlist.Track{Title: "Old", Artist: "Band", Path: "old.mp3", DurationSecs: 180})
				m.requests.tracks = 1
				updated, _ := m.Update(tracksLoadedMsg{
					tracks: []playlist.Track{
						{Title: "New 1", Path: "new1.mp3", DurationSecs: 180},
						{Title: "New 2", Path: "new2.mp3", DurationSecs: 180},
					},
					providerName: "Test",
					gen:          1,
				})
				m = updated.(Model)
				if !m.playbackDetached {
					t.Fatal("playbackDetached = false, want the old track to keep playing")
				}
				return m
			},
			wantTrack: luaplugin.Track{Title: "Old", Artist: "Band", Path: "old.mp3", Duration: 180},
			wantState: func(t *testing.T, s PluginState) {
				if s.Status != "playing" || s.Count != 2 || s.Index != 0 {
					t.Errorf("status, count, index = %q, %d, %d; want playing, 2, 0", s.Status, s.Count, s.Index)
				}
				if len(s.Queue) != 2 || s.Queue[0].Title != "New 1" || s.Queue[1].Title != "New 2" {
					t.Errorf("queue = %+v, want the new list", s.Queue)
				}
			},
		},
		{
			name: "a radio stream with a stream title",
			run: func(t *testing.T) Model {
				station := playlist.Track{Title: "Station", Path: "https://radio.example.com/live", Stream: true}
				m := newPluginStateModel(&playbackFakeEngine{playing: true, live: true}, station)
				m.setPlaybackTrack(station)
				m.streamTitle = "Artist - Song"
				updated, _ := m.Update(pluginStateTestMsg{})
				return updated.(Model)
			},
			wantTrack: luaplugin.Track{Title: "Song", Artist: "Artist", Path: "https://radio.example.com/live", Stream: true, Live: true},
		},
		{
			name: "stopped",
			run: func(t *testing.T) Model {
				m := newPluginStateModel(&playbackFakeEngine{},
					playlist.Track{Title: "A", Path: "a.mp3", Year: 2001, TrackNumber: 3},
					playlist.Track{Title: "B", Path: "b.mp3"})
				m.playlist.Queue(1)
				updated, _ := m.Update(pluginStateTestMsg{})
				return updated.(Model)
			},
			wantTrack: luaplugin.Track{Title: "A", Path: "a.mp3", Year: 2001, Number: 3},
			wantState: func(t *testing.T, s PluginState) {
				if s.Status != "stopped" || !s.HasNext {
					t.Errorf("status, has next = %q, %v; want stopped, true", s.Status, s.HasNext)
				}
				if len(s.Queue) != 2 || s.Queue[0].Queued || !s.Queue[1].Queued || s.Queue[1].Index != 1 {
					t.Errorf("queue = %+v, want B queued at index 1", s.Queue)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := tt.run(t)
			got := m.PluginStateLoader()()
			if got.Track != tt.wantTrack {
				t.Errorf("track = %+v, want %+v", got.Track, tt.wantTrack)
			}
			if tt.wantState != nil {
				tt.wantState(t, got)
			}
		})
	}
}

// With no plugin loaded, the Model publishes nothing, and a read returns a
// stopped state at normal speed.
func TestPluginStateLoaderWithoutPlugins(t *testing.T) {
	m := Model{player: &playbackFakeEngine{playing: true}, playlist: playlist.New()}
	updated, _ := m.Update(pluginStateTestMsg{})
	m = updated.(Model)
	got := m.PluginStateLoader()()
	if got.Status != "stopped" || got.Speed != 1 {
		t.Fatalf("PluginStateLoader()() = %+v, want stopped at speed 1", got)
	}
}

// The states share the queue until the playlist changes, so an Update does
// not copy a long playlist again.
func TestPluginStateRebuildsQueueOnPlaylistChange(t *testing.T) {
	m := newPluginStateModel(&playbackFakeEngine{},
		playlist.Track{Title: "A", Path: "a.mp3"},
		playlist.Track{Title: "B", Path: "b.mp3"})
	m.publishPluginState()
	load := m.PluginStateLoader()
	first := load().Queue
	m.publishPluginState()
	if second := load().Queue; &second[0] != &first[0] {
		t.Fatal("the queue was built again with no playlist change")
	}
	m.playlist.Add(playlist.Track{Title: "C", Path: "c.mp3"})
	m.publishPluginState()
	if third := load().Queue; len(third) != 3 || third[2].Title != "C" {
		t.Fatalf("queue = %+v, want the added track", third)
	}
}

// Plugins read the state on their own goroutines while Update publishes it.
// Run with -race.
func TestPluginStateConcurrentReads(t *testing.T) {
	m := newPluginStateModel(&playbackFakeEngine{playing: true},
		playlist.Track{Title: "A", Path: "a.mp3"})
	// main.go takes the loader from the Model that New returned. The
	// copies that Update returns publish to its store.
	load := m.PluginStateLoader()
	var stop atomic.Bool
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				s := load()
				for _, e := range s.Queue {
					_ = e.Title
				}
				_ = s.Track.Title
			}
		}()
	}
	for i := range 200 {
		m.playlist.Add(playlist.Track{Title: "T", Path: "t.mp3"})
		msgs := []any{playback.SetSpeedMsg{Ratio: 1 + float64(i%4)/4}, playback.ToggleMonoMsg{}, pluginStateTestMsg{}}
		updated, _ := m.Update(msgs[i%len(msgs)])
		m = updated.(Model)
	}
	stop.Store(true)
	wg.Wait()
	if got := load().Count; got != 201 {
		t.Fatalf("count = %d, want 201", got)
	}
}
