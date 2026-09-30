package model

import "github.com/bjarneo/cliamp/luaplugin"

// PluginState is the playback state that Lua plugins read. The Model
// publishes a new PluginState at the end of each Update, so a plugin never
// sees a state in the middle of an Update. Track is the track that plays, as
// the plugin events and the media controls report it. The engine position
// is not in it, because it moves between Updates.
type PluginState struct {
	Status  string // "playing", "paused" or "stopped"
	Volume  float64
	Speed   float64
	Mono    bool
	Repeat  string
	Shuffle bool
	EQBands [10]float64
	Track   luaplugin.Track
	Count   int
	Index   int
	HasNext bool
	// Queue is the playlist. The states share it until the playlist
	// changes, so a caller must not change it.
	Queue []luaplugin.QueueEntry

	revision uint64 // the playlist revision that Queue shows
}

// PluginStateLoader returns a func that loads the state that the Model
// published last. With no plugin loaded, the func returns a stopped state.
// The func is safe to call from any goroutine. Every copy of the Model
// publishes to the store that it reads.
func (m *Model) PluginStateLoader() func() PluginState {
	store := m.pluginState
	return func() PluginState {
		if store != nil {
			if state := store.Load(); state != nil {
				return *state
			}
		}
		return PluginState{Status: "stopped", Speed: 1}
	}
}

// publishPluginState stores the state that Lua plugins read. It rebuilds
// the queue only when the playlist revision changed.
func (m *Model) publishPluginState() {
	if m.pluginState == nil || m.player == nil || m.playlist == nil {
		return
	}
	track, _ := m.currentPlaybackTrack()
	state := &PluginState{
		Status:   m.playerStatus(),
		Volume:   m.player.Volume(),
		Speed:    m.player.Speed(),
		Mono:     m.player.Mono(),
		Repeat:   m.playlist.Repeat().String(),
		Shuffle:  m.playlist.Shuffled(),
		EQBands:  m.player.EQBands(),
		Track:    pluginTrack(track),
		Count:    m.playlist.Len(),
		Index:    m.playlist.Index(),
		HasNext:  m.playlist.HasNext(),
		revision: m.playlist.Revision(),
	}
	state.Track.Artist, state.Track.Title = m.resolveTrackDisplay(track)
	state.Track.Live = m.currentPlaybackIsLive(track)
	if prev := m.pluginState.Load(); prev != nil && prev.revision == state.revision {
		state.Queue = prev.Queue
	} else {
		state.Queue = m.pluginQueue()
	}
	m.pluginState.Store(state)
}

// pluginQueue returns the playlist as cliamp.queue.list reports it.
func (m *Model) pluginQueue() []luaplugin.QueueEntry {
	queued := make(map[int]bool)
	for _, entry := range m.playlist.QueueEntries() {
		queued[entry.TrackIndex] = true
	}
	tracks := m.playlist.Tracks()
	queue := make([]luaplugin.QueueEntry, len(tracks))
	for i, track := range tracks {
		queue[i] = luaplugin.QueueEntry{Track: pluginTrack(track), Index: i, Queued: queued[i]}
	}
	return queue
}
