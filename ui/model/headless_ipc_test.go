package model

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/history"
	"github.com/bjarneo/cliamp/internal/playback"
	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// The tests in this file drive a headless Model with no WindowSizeMsg. They
// replace the tests of the headless daemon that the Model took over.

// libraryTestProvider serves one playlist, a search, artists and albums.
type libraryTestProvider struct{ commandsTestProvider }

func (libraryTestProvider) Playlists() ([]playlist.PlaylistInfo, error) {
	return []playlist.PlaylistInfo{{ID: "mix", Name: "Mix", TrackCount: 2}}, nil
}
func (libraryTestProvider) Tracks(string) ([]playlist.Track, error) {
	return []playlist.Track{{Path: "/one.flac", Title: "One"}, {Path: "/two.flac", Title: "Two"}}, nil
}
func (libraryTestProvider) SearchTracks(context.Context, string, int) ([]playlist.Track, error) {
	return []playlist.Track{{Path: "/result.flac", Title: "Result"}}, nil
}
func (libraryTestProvider) Artists() ([]provider.ArtistInfo, error) {
	return []provider.ArtistInfo{{ID: "artist", Name: "Artist", AlbumCount: 1}}, nil
}
func (libraryTestProvider) ArtistAlbums(string) ([]provider.AlbumInfo, error) {
	return []provider.AlbumInfo{{ID: "album", Name: "Album", Artist: "Artist", TrackCount: 2}}, nil
}
func (libraryTestProvider) AlbumList(string, int, int) ([]provider.AlbumInfo, error) {
	return []provider.AlbumInfo{{ID: "album", Name: "Album"}}, nil
}
func (libraryTestProvider) AlbumSortTypes() []provider.SortType {
	return []provider.SortType{{ID: "name", Label: "By name"}}
}
func (libraryTestProvider) DefaultAlbumSort() string { return "name" }
func (libraryTestProvider) AlbumTracks(string) ([]playlist.Track, error) {
	return []playlist.Track{{Path: "/album.flac", Title: "Album track"}}, nil
}

// stationCatalogProvider is a station catalog with a search and favorites.
type stationCatalogProvider struct {
	commandsTestProvider
	searching bool
	favorite  string
}

func (p *stationCatalogProvider) Playlists() ([]playlist.PlaylistInfo, error) {
	return []playlist.PlaylistInfo{{ID: "c:station", Name: "Station"}}, nil
}
func (p *stationCatalogProvider) Tracks(string) ([]playlist.Track, error) {
	return []playlist.Track{{Path: "https://radio.example/stream", Title: "Station", Stream: true}}, nil
}
func (p *stationCatalogProvider) SearchCatalog(string) (int, error) {
	p.searching = true
	return 1, nil
}
func (*stationCatalogProvider) LoadCatalogPage(int, int) (int, error) { return 1, nil }
func (p *stationCatalogProvider) ClearSearch()                        { p.searching = false }
func (p *stationCatalogProvider) IsSearching() bool                   { return p.searching }
func (*stationCatalogProvider) IDPrefix(string) string                { return "c" }
func (*stationCatalogProvider) IsFavoritableID(id string) bool        { return id == "c:station" }
func (p *stationCatalogProvider) ToggleFavorite(id string) (bool, string, error) {
	p.favorite = id
	return true, "Station", nil
}

func TestHeadlessStateSnapshot(t *testing.T) {
	engine := &headlessEngine{}
	engine.playing, engine.seekable = true, true
	engine.position, engine.duration = 12*time.Second, 3*time.Minute
	m := newHeadlessModel(t, engine, nil,
		playlist.Track{Path: "https://example.com/one", Title: "One", Stream: true},
		playlist.Track{Path: "https://example.com/two", Title: "Two", Stream: true},
	)
	m.playlist.Queue(1)
	m.SetIPCBroker(ipc.NewBroker())
	m.publishIPCRuntimeState()

	reply := make(chan V2RequestResult, 1)
	updated, _ := m.Update(V2RequestMsg{Request: ipc.V2Request{Method: "state.get"}, Reply: reply})
	m = updated.(Model)
	result := <-reply
	if result.Error != nil || result.Result.Snapshot == nil {
		t.Fatalf("state.get = %+v", result)
	}
	snapshot := result.Result.Snapshot
	if snapshot.Revision != m.ipcRuntime.revision || snapshot.Revision == 0 || snapshot.PlaylistRevision != m.playlist.Revision() {
		t.Fatalf("revisions = %d/%d, want %d/%d", snapshot.Revision, snapshot.PlaylistRevision, m.ipcRuntime.revision, m.playlist.Revision())
	}
	if snapshot.State != "playing" || !snapshot.Seekable || snapshot.Position != 12 || snapshot.Duration != 180 || snapshot.Total != 2 || snapshot.PlayNextTotal != 1 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if snapshot.Track == nil || snapshot.LogicalTrack == nil || snapshot.Track.Path != "https://example.com/one" || snapshot.LogicalTrack.Path != "https://example.com/one" {
		t.Fatalf("tracks = %+v / %+v", snapshot.Track, snapshot.LogicalTrack)
	}
}

