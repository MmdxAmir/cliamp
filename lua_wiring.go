package main

import (
	"time"

	tea "charm.land/bubbletea/v2"

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
		Position:      func() float64 { return p.Position().Seconds() },
		Duration:      func() float64 { return p.Duration().Seconds() },
		Volume:        func() float64 { return p.Volume() },
		Speed:         func() float64 { return p.Speed() },
		Mono:          func() bool { return p.Mono() },
		RepeatMode:    func() string { return pl.Repeat().String() },
		Shuffle:       func() bool { return pl.Shuffled() },
		EQBands:       func() [10]float64 { return p.EQBands() },
		TrackTitle:    func() string { t, _ := pl.Current(); return t.Title },
		TrackArtist:   func() string { t, _ := pl.Current(); return t.Artist },
		TrackAlbum:    func() string { t, _ := pl.Current(); return t.Album },
		TrackGenre:    func() string { t, _ := pl.Current(); return t.Genre },
		TrackYear:     func() int { t, _ := pl.Current(); return t.Year },
		TrackNumber:   func() int { t, _ := pl.Current(); return t.TrackNumber },
		TrackPath:     func() string { t, _ := pl.Current(); return t.Path },
		TrackIsStream: func() bool { t, _ := pl.Current(); return t.Stream },
		TrackIsLive:   func() bool { t, _ := pl.Current(); return model.PlaysLive(t, p) },
		TrackDuration: func() int { t, _ := pl.Current(); return t.DurationSecs },
		PlaylistCount: func() int { return pl.Len() },
		CurrentIndex:  func() int { return pl.Index() },
		HasNext:       pl.HasNext,
		QueueList: func() []luaplugin.QueueEntry {
			tracks := pl.Tracks()
			out := make([]luaplugin.QueueEntry, len(tracks))
			for i, t := range tracks {
				out[i] = luaplugin.QueueEntry{
					Title:    t.Title,
					Artist:   t.Artist,
					Album:    t.Album,
					Genre:    t.Genre,
					Year:     t.Year,
					Path:     t.Path,
					Duration: t.DurationSecs,
					Stream:   t.Stream,
					Index:    i,
					Queued:   pl.QueuePosition(i) > 0, // 1-based; 0 means not queued
				}
			}
			return out
		},
	}
}

// luaControlProvider gives Lua plugins control of playback. Most controls
// send a message to the Model through send.
func luaControlProvider(p *player.Player, send func(tea.Msg)) luaplugin.ControlProvider {
	return luaplugin.ControlProvider{
		SetVolume:   func(db float64) { p.SetVolume(db) },
		SetSpeed:    func(ratio float64) { p.SetSpeed(ratio) },
		SetEQBand:   func(band int, db float64) { send(model.SetEQBandMsg{Band: band, Gain: db}) },
		ToggleMono:  func() { p.ToggleMono() },
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
		QueueAddTrack: func(t luaplugin.QueueTrack) {
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

// luaUIProvider lets Lua plugins show a status message.
func luaUIProvider(send func(tea.Msg)) luaplugin.UIProvider {
	return luaplugin.UIProvider{
		ShowMessage: func(text string, duration time.Duration) {
			send(model.ShowStatusMsg{Text: text, Duration: duration})
		},
	}
}
