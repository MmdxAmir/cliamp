package model

import (
	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
)

// This file holds one action verb for each user intent that more than one
// entry point starts. The keys, the full-screen visualizer keys, the
// playback messages from media controls and Lua, and V2 IPC call the same
// verb. So each intent gets the same scrobble, notification, config save
// and preload rearm, whatever starts it.

// skipNext scrobbles the track that plays and starts the next track.
func (m *Model) skipNext() tea.Cmd {
	refresh := m.scrobbleCurrent()
	cmd := m.nextTrack()
	m.notifyAll()
	return tea.Batch(refresh, cmd)
}

// skipPrev scrobbles the track that plays and goes to the previous track.
// Past 3 seconds into a track, it restarts that track instead.
func (m *Model) skipPrev() tea.Cmd {
	refresh := m.scrobbleCurrent()
	cmd := m.prevTrack()
	m.notifyAll()
	return tea.Batch(refresh, cmd)
}

// setShuffle turns shuffle on or off, saves the mode and re-arms the preload
// for the new next track.
func (m *Model) setShuffle(on bool) tea.Cmd {
	if m.playlist.Shuffled() != on {
		m.playlist.ToggleShuffle()
	}
	m.adjustScroll()
	_ = m.saveConfigBool("shuffle", m.playlist.Shuffled())
	return m.rearmPreload()
}

// setRepeat sets the repeat mode, saves it and re-arms the preload for the
// new next track.
func (m *Model) setRepeat(mode playlist.RepeatMode) tea.Cmd {
	m.playlist.SetRepeat(mode)
	_ = m.saveConfigString("repeat", m.playlist.Repeat().String())
	return m.rearmPreload()
}

// cycleVisualizer switches to the next visualizer mode, fits the layout to
// it and saves the choice. It returns the error of the config save.
func (m *Model) cycleVisualizer() error {
	m.vis.CycleMode()
	m.vis.RequestRefresh()
	m.applyHeightMode()
	m.adjustScroll()
	return m.saveVisualizerChoice()
}

// setVolume sets the volume in dB and tells the media controls and plugins.
// The player clamps db to its range.
func (m *Model) setVolume(db float64) {
	m.player.SetVolume(db)
	m.notifyAll()
}

// adjustVolume changes the volume by delta dB. See setVolume.
func (m *Model) adjustVolume(delta float64) {
	m.setVolume(m.player.Volume() + delta)
}