// A job reports its result and the snapshot of the state that it committed.
func TestHeadlessVolumeJobCarriesSnapshot(t *testing.T) {
	engine := &headlessEngine{volume: -6}
	m := newHeadlessModel(t, engine, nil, playlist.Track{Path: "/music/one.flac", Title: "One"})

	msg := v2Request(t, "volume", ipc.Request{Value: -18})
	m.Update(msg)
	job, ok := msg.Jobs.Get(msg.JobID)
	if !ok || job.State != ipc.JobSucceeded {
		t.Fatalf("job = %+v, found %v", job, ok)
	}
	if engine.volume != -18 || job.Snapshot == nil || job.Snapshot.Volume != -18 {
		t.Fatalf("volume = %v, job snapshot = %+v; want -18", engine.volume, job.Snapshot)
	}
}

func TestHeadlessQueueAndPlayNextAreSeparate(t *testing.T) {
	m := newHeadlessModel(t, &headlessEngine{}, nil)

	if response := runV2(t, &m, "queue", ipc.Request{Path: "https://example.com/queued"}); !response.OK || m.playlist.Len() != 1 || m.playlist.QueueLen() != 0 {
		t.Fatalf("queue = %+v, playlist %d, play-next %d", response, m.playlist.Len(), m.playlist.QueueLen())
	}
	if response := runV2(t, &m, "queue.enqueue", ipc.Request{Index: 0}); !response.OK || m.playlist.Len() != 1 || m.playlist.QueueLen() != 1 {
		t.Fatalf("queue.enqueue = %+v, playlist %d, play-next %d", response, m.playlist.Len(), m.playlist.QueueLen())
	}
	playNext := runV2(t, &m, "playnext.list", ipc.Request{})
	if len(playNext.Tracks) != 1 || playNext.Total != 1 || playNext.Tracks[0].QueuePosition != 1 {
		t.Fatalf("playnext.list = %+v", playNext)
	}
	live := runV2(t, &m, "queue.list", ipc.Request{})
	if len(live.Tracks) != 1 || live.Total != 1 || live.Tracks[0].Path != playNext.Tracks[0].Path {
		t.Fatalf("queue.list = %+v", live)
	}
}

func TestHeadlessQueueListIncludesMetadata(t *testing.T) {
	m := newHeadlessModel(t, &headlessEngine{}, nil,
		playlist.Track{Path: "/one.flac", Title: "One", Album: "Album", DurationSecs: 60},
		playlist.Track{Path: "/two.flac", Title: "Two"},
	)
	m.playlist.Queue(1)

	response := runV2(t, &m, "queue.list", ipc.Request{})
	if len(response.Tracks) != 2 || response.Tracks[0].Album != "Album" || response.Tracks[0].DurationSecs != 60 || response.Tracks[1].QueuePosition != 1 {
		t.Fatalf("queue.list = %+v", response.Tracks)
	}
}

// Media-control messages apply in the order that they arrive.
func TestHeadlessMediaControlsApplyInOrder(t *testing.T) {
	engine := &headlessEngine{volume: -6}
	m := newHeadlessModel(t, engine, nil)
	for _, want := range []float64{-10, -20} {
		updated, _ := m.Update(playback.SetVolumeMsg{VolumeDB: want})
		m = updated.(Model)
		if engine.volume != want {
			t.Fatalf("volume = %v, want %v", engine.volume, want)
		}
	}
}

// play on a paused live station connects again to the station that plays. It
// does not resume the stale buffer, and it does not advance.
func TestHeadlessPlayRestartsLiveStation(t *testing.T) {
	engine := &headlessEngine{}
	engine.playing, engine.paused, engine.live = true, true, true
	m := newHeadlessModel(t, engine, nil,
		playlist.Track{Path: "https://radio.example.com/one", Title: "One", Stream: true},
		playlist.Track{Path: "https://radio.example.com/two", Title: "Two", Stream: true},
	)

	if response := runV2(t, &m, "play", ipc.Request{}); !response.OK {
		t.Fatalf("play = %+v", response)
	}
	if got := m.playlist.Index(); got != 0 {
		t.Fatalf("playlist index = %d, want the current station 0", got)
	}
	if engine.stopCalls != 1 || !m.buffering || m.playingTrack.Path != "https://radio.example.com/one" {
		t.Fatalf("stops = %d, buffering %v, track %q; want a new connection to station one", engine.stopCalls, m.buffering, m.playingTrack.Path)
	}
}

