package model

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// armedModel plays a.mp3 one second before its end, with b.mp3 next and
// already armed, then c.mp3.
func armedModel() (Model, *playbackFakeEngine) {
	player := &playbackFakeEngine{playing: true, duration: 180 * time.Second, position: 179 * time.Second, hasPreload: true}
	p := playlist.New()
	p.Replace([]playlist.Track{
		{Title: "A", Path: "a.mp3", DurationSecs: 180},
		{Title: "B", Path: "b.mp3", DurationSecs: 180},
		{Title: "C", Path: "c.mp3", DurationSecs: 180},
	})
	p.SetIndex(0)
	m := Model{
		player:      player,
		playlist:    p,
		focus:       focusPlaylist,
		configSaver: &recordingSaver{},
		preloadFor:  "b.mp3",
	}
	return m, player
}

// Every way of changing the next track drops the armed b.mp3, and the next
// tick arms the new next track instead.
func TestUpdateDropsStalePreload(t *testing.T) {
	for _, tc := range []struct {
		name     string
		cursor   int
		msg      tea.Msg
		wantNext string
	}{
		{name: "IPC repeat one", msg: v2Request(t, "repeat", ipc.Request{Name: "one"}), wantNext: "a.mp3"},
		{name: "plugin swap next away", msg: PluginQueueMsg{Op: "move", Index: 1, To: 2}, wantNext: "c.mp3"},
		{name: "plugin remove next", msg: PluginQueueMsg{Op: "remove", Index: 1}, wantNext: "c.mp3"},
		{name: "TUI move next down", cursor: 1, msg: tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModShift}, wantNext: "c.mp3"},
		{name: "TUI delete next", cursor: 1, msg: tea.KeyPressMsg{Text: "x", Code: 'x'}, wantNext: "c.mp3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, player := armedModel()
			m.plCursor = tc.cursor

			next, _ := m.Update(tc.msg)
			m = next.(Model)
			if player.clearPreloadCalls == 0 || (m.preloadFor == "b.mp3" && (m.preloading || player.hasPreload)) {
				t.Fatalf("b.mp3 still armed after the next track changed (ClearPreload %d)", player.clearPreloadCalls)
			}

			next, _ = m.Update(tickMsg(time.Now()))
			m = next.(Model)
			if !m.preloading || m.preloadFor != tc.wantNext {
				t.Fatalf("after a tick: preloading %v for %q, want %s", m.preloading, m.preloadFor, tc.wantNext)
			}
		})
	}
}

// Appending over IPC while the last track plays with repeat-all puts the new
// track next instead of the first one.
func TestUpdateDropsStalePreloadOnRepeatAllAppend(t *testing.T) {
	m, player := armedModel()
	m.playlist.SetIndex(2)
	m.playlist.SetRepeat(playlist.RepeatAll)
	m.preloadFor = "a.mp3"

	if response := runV2(t, &m, "queue", ipc.Request{Path: "d.mp3"}); !response.OK {
		t.Fatalf("queue response = %+v", response)
	}
	next, _ := m.Update(tickMsg(time.Now()))
	if m = next.(Model); player.clearPreloadCalls != 1 || m.preloadFor != "d.mp3" {
		t.Fatalf("ClearPreload %d, preloading %q; want a.mp3 dropped and d.mp3 armed", player.clearPreloadCalls, m.preloadFor)
	}
}

// A message that leaves b.mp3 next keeps it armed.
func TestUpdateKeepsValidPreload(t *testing.T) {
	for _, tc := range []struct {
		name string
		msg  tea.Msg
	}{
		{name: "status", msg: ShowStatusMsg{}},
		{name: "plugin remove below next", msg: PluginQueueMsg{Op: "remove", Index: 2}},
		{name: "tick", msg: tickMsg(time.Now())},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, player := armedModel()
			next, _ := m.Update(tc.msg)
			if player.clearPreloadCalls != 0 || !player.hasPreload || next.(Model).preloadFor != "b.mp3" {
				t.Fatalf("still-valid b.mp3 was dropped (ClearPreload %d)", player.clearPreloadCalls)
			}
		})
	}
}

// When nothing plays after the current track any more, the preload is
// dropped and nothing replaces it.
func TestUpdateDropsPreloadWhenNothingPlaysNext(t *testing.T) {
	m, player := armedModel()
	for range 2 {
		next, _ := m.Update(PluginQueueMsg{Op: "remove", Index: 1})
		m = next.(Model)
	}
	next, _ := m.Update(tickMsg(time.Now()))
	m = next.(Model)
	if player.clearPreloadCalls != 1 || player.hasPreload || m.preloading {
		t.Fatalf("ClearPreload %d, armed %v, preloading %v; want b.mp3 dropped and nothing armed", player.clearPreloadCalls, player.hasPreload, m.preloading)
	}
}

