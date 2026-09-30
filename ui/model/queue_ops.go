package model

import (
	"errors"
	"fmt"
	"slices"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
)

// The queue edits in this file follow one rule, whether a key, IPC or a Lua
// plugin starts them:
//   - A move is refused while shuffle is on, because the rows then list the
//     play order and not the track order.
//   - When the queue mirrors a writable saved playlist, the edit is saved to
//     that playlist in one locked update.
//   - The gapless preload is re-armed when the edit changes the next track.

var (
	errQueueIndex    = errors.New("no track at that index")
	errQueueShuffled = errors.New("turn off shuffle to move tracks")
	errQueueDirTrack = errors.New("a directory source supplies the track")
)

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

// moveTrack swaps the track at from with the track at to and saves the new
// order to the loaded playlist. The playlist cursor stays on its track.
func (m *Model) moveTrack(from, to int) (tea.Cmd, error) {
	if m.playlist.Shuffled() {
		m.status.Warning(shuffleMoveWarning, statusTTLShort)
		return nil, errQueueShuffled
	}
	if !m.playlist.Move(from, to) {
		return nil, errQueueIndex
	}
	switch m.plCursor {
	case from:
		m.plCursor = to
	case to:
		m.plCursor = from
	}
	m.recountHeaderState(m.playlist.Tracks())
	m.normalizeQueueOverlay()
	m.persistLoadedPlaylistOrder()
	m.adjustScroll()
	return m.rearmStalePreload(), nil
}

// removeTrack removes the track at idx. When the queue mirrors a writable
// saved playlist, the track leaves that file too, and Ctrl+Z restores both.
// A track from a directory source is refused, because the file cannot drop
// it. Removing the track that plays stops playback.
func (m *Model) removeTrack(idx int) (tea.Cmd, error) {
	track, ok := m.playlist.Track(idx)
	if !ok {
		return nil, errQueueIndex
	}
	if track.DirSourced {
		m.status.Warningf(statusTTLDefault, "Can't remove %q: it's supplied by the playlist's directory source", track.DisplayName())
		return nil, errQueueDirTrack
	}
	snapshot := m.playlist.Snapshot()
	loaded := m.writableLoadedPlaylist()
	var saved []playlist.Track
	persisted := false
	if loaded != "" {
		if updater, ok := m.localProvider.(playlistUpdater); ok {
			err := updater.UpdatePlaylist(loaded, func(tracks []playlist.Track) ([]playlist.Track, error) {
				// Another writer or a new file in a directory source can
				// have shifted indexes since the queue was loaded. Match the
				// persisted explicit track by path so the wrong track is
				// never removed.
				savedIdx := slices.IndexFunc(tracks, func(candidate playlist.Track) bool {
					return !candidate.DirSourced && candidate.Path == track.Path
				})
				if savedIdx < 0 {
					return nil, fmt.Errorf("selected track is no longer in %q", loaded)
				}
				saved = cloneTracks(tracks)
				return slices.Delete(tracks, savedIdx, savedIdx+1), nil
			})
			if err != nil {
				m.status.Errorf(statusTTLDefault, "Remove failed: %s", err)
				return nil, err
			}
			persisted = true
		}
	}
	wasActive := idx == m.playlist.Index()
	if !m.playlist.Remove(idx) {
		return nil, errQueueIndex
	}
	m.normalizeQueueOverlay()
	m.playlistUndo = playlistUndo{active: true, snapshot: snapshot, loaded: loaded, saved: saved, persisted: persisted}
	if wasActive {
		m.stopPlayback()
		m.player.ClearPreload()
	}
	if idx < m.plCursor {
		m.plCursor--
	}
	if newLen := m.playlist.Len(); newLen == 0 {
		m.plCursor = 0
	} else if m.plCursor >= newLen {
		m.plCursor = newLen - 1
	}
	m.recountHeaderState(m.playlist.Tracks())
	m.adjustScroll()
	if persisted {
		m.status.Showf(statusTTLDefault, "Removed from %q: %s (Ctrl+Z to undo)", loaded, track.DisplayName())
	} else {
		m.status.Showf(statusTTLDefault, "Removed from queue: %s (Ctrl+Z to undo)", track.DisplayName())
	}
	m.notifyPlayback()
	return m.rearmStalePreload(), nil
}

// rearmStalePreload drops a preload that no longer holds the next track and
// arms the next track at once. A preload that still holds it stays, so an
// edit below the next track opens no new stream. Nothing is armed while
// playback is stopped or a track still buffers.
func (m *Model) rearmStalePreload() tea.Cmd {
	m.dropStalePreload()
	if !m.player.IsPlaying() || m.buffering || m.tracksPaging || m.preloading || m.player.HasPreload() {
		return nil
	}
	return m.preloadNext()
}