// A drained live stream connects again in place. A yt-dlp recording with a
// stale live flag has a duration, so its drain advances.
func TestHeadlessDrainedLiveStream(t *testing.T) {
	ytdlLive := playlist.Track{Path: "https://music.youtube.com/watch?v=live1", Title: "Live", Stream: true, Realtime: true}
	ytdlNext := playlist.Track{Path: "https://music.youtube.com/watch?v=next1", Title: "Next", Stream: true, DurationSecs: 100}
	for _, tc := range []struct {
		name          string
		tracks        []playlist.Track
		runtimeLive   bool
		duration      time.Duration
		wantIndex     int
		wantReconnect bool
	}{
		{
			name: "radio station",
			tracks: []playlist.Track{
				{Path: "https://radio.example.com/one", Title: "One", Stream: true},
				{Path: "https://radio.example.com/two", Title: "Two", Stream: true},
			},
			runtimeLive:   true,
			wantReconnect: true,
		},
		{name: "yt-dlp stream that is still live", tracks: []playlist.Track{ytdlLive, ytdlNext}, wantReconnect: true},
		{name: "yt-dlp recording that ended", tracks: []playlist.Track{ytdlLive, ytdlNext}, duration: 90 * time.Minute, wantIndex: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine := &headlessEngine{}
			engine.playing, engine.drained, engine.live, engine.duration = true, true, tc.runtimeLive, tc.duration
			m := newHeadlessModel(t, engine, nil, tc.tracks...)

			updated, _ := m.Update(tickMsg(time.Now()))
			m = updated.(Model)
			if got := m.playlist.Index(); got != tc.wantIndex {
				t.Fatalf("playlist index = %d, want %d", got, tc.wantIndex)
			}
			if got := !m.reconnect.at.IsZero(); got != tc.wantReconnect {
				t.Fatalf("reconnect scheduled = %v, want %v", got, tc.wantReconnect)
			}
		})
	}
}

// The snapshot splits ICY metadata into artist and title and names the
// station, the same way the TUI shows it.
func TestHeadlessStreamTitleFields(t *testing.T) {
	tests := []struct {
		name        string
		streamTitle string
		track       playlist.Track
		wantTitle   string
		wantArtist  string
		wantStation string
		wantStream  string
	}{
		{
			name:        "artist and title split on separator",
			streamTitle: "Tycho - Awake",
			track:       playlist.Track{Path: "https://example.com/ncs", Title: "NCS Trap Stream", Stream: true},
			wantTitle:   "Awake",
			wantArtist:  "Tycho",
			wantStation: "NCS Trap Stream",
			wantStream:  "Tycho - Awake",
		},
		{
			name:        "empty title after the separator keeps the station",
			streamTitle: "Tycho - ",
			track:       playlist.Track{Path: "https://example.com/lofi", Title: "Lofi Stream", Stream: true},
			wantTitle:   "Lofi Stream",
			wantStream:  "Tycho - ",
		},
		{
			name:        "title-only metadata becomes the title",
			streamTitle: "Morning Session",
			track:       playlist.Track{Path: "https://example.com/lofi", Title: "Lofi Stream", Stream: true},
			wantTitle:   "Morning Session",
			wantStation: "Lofi Stream",
			wantStream:  "Morning Session",
		},
		{
			name:      "no metadata leaves the entry untouched",
			track:     playlist.Track{Path: "https://example.com/lofi", Title: "Lofi Stream", Stream: true},
			wantTitle: "Lofi Stream",
		},
		{
			name:        "non-stream track is never rewritten",
			streamTitle: "Tycho - Awake",
			track:       playlist.Track{Path: "/music/alien-boy.flac", Title: "Alien Boy", Artist: "Oliver Tree"},
			wantTitle:   "Alien Boy",
			wantArtist:  "Oliver Tree",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			engine := &headlessEngine{streamTitle: tc.streamTitle}
			engine.playing = true
			m := newHeadlessModel(t, engine, nil, tc.track)

			// The tick reads the ICY title from the player.
			updated, _ := m.Update(tickMsg(time.Now()))
			m = updated.(Model)
			info := m.runtimeSnapshot().Track
			if info == nil {
				t.Fatal("snapshot has no track")
			}
			for _, f := range []struct{ field, got, want string }{
				{"Title", info.Title, tc.wantTitle},
				{"Artist", info.Artist, tc.wantArtist},
				{"Station", info.Station, tc.wantStation},
				{"StreamTitle", info.StreamTitle, tc.wantStream},
			} {
				if f.got != f.want {
					t.Errorf("%s = %q, want %q", f.field, f.got, f.want)
				}
			}
		})
	}
}