// A preload still loading is dropped as well, and its late completion does not
// clear the in-flight flag of the one that replaces it.
func TestUpdateDropsStaleInFlightPreload(t *testing.T) {
	m, player := armedModel()
	player.hasPreload, m.preloading = false, true
	stale := m.requests.preload

	next, _ := m.Update(PluginQueueMsg{Op: "remove", Index: 1})
	m = next.(Model)
	if player.clearPreloadCalls != 1 || m.preloading {
		t.Fatalf("in-flight b.mp3 kept (ClearPreload %d, preloading %v)", player.clearPreloadCalls, m.preloading)
	}
	next, _ = m.Update(tickMsg(time.Now()))
	next, _ = next.(Model).Update(streamPreloadedMsg{path: "b.mp3", gen: stale})
	if m = next.(Model); !m.preloading || m.preloadFor != "c.mp3" {
		t.Fatalf("preloading %v for %q, want c.mp3 still in flight", m.preloading, m.preloadFor)
	}
}

// A shuffle or repeat change over IPC re-arms the preload for the new next
// track at once, and a failed config save shows in the TUI, as the keys do.
func TestV2ModeChangeRearmsPreloadAndReportsSaveError(t *testing.T) {
	for _, tc := range []struct {
		op, name string
	}{
		{op: "shuffle", name: "on"},
		{op: "repeat", name: "one"},
	} {
		t.Run(tc.op, func(t *testing.T) {
			m, player := armedModel()
			m.configSaver = &recordingSaver{err: errors.New("disk full")}

			if response := runV2(t, &m, tc.op, ipc.Request{Name: tc.name}); !response.OK {
				t.Fatalf("response = %+v", response)
			}
			next, ok := m.playlist.PeekNext()
			if !ok || player.clearPreloadCalls == 0 || !m.preloading || m.preloadFor != next.Path {
				t.Fatalf("preloading %v for %q after ClearPreload %d, want %q armed at once", m.preloading, m.preloadFor, player.clearPreloadCalls, next.Path)
			}
			if !strings.Contains(m.status.text, "Config save failed: disk full") {
				t.Fatalf("status = %q, want the config save error", m.status.text)
			}
		})
	}
}

// verbReporter records the tracks that a verb scrobbles.
type verbReporter struct {
	plainProv
	scrobbles chan string
}

func (r *verbReporter) CanReportPlayback(playlist.Track) bool { return true }

func (r *verbReporter) ReportNowPlaying(playlist.Track, time.Duration, bool) error { return nil }

func (r *verbReporter) ReportScrobble(track playlist.Track, _, _ time.Duration, _ bool) error {
	r.scrobbles <- track.Path
	return nil
}

// verbState is the part of the Model that an action verb changes.
type verbState struct {
	index, cursor int
	volume        float64
	shuffle       bool
	repeat        playlist.RepeatMode
	vis           string
	saved         string
	preload       string
	clears        int
	notified      bool
	scrobbled     string
	pluginState   bool
}

