package model

import tea "charm.land/bubbletea/v2"

// handleNetSearchResults shows the results of a net search.
func (m *Model) handleNetSearchResults(msg netSearchResultsMsg) {
	if msg.gen != m.requests.netSearch || !m.netSearch.active || msg.query != m.netSearch.request {
		return
	}
	m.netSearch.loading = false
	m.netSearch.cursor = 0
	m.netSearch.scroll = 0
	if msg.err != nil {
		m.netSearch.err = msg.err.Error()
		return
	}
	m.netSearch.results = msg.tracks
	m.netSearch.cursor = 0
	m.netSearch.screen = netSearchResults
	if len(msg.tracks) == 0 {
		m.netSearch.err = "No results found"
	}
	m.applyHeightMode()
	m.clampActiveScrollState()
}

// handleSpotSearchResults shows the track results of a provider search.
func (m *Model) handleSpotSearchResults(msg spotSearchResultsMsg) {
	if !m.isCurrentSpotRequest(msg.gen, msg.providerName) || m.spotSearch.query != msg.query {
		return
	}
	m.cancelSpotRequest()
	m.spotSearch.loading = false
	m.spotSearch.cursor = 0
	m.spotSearch.scroll = 0
	if msg.err != nil {
		m.setSpotSearchError(msg.err.Error())
		return
	}
	m.spotSearch.results = msg.tracks
	m.spotSearch.cursor = 0
	m.spotSearch.screen = spotSearchResults
	m.applyHeightMode()
	m.clampActiveScrollState()
}

// handleSpotAlbumTracks plays, appends or queues the tracks of an album
// that the provider search expanded.
func (m *Model) handleSpotAlbumTracks(msg spotAlbumTracksMsg) tea.Cmd {
	if msg.gen != m.requests.spotAlbum {
		return nil
	}
	m.cancelSpotRequest()
	m.spotSearch.albumLoading = false
	if msg.err != nil {
		m.setSpotSearchError(msg.err.Error())
		return nil
	}
	if len(msg.tracks) == 0 {
		m.setSpotSearchError("That album has no tracks available here.")
		return nil
	}
	album := msg.album
	tracks := msg.tracks
	m.closeSpotSearch()
	switch msg.action {
	case spotAlbumAppend:
		return m.appendAlbum(album, tracks)
	case spotAlbumQueueNext:
		return m.queueAlbumNext(album, tracks)
	default:
		return m.playAlbumImmediate(album, tracks)
	}
}

// handleSpotPlaylists shows the playlists that can take a provider search
// result.
func (m *Model) handleSpotPlaylists(msg spotPlaylistsMsg) {
	if !m.isCurrentSpotListRequest(msg.gen, msg.providerName) {
		return
	}
	m.spotSearch.loading = false
	m.spotSearch.cursor = 0
	m.spotSearch.scroll = 0
	if msg.err != nil {
		m.setSpotSearchError(msg.err.Error())
		return
	}
	m.spotSearch.playlists = msg.playlists
	m.spotSearch.cursor = 0
	m.spotSearch.screen = spotSearchPlaylist
	m.applyHeightMode()
	m.clampActiveScrollState()
}

// handleSpotAdded reports the add of a provider search result to a
// playlist.
func (m *Model) handleSpotAdded(msg spotAddedMsg) {
	if !m.isCurrentSpotMutation(msg.gen, msg.providerName) {
		return
	}
	m.cancelSpotRequest()
	m.spotSearch.loading = false
	if msg.err != nil {
		m.setSpotSearchError("Add failed: " + msg.err.Error())
		return
	}
	m.status.Showf(statusTTLDefault, "Added to %q", msg.name)
	m.closeSpotSearch()
}

// handleSpotCreated reports a playlist that the provider search created for
// a track.
func (m *Model) handleSpotCreated(msg spotCreatedMsg) {
	if !m.isCurrentSpotMutation(msg.gen, msg.providerName) {
		return
	}
	m.cancelSpotRequest()
	m.spotSearch.loading = false
	if msg.err != nil {
		m.setSpotSearchError("Create failed: " + msg.err.Error())
		return
	}
	m.status.Showf(statusTTLDefault, "Created %q & added track", msg.name)
	m.closeSpotSearch()
}