func TestHeadlessLibraryRequests(t *testing.T) {
	providers := []provider.Entry{{Key: "test", Name: "Test", Provider: libraryTestProvider{commandsTestProvider{name: "Test"}}}}
	for _, tc := range []struct {
		op     string
		params ipc.Request
		ok     func(ipc.Response) bool
	}{
		{"provider.list", ipc.Request{}, func(r ipc.Response) bool {
			return len(r.Providers) == 1 && r.Providers[0].Searchable && r.Providers[0].BrowseArtists && r.Providers[0].BrowseAlbums
		}},
		{"provider.playlists", ipc.Request{Provider: "test"}, func(r ipc.Response) bool {
			return len(r.Playlists) == 1 && r.Playlists[0].ID == "mix" && r.Playlists[0].Provider == "test"
		}},
		{"provider.search", ipc.Request{Provider: "test", Query: "result"}, func(r ipc.Response) bool {
			return len(r.Tracks) == 1 && r.Tracks[0].Title == "Result"
		}},
		{"provider.artists", ipc.Request{Provider: "test"}, func(r ipc.Response) bool {
			return len(r.Artists) == 1 && r.Artists[0].Name == "Artist"
		}},
		{"provider.artist_albums", ipc.Request{Provider: "test", Artist: "artist"}, func(r ipc.Response) bool {
			return len(r.Albums) == 1 && r.Albums[0].Artist == "Artist"
		}},
		{"provider.albums", ipc.Request{Provider: "test"}, func(r ipc.Response) bool {
			return len(r.Albums) == 1 && len(r.Sorts) == 1 && r.Albums[0].Name == "Album"
		}},
		{"provider.album_tracks", ipc.Request{Provider: "test", Album: "album"}, func(r ipc.Response) bool {
			return len(r.Tracks) == 1 && r.Tracks[0].Title == "Album track"
		}},
		{"provider.tracks", ipc.Request{Provider: "test", Playlist: "mix"}, func(r ipc.Response) bool {
			return len(r.Tracks) == 2 && r.Total == 2 && r.Playlist == "mix"
		}},
	} {
		t.Run(tc.op, func(t *testing.T) {
			m := newHeadlessModel(t, &headlessEngine{}, providers)
			if response := runV2(t, &m, tc.op, tc.params); !response.OK || !tc.ok(response) {
				t.Fatalf("%s = %+v", tc.op, response)
			}
		})
	}
}

func TestIPCTrackInfoConversion(t *testing.T) {
	track := playlist.Track{Path: "https://example.com/stream", Title: "Stream", Artist: "Artist", Realtime: true}
	info := ipcTrackInfo(track, 3, 2, false)
	converted := ipcTrackFromInfo(info)
	if info.Index != 3 || info.QueuePosition != 2 || converted.Path != track.Path || !converted.Stream || !converted.Realtime {
		t.Fatalf("conversion lost metadata: info=%+v converted=%+v", info, converted)
	}
}

// A search on a station catalog runs the catalog search and clears it again.
func TestHeadlessCatalogSearch(t *testing.T) {
	prov := &stationCatalogProvider{commandsTestProvider: commandsTestProvider{name: "Catalog"}}
	m := newHeadlessModel(t, &headlessEngine{}, []provider.Entry{{Key: "radio", Name: "Radio", Provider: prov}})

	response := runV2(t, &m, "provider.search", ipc.Request{Provider: "radio", Query: "station"})
	if !response.OK || len(response.Tracks) != 1 || response.Tracks[0].Title != "Station" || prov.searching {
		t.Fatalf("provider.search = %+v, searching %v", response, prov.searching)
	}
}

