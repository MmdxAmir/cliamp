package main

import (
	"reflect"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/internal/playback"
	"github.com/bjarneo/cliamp/luaplugin"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui/model"
)

// Each Lua control reaches the Model as a message, so the Update loop makes
// every player change.
func TestLuaControlProviderSendsMessages(t *testing.T) {
	bands := [10]float64{1, 2}
	tests := []struct {
		name string
		call func(luaplugin.ControlProvider)
		want tea.Msg
	}{
		{"set_volume", func(c luaplugin.ControlProvider) { c.SetVolume(-40) }, playback.SetVolumeMsg{VolumeDB: -40}},
		{"set_speed", func(c luaplugin.ControlProvider) { c.SetSpeed(1.5) }, playback.SetSpeedMsg{Ratio: 1.5}},
		{"toggle_mono", func(c luaplugin.ControlProvider) { c.ToggleMono() }, playback.ToggleMonoMsg{}},
		{"set_eq_band", func(c luaplugin.ControlProvider) { c.SetEQBand(2, 3) }, model.SetEQBandMsg{Band: 2, Gain: 3}},
		{"set_eq_preset", func(c luaplugin.ControlProvider) { c.SetEQPreset("Rock", &bands) }, model.SetEQPresetMsg{Name: "Rock", Bands: &bands}},
		{"play_pause", func(c luaplugin.ControlProvider) { c.TogglePause() }, playback.PlayPauseMsg{}},
		{"stop", func(c luaplugin.ControlProvider) { c.Stop() }, playback.StopMsg{}},
		{"seek", func(c luaplugin.ControlProvider) { c.Seek(1.5) }, playback.SeekMsg{Offset: 1500 * time.Millisecond}},
		{"next", func(c luaplugin.ControlProvider) { c.Next() }, playback.NextMsg{}},
		{"prev", func(c luaplugin.ControlProvider) { c.Prev() }, playback.PrevMsg{}},
		{"queue.add path", func(c luaplugin.ControlProvider) { c.QueueAdd("/a.mp3") }, model.PluginQueueMsg{Op: "add", Path: "/a.mp3"}},
		{
			"queue.add track",
			func(c luaplugin.ControlProvider) {
				c.QueueAddTrack(luaplugin.QueueTrack{Path: "/a.mp3", Title: "A", Artist: "B", Album: "C", Genre: "D", Year: 1999, Duration: 60, Stream: true})
			},
			model.PluginQueueMsg{Op: "add_track", Track: playlist.Track{Path: "/a.mp3", Title: "A", Artist: "B", Album: "C", Genre: "D", Year: 1999, DurationSecs: 60, Stream: true}},
		},
		{"queue.jump", func(c luaplugin.ControlProvider) { c.QueueJump(3) }, model.PluginQueueMsg{Op: "jump", Index: 3}},
		{"queue.remove", func(c luaplugin.ControlProvider) { c.QueueRemove(1) }, model.PluginQueueMsg{Op: "remove", Index: 1}},
		{"queue.move", func(c luaplugin.ControlProvider) { c.QueueMove(1, 4) }, model.PluginQueueMsg{Op: "move", Index: 1, To: 4}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []tea.Msg
			tt.call(luaControlProvider(func(msg tea.Msg) { got = append(got, msg) }))
			if want := []tea.Msg{tt.want}; !reflect.DeepEqual(got, want) {
				t.Fatalf("sent %#v, want %#v", got, want)
			}
		})
	}
}
