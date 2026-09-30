package model

import "github.com/bjarneo/cliamp/playlist"

// appendTracks adds tracks to the end of the queue and returns the index of
// the first one. The queue then mirrors no saved playlist, and the album
// header counters include the new tracks. The caller starts playback or
// re-arms the preload, because that depends on why the tracks came.
func (m *Model) appendTracks(tracks ...playlist.Track) int {
	first := m.playlist.Len()
	m.playlist.Add(tracks...)
	m.clearLoadedPlaylist()
	m.addToHeaderState(tracks)
	return first
}