func TestHeadlessProviderFavoriteAndCatalog(t *testing.T) {
	prov := &stationCatalogProvider{commandsTestProvider: commandsTestProvider{name: "Catalog"}}
	m := newHeadlessModel(t, &headlessEngine{}, []provider.Entry{{Key: "radio", Name: "Radio", Provider: prov}})

	if response := runV2(t, &m, "provider.favorite", ipc.Request{Provider: "radio", Playlist: "c:station"}); !response.OK || prov.favorite != "c:station" {
		t.Fatalf("provider.favorite = %+v, provider favorite %q", response, prov.favorite)
	}
	if response := runV2(t, &m, "provider.playlists", ipc.Request{Provider: "radio"}); len(response.Playlists) != 1 || !response.Playlists[0].Favoritable {
		t.Fatalf("provider.playlists = %+v", response)
	}
	if response := runV2(t, &m, "provider.catalog", ipc.Request{Provider: "radio", Limit: 50}); response.Total != 1 || len(response.Playlists) != 1 {
		t.Fatalf("provider.catalog = %+v", response)
	}
}

func TestHeadlessPlaylistMutations(t *testing.T) {
	prov := &writableTestProvider{commandsTestProvider: commandsTestProvider{name: "Writable"}, removed: -1}
	m := newHeadlessModel(t, &headlessEngine{}, []provider.Entry{{Key: "local", Name: "Local", Provider: prov}})
	track := ipc.TrackInfo{Path: "/song.flac"}
	for _, tc := range []struct {
		op     string
		params ipc.Request
	}{
		{"playlist.create", ipc.Request{Provider: "local", Playlist: "Mix"}},
		{"playlist.rename", ipc.Request{Provider: "local", Playlist: "Mix", NewName: "New"}},
		{"playlist.delete", ipc.Request{Provider: "local", Playlist: "Old"}},
		{"playlist.remove", ipc.Request{Provider: "local", Playlist: "Mix", Index: 3}},
		{"playlist.add", ipc.Request{Provider: "local", Playlist: "Mix", Track: &track}},
	} {
		if response := runV2(t, &m, tc.op, tc.params); !response.OK {
			t.Fatalf("%s = %+v", tc.op, response)
		}
	}
	if prov.created != "Mix" || prov.renamed != "Mix:New" || prov.deleted != "Old" || prov.removed != 3 || !slices.Equal(prov.added, []string{"Mix:/song.flac"}) {
		t.Fatalf("writes were not forwarded: %+v", prov)
	}
}

// playlist.bookmark toggles the ♥ favorite and copies the change to the
// provider that owns the track. That provider ends with the last state.
func TestHeadlessBookmarkSyncsOwningProvider(t *testing.T) {
	for _, tc := range []struct {
		name      string
		path      string
		wantCalls bool
	}{
		{name: "local file", path: "/song.flac"},
		{name: "provider track", path: "fake:track:1", wantCalls: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &dirSourceTestProvider{commandsTestProvider: commandsTestProvider{name: "Local"}}
			fake := &fakeTrackFavoriter{commandsTestProvider: commandsTestProvider{name: "Fake"}, prefix: "fake:"}
			m := newHeadlessModel(t, &headlessEngine{}, []provider.Entry{
				{Key: "local", Name: "Local", Provider: store},
				{Key: "fake", Name: "Fake", Provider: fake},
			})
			m.localProvider, m.favStore = store, store.useFavorites(t)
			track := ipc.TrackInfo{Path: tc.path, Title: "Song"}

			for _, want := range []bool{true, false} {
				if response := runV2(t, &m, "playlist.bookmark", ipc.Request{Provider: "local", Playlist: "Mix", Track: &track}); !response.OK {
					t.Fatalf("playlist.bookmark = %+v", response)
				}
				if got := store.favs.IsFavorited(tc.path); got != want {
					t.Fatalf("favorited = %v, want %v", got, want)
				}
			}
			if !tc.wantCalls {
				time.Sleep(50 * time.Millisecond)
				if calls := fake.recorded(); len(calls) != 0 {
					t.Fatalf("provider calls = %v, want none", calls)
				}
				return
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				calls := fake.recorded()
				if len(calls) > 0 && calls[len(calls)-1] == "remove "+tc.path {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("provider calls = %v, want the last call to remove %s", calls, tc.path)
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}

func TestHeadlessClearsHistory(t *testing.T) {
	m := newHeadlessModel(t, &headlessEngine{}, nil)
	store := history.New()
	if err := store.Record(playlist.Track{Path: "/song.flac"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if response := runV2(t, &m, "history", ipc.Request{}); len(response.History) != 1 {
		t.Fatalf("history = %+v, want 1 entry", response)
	}
	if response := runV2(t, &m, "history.clear", ipc.Request{}); !response.OK {
		t.Fatalf("history.clear = %+v", response)
	}
	if entries, err := store.Recent(0); err != nil || len(entries) != 0 {
		t.Fatalf("history after clear = %+v, err %v", entries, err)
	}
}
