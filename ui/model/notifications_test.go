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

// notifyPlugins builds the playback.state payload, which reads the engine
// position, only when a plugin listens for playback.state. A hook for
// another event does not count.
func TestNotifyPluginsBuildsOnlyForItsHook(t *testing.T) {
	tests := []struct {
		event     string
		wantReads bool
	}{
		{luaplugin.EventPlaybackState, true},
		{luaplugin.EventTrackChange, false},
	}
	for _, tt := range tests {
		t.Run(tt.event, func(t *testing.T) {
			mgr, _, _ := newEventTestPlugin(t, tt.event)
			engine := &positionCountingEngine{}
			m := Model{player: engine, playlist: playlist.New(), luaMgr: mgr}
			m.notifyPlugins()
			if got := engine.positionReads > 0; got != tt.wantReads {
				t.Fatalf("position read = %v, want %v", got, tt.wantReads)
			}
		})
	}
}