// Each action verb reaches the same end state from each entry point: the
// main keys, the full-screen visualizer keys, a playback message from media
// controls or Lua, and a V2 request.
func TestVerbEntryPointsReachTheSameState(t *testing.T) {
	key := func(text string) tea.Msg { return tea.KeyPressMsg{Text: text} }
	fullVis := func(text string) tea.Msg { return fullVisKey{tea.KeyPressMsg{Text: text}} }
	for _, verb := range []struct {
		name string
		// skips is true for a verb that leaves the track, so it scrobbles.
		// notifies is true when the verb tells the media controls and the
		// playback.state plugin hook.
		skips, notifies bool
		entries         map[string]tea.Msg
		// done reports whether the key state shows the verb.
		done func(verbState) bool
	}{
		{name: "skipNext", skips: true, notifies: true, entries: map[string]tea.Msg{
			"key": key(">"), "full-screen key": fullVis(">"),
		}, done: func(s verbState) bool { return s.index == 1 && s.cursor == 1 && s.scrobbled == "a.mp3" }},
		{name: "skipPrev", skips: true, notifies: true, entries: map[string]tea.Msg{
			"key": key("<"), "full-screen key": fullVis("<"),
		}, done: func(s verbState) bool { return s.index == 0 && s.scrobbled == "a.mp3" }},
		{name: "setShuffle", entries: map[string]tea.Msg{
			"key": key("z"),
		}, done: func(s verbState) bool { return s.shuffle && s.saved == "map[shuffle:true]" && s.clears == 1 }},
		{name: "setRepeat", entries: map[string]tea.Msg{
			"key": key("r"),
		}, done: func(s verbState) bool {
			return s.repeat == playlist.RepeatAll && s.saved == `map[repeat:"All"]` && s.clears == 1
		}},
		{name: "cycleVisualizer", entries: map[string]tea.Msg{
			"key": key("v"), "full-screen key": fullVis("v"),
		}, done: func(s verbState) bool { return s.vis == "BarsDot" && s.saved == `map[visualizer:"BarsDot"]` }},
		{name: "adjustVolume", notifies: true, entries: map[string]tea.Msg{
			"key": key("+"), "full-screen key": fullVis("+"),
		}, done: func(s verbState) bool { return s.volume == 1 }},
	} {
		t.Run(verb.name, func(t *testing.T) {
			states := map[string]verbState{}
			for entry, msg := range verb.entries {
				states[entry] = runVerbEntry(t, msg, verb.skips, verb.notifies)
			}
			want := states["key"]
			if !verb.done(want) {
				t.Fatalf("key state = %+v, want the verb done", want)
			}
			if verb.notifies && (!want.notified || !want.pluginState) {
				t.Fatalf("key state = %+v, want the media controls and the playback.state hook told", want)
			}
			for entry, got := range states {
				if got != want {
					t.Errorf("%s: state = %+v, want the key state %+v", entry, got, want)
				}
			}
		})
	}
}

// fullVisKey is a key press that runVerbEntry sends while the full-screen
// visualizer is open.
type fullVisKey struct{ tea.KeyPressMsg }

// runVerbEntry sends msg to a Model that plays a.mp3 near its end, with
// b.mp3 armed next, and returns the state that msg leaves.
func runVerbEntry(t *testing.T, msg tea.Msg, skips, notifies bool) verbState {
	t.Helper()
	mgr, messages, _ := newReportTestPlugin(t, "playback.state", `ev.status`)
	engine := &settingsFocusEngine{playbackFakeEngine: playbackFakeEngine{playing: true, duration: 180 * time.Second, position: 179 * time.Second, hasPreload: true}}
	reporter := &verbReporter{scrobbles: make(chan string, 4)}
	notifier := &fakeNotifier{}
	saver := &recordingSaver{}
	m := newColumnTestModel(100, 30)
	m.playlist.Replace([]playlist.Track{
		{Title: "A", Path: "a.mp3", DurationSecs: 180},
		{Title: "B", Path: "b.mp3", DurationSecs: 180},
		{Title: "C", Path: "c.mp3", DurationSecs: 180},
	})
	m.playlist.SetIndex(0)
	m.player, m.notifier, m.configSaver, m.luaMgr = engine, notifier, saver, mgr
	m.providers = []provider.Entry{{Key: "p", Name: "P", Provider: reporter}}
	m.playingTrack, m.playingTrackActive, m.playingTrackStarted = playlist.Track{Title: "A", Path: "a.mp3", DurationSecs: 180}, true, true
	m.preloadFor = "b.mp3"

	switch msg := msg.(type) {
	case fullVisKey:
		m.fullVis = true
		m.recomputeLayout()
		next, _ := m.Update(msg.KeyPressMsg)
		m = next.(Model)
	case V2RequestMsg:
		next, _ := m.Update(msg)
		m = next.(Model)
		if job, _ := msg.Jobs.Get(msg.JobID); job.State != ipc.JobSucceeded {
			t.Fatalf("V2 job = %+v, want success", job)
		}
	default:
		next, _ := m.Update(msg)
		m = next.(Model)
	}

	state := verbState{
		index:    m.playlist.Index(),
		cursor:   m.plCursor,
		volume:   engine.volume,
		shuffle:  m.playlist.Shuffled(),
		repeat:   m.playlist.Repeat(),
		vis:      m.vis.ModeName(),
		saved:    fmt.Sprint(saver.saved),
		clears:   engine.clearPreloadCalls,
		notified: len(notifier.updates) > 0,
	}
	if m.preloading || engine.hasPreload {
		state.preload = m.preloadFor
	}
	if skips {
		select {
		case state.scrobbled = <-reporter.scrobbles:
		case <-time.After(2 * time.Second):
		}
	}
	if notifies {
		select {
		case <-messages:
			state.pluginState = true
		case <-time.After(2 * time.Second):
		}
	}
	return state
}
