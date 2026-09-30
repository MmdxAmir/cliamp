package model

import (
	"testing"
	"time"

	"github.com/bjarneo/cliamp/luaplugin"
	"github.com/bjarneo/cliamp/playlist"
)

// positionCountingEngine counts the reads of the engine position.
type positionCountingEngine struct {
	playbackFakeEngine
	positionReads int
}

func (e *positionCountingEngine) Position() time.Duration {
	e.positionReads++
	return e.playbackFakeEngine.Position()
}

func (e *positionCountingEngine) PositionAndDuration() (time.Duration, time.Duration) {
	e.positionReads++
	return e.playbackFakeEngine.PositionAndDuration()
}

// notifyPlaybackChange builds the playback state, which reads the engine
// position, only when the media controls or a playback.state hook get it.
// A hook for another event does not count.
func TestNotifyPlaybackChangeBuildsOnlyWhenNeeded(t *testing.T) {
	tests := []struct {
		name      string
		event     string // the plugin hook, if any
		notifier  bool
		wantReads bool
	}{
		{name: "a playback.state hook", event: luaplugin.EventPlaybackState, wantReads: true},
		{name: "a track.change hook", event: luaplugin.EventTrackChange},
		{name: "media controls", notifier: true, wantReads: true},
		{name: "nothing listens"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine := &positionCountingEngine{}
			m := Model{player: engine, playlist: playlist.New()}
			if tt.event != "" {
				m.luaMgr, _, _ = newEventTestPlugin(t, tt.event)
			}
			if tt.notifier {
				m.notifier = &fakeNotifier{}
			}
			m.notifyPlaybackChange()
			if got := engine.positionReads > 0; got != tt.wantReads {
				t.Fatalf("position read = %v, want %v", got, tt.wantReads)
			}
		})
	}
}
