package main

import (
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/internal/playback"
	"github.com/bjarneo/cliamp/luaplugin"
	"github.com/bjarneo/cliamp/player"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui/model"
)

// luaStateProvider gives Lua plugins read access to the player and the
// playlist.
func luaStateProvider(p *player.Player, pl *playlist.Playlist) luaplugin.StateProvider {
	return luaplugin.StateProvider{
		PlayerState: func() string {
			if !p.IsPlaying() {
				return "stopped"
			}
			if p.IsPaused() {
				return "paused"
			}
			return "playing"
		},
		Position:   func() float64 { return p.Position().Seconds() },
		Duration:   func() float64 { return p.Duration().Seconds() },
		Volume:     func() float64 { return p.Volume() },
		Speed:      func() float64 { return p.Speed() },
		Mono:       func() bool { return p.Mono() },
		RepeatMode: func() string { return pl.Repeat().String() },
		Shuffle:    func() bool { return pl.Shuffled() },
		EQBands:    func() [10]float64 { return p.EQBands() },
		CurrentTrack: func() luaplugin.Track {
			t, _ := pl.Current()
			track := luaTrack(t)
			track.Live = model.PlaysLive(t, p)
			return track
		},
		PlaylistCount: func() int { return pl.Len() },
		CurrentIndex:  func() int { return pl.Index() },
		HasNext:       pl.HasNext,
		QueueList: func() []luaplugin.QueueEntry {
			tracks := pl.Tracks()
			out := make([]luaplugin.QueueEntry, len(tracks))
			for i, t := range tracks {
				out[i] = luaplugin.QueueEntry{
					Track:  luaTrack(t),
					Index:  i,
					Queued: pl.QueuePosition(i) > 0, // 1-based; 0 means not queued
				}
			}
			return out
		},
	}
}

// luaControlProvider gives Lua plugins control of playback. Each control
// sends a message to the Model through send, so the Update loop makes every
// player change, tells the media controls and saves the settings.
func luaControlProvider(send func(tea.Msg)) luaplugin.ControlProvider {
	return luaplugin.ControlProvider{
		SetVolume:   func(db float64) { send(playback.SetVolumeMsg{VolumeDB: db}) },
		SetSpeed:    func(ratio float64) { send(playback.SetSpeedMsg{Ratio: ratio}) },
		SetEQBand:   func(band int, db float64) { send(model.SetEQBandMsg{Band: band, Gain: db}) },
		ToggleMono:  func() { send(playback.ToggleMonoMsg{}) },
		TogglePause: func() { send(playback.PlayPauseMsg{}) },
		Stop:        func() { send(playback.StopMsg{}) },
		Seek: func(secs float64) {
			send(playback.SeekMsg{Offset: time.Duration(secs * float64(time.Second))})
		},
		SetEQPreset: func(name string, bands *[10]float64) {
			send(model.SetEQPresetMsg{Name: name, Bands: bands})
		},
		Next: func() { send(playback.NextMsg{}) },
		Prev: func() { send(playback.PrevMsg{}) },
		QueueAdd: func(path string) {
			send(model.PluginQueueMsg{Op: "add", Path: path})
		},
		QueueAddTrack: func(t luaplugin.Track) {
			send(model.PluginQueueMsg{Op: "add_track", Track: playlist.Track{
				Path: t.Path, Title: t.Title, Artist: t.Artist, Album: t.Album,
				Genre: t.Genre, Year: t.Year, DurationSecs: t.Duration, Stream: t.Stream,
			}})
		},
		QueueJump: func(index int) {
			send(model.PluginQueueMsg{Op: "jump", Index: index})
		},
		QueueRemove: func(index int) {
			send(model.PluginQueueMsg{Op: "remove", Index: index})
		},
		QueueMove: func(from, to int) {
			send(model.PluginQueueMsg{Op: "move", Index: from, To: to})
		},
	}
}

// luaTrack returns t as Lua plugins see it, with no live flag.
func luaTrack(t playlist.Track) luaplugin.Track {
	return luaplugin.Track{
		Title: t.Title, Artist: t.Artist, Album: t.Album, Genre: t.Genre, Path: t.Path,
		Year: t.Year, Number: t.TrackNumber, Duration: t.DurationSecs, Stream: t.Stream,
	}
}

// luaSendQueueSize bounds the messages of Lua plugins that wait for the
// event loop.
const luaSendQueueSize = 256

// newLuaSender returns a send func for the Lua providers and a stop func. A
// plugin calls a control or cliamp.message while it holds its own lock, and
// send, which is prog.Send, waits until the event loop reads the message.
// The returned func only queues the message and returns at once. One
// goroutine passes the queued messages to send in order, as mediactl does
// for D-Bus calls. When the queue is full, the func drops the message and
// logs a warning once until the queue accepts a message again.
func newLuaSender(send func(tea.Msg)) (queue func(tea.Msg), stop func()) {
	msgs := make(chan tea.Msg, luaSendQueueSize)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			case msg := <-msgs:
				send(msg)
			}
		}
	}()
	var dropping atomic.Bool
	queue = func(msg tea.Msg) {
		select {
		case msgs <- msg:
			dropping.Store(false)
		default:
			if !dropping.Swap(true) {
				applog.Warn("lua plugins: %d messages wait for the player, so cliamp drops new ones", luaSendQueueSize)
			}
		}
	}
	var stopped atomic.Bool
	stop = func() {
		if stopped.CompareAndSwap(false, true) {
			close(done)
		}
	}
	return queue, stop
}

// luaUIProvider lets Lua plugins show a status message.
func luaUIProvider(send func(tea.Msg)) luaplugin.UIProvider {
	return luaplugin.UIProvider{
		ShowMessage: func(text string, duration time.Duration) {
			send(model.ShowStatusMsg{Text: text, Duration: duration})
		},
	}
}
