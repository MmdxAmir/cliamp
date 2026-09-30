package model

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gopxl/beep/v2"

	"github.com/bjarneo/cliamp/external/local"
	"github.com/bjarneo/cliamp/favorites"
	"github.com/bjarneo/cliamp/history"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
	"github.com/bjarneo/cliamp/ui"
)

// customStreamTestProvider claims spotify: URIs like the Spotify provider.
type customStreamTestProvider struct {
	commandsTestProvider
}

func (customStreamTestProvider) URISchemes() []string { return []string{"spotify:"} }

func (customStreamTestProvider) NewStreamer(string) (beep.StreamSeekCloser, beep.Format, time.Duration, error) {
	return nil, beep.Format{}, 0, errors.New("not used")
}

func (customStreamTestProvider) Authenticate() error { return nil }

// sourceResolverEngine reports a play-time source resolver for each prefix,
// as player.Player does after main registers the Qobuz and Tidal resolvers.
type sourceResolverEngine struct {
	*playbackFakeEngine
	prefixes []string
}

func (e sourceResolverEngine) HasSourceResolver(path string) bool {
	for _, prefix := range e.prefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func newCustomStreamModel(player *playbackFakeEngine) Model {
	prov := customStreamTestProvider{commandsTestProvider{name: "Spotify"}}
	p := playlist.New()
	p.Replace([]playlist.Track{
		{Title: "Song", Path: "spotify:track:abc", DurationSecs: 200},
		{Title: "Local", Path: "local.mp3", DurationSecs: 100},
	})
	p.SetIndex(0)
	m := Model{
		player:    player,
		playlist:  p,
		provider:  prov,
		providers: []provider.Entry{{Key: "spotify", Name: "Spotify", Provider: prov}},
		vis:       ui.NewVisualizer(float64(player.SampleRate())),
	}
	m.SetVisualizer("none")
	return m
}

// streamPlayedFrom runs cmd and returns the streamPlayedMsg it produces.
func streamPlayedFrom(t *testing.T, cmd tea.Cmd) streamPlayedMsg {
	t.Helper()
	pending := []tea.Cmd{cmd}
	for len(pending) > 0 {
		next := pending[0]
		pending = pending[1:]
		if next == nil {
			continue
		}
		switch msg := next().(type) {
		case streamPlayedMsg:
			return msg
		case tea.BatchMsg:
			pending = append(pending, msg...)
		}
	}
	t.Fatal("command produced no streamPlayedMsg")
	return streamPlayedMsg{}
}

func TestPlayTrackStartsCustomURIOffUpdate(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		wantAsync bool
	}{
		{name: "spotify track", path: "spotify:track:abc", wantAsync: true},
		// A file that an older version wrote reloads a qobuz:// track
		// without the stream flag. The resolver still opens it over the
		// network.
		{name: "qobuz track from a saved list", path: "qobuz://track/42", wantAsync: true},
		{name: "local file", path: "local.mp3", wantAsync: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			player := &playbackFakeEngine{}
			m := newCustomStreamModel(player)
			m.player = sourceResolverEngine{player, []string{"qobuz://track/"}}

			cmd := m.playTrack(playlist.Track{Title: "Song", Path: tt.path, DurationSecs: 200})

			if m.buffering != tt.wantAsync {
				t.Fatalf("buffering = %v, want %v", m.buffering, tt.wantAsync)
			}
			if gotSync := len(player.playCalls) > 0; gotSync == tt.wantAsync {
				t.Fatalf("play calls inside playTrack = %v, want sync start %v", player.playCalls, !tt.wantAsync)
			}
			if !tt.wantAsync {
				return
			}

			msg := streamPlayedFrom(t, cmd)
			if msg.path != tt.path || len(player.playCalls) != 1 {
				t.Fatalf("async start = %+v, play calls = %v", msg, player.playCalls)
			}
			updated, _ := m.Update(msg)
			m = updated.(Model)
			if m.buffering || m.err != nil {
				t.Fatalf("after start: buffering = %v, err = %v", m.buffering, m.err)
			}
		})
	}
}

func TestPreloadNextWaitsForLeadTimeOnSourceResolverURI(t *testing.T) {
	tests := []struct {
		name        string
		position    time.Duration
		wantPreload bool
	}{
		{name: "early in the track", position: 10 * time.Second, wantPreload: false},
		{name: "inside the lead time", position: 58 * time.Second, wantPreload: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			player := &playbackFakeEngine{playing: true, duration: time.Minute, position: tt.position}
			p := playlist.New()
			p.Replace([]playlist.Track{
				{Title: "Current", Path: "qobuz://track/1", DurationSecs: 60},
				{Title: "Next", Path: "qobuz://track/2", DurationSecs: 60},
			})
			p.SetIndex(0)
			m := Model{player: sourceResolverEngine{player, []string{"qobuz://track/"}}, playlist: p}

			cmd := m.preloadNext()
			if gotPreload := cmd != nil; gotPreload != tt.wantPreload {
				t.Fatalf("preloadNext() command = %v, want %v", gotPreload, tt.wantPreload)
			}
			if len(player.preloadCalls) != 0 {
				t.Fatalf("preloadCalls inside preloadNext = %v, want none", player.preloadCalls)
			}
		})
	}
}

func TestStreamPlayedNeedsAuthAsksForSignIn(t *testing.T) {
	player := &playbackFakeEngine{}
	m := newCustomStreamModel(player)
	track, _ := m.playlist.Current()
	m.playTrack(track)

	updated, _ := m.Update(streamPlayedMsg{
		path: track.Path,
		gen:  m.requests.stream,
		err:  fmt.Errorf("spotify: stream auth error: %w", playlist.ErrNeedsAuth),
	})
	m = updated.(Model)

	if !m.provSignIn {
		t.Fatal("provSignIn = false, want the sign-in prompt")
	}
	if m.err != nil {
		t.Fatalf("err = %v, want nil so the prompt is not hidden", m.err)
	}
	if !strings.Contains(m.status.text, "Sign-in required") {
		t.Fatalf("status = %q, want a sign-in hint", m.status.text)
	}
}

func TestProviderAuthFailureKeepsSignInPrompt(t *testing.T) {
	m := newCustomStreamModel(&playbackFakeEngine{})
	gen := nextRequest(&m.requests.auth)

	updated, _ := m.Update(provAuthDoneMsg{providerName: "Spotify", gen: gen, err: errors.New("access_denied")})
	m = updated.(Model)
	if !m.provSignIn || m.err == nil {
		t.Fatalf("after failure: provSignIn = %v, err = %v, want prompt and error", m.provSignIn, m.err)
	}

	// Enter retries the sign-in and clears the old error.
	m.focus = focusProvider
	cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter on the sign-in prompt returned no command")
	}
	if m.provSignIn || !m.provLoading || m.err != nil {
		t.Fatalf("after retry: provSignIn = %v, provLoading = %v, err = %v", m.provSignIn, m.provLoading, m.err)
	}
}

// Favorites, Recently Played and saved playlists keep provider tracks that
// open over the network at play time, such as qobuz:// tracks. A reloaded
// track must start and seek off the Update goroutine. This also holds for a
// file that an older version wrote without the stream key.
func TestReloadedResolverTracksStayOffUpdate(t *testing.T) {
	prefixes := []string{"qobuz://track/", "tidal://track/", "yandex:track:", "lyrion://track/"}
	stores := []struct {
		name string
		// reload saves track, lets strip edit the file and loads the track back.
		reload func(t *testing.T, track playlist.Track, strip func(path string)) playlist.Track
	}{
		{name: "favorites", reload: func(t *testing.T, track playlist.Track, strip func(string)) playlist.Track {
			path := filepath.Join(t.TempDir(), "favorites.toml")
			if _, err := favorites.NewAt(path).Toggle(track); err != nil {
				t.Fatal(err)
			}
			strip(path)
			tracks, err := favorites.NewAt(path).Tracks()
			if err != nil || len(tracks) != 1 {
				t.Fatalf("favorites = %+v, %v", tracks, err)
			}
			return tracks[0]
		}},
		{name: "history", reload: func(t *testing.T, track playlist.Track, strip func(string)) playlist.Track {
			path := filepath.Join(t.TempDir(), "history.toml")
			if err := history.NewAt(path).Record(track, time.Now()); err != nil {
				t.Fatal(err)
			}
			strip(path)
			tracks, err := history.NewAt(path).Tracks(0)
			if err != nil || len(tracks) != 1 {
				t.Fatalf("history = %+v, %v", tracks, err)
			}
			return tracks[0]
		}},
		{name: "saved playlist", reload: func(t *testing.T, track playlist.Track, strip func(string)) playlist.Track {
			dir := t.TempDir()
			t.Setenv("CLIAMP_CONFIG_DIR", dir)
			if err := local.New(nil, nil).SavePlaylist("Mix", []playlist.Track{track}); err != nil {
				t.Fatal(err)
			}
			strip(filepath.Join(dir, "playlists", "Mix.toml"))
			tracks, err := local.New(nil, nil).Tracks("Mix")
			if err != nil || len(tracks) != 1 {
				t.Fatalf("Mix = %+v, %v", tracks, err)
			}
			return tracks[0]
		}},
	}
	// Each action runs on the Model after the async start and returns the
	// command of the seek. It must not call the player in Update.
	actions := []struct {
		name   string
		resume bool // the startup resume hint names the track
		act    func(m *Model, played streamPlayedMsg) tea.Cmd
	}{
		{name: "seek", act: func(m *Model, _ streamPlayedMsg) tea.Cmd { return m.seekAbsolute(30 * time.Second) }},
		{name: "resume", resume: true, act: func(m *Model, played streamPlayedMsg) tea.Cmd {
			updated, cmd := m.Update(played)
			*m = updated.(Model)
			return cmd
		}},
	}
	keep := func(string) {}
	for _, store := range stores {
		for _, legacy := range []bool{false, true} {
			for _, prefix := range prefixes {
				for _, action := range actions {
					name := fmt.Sprintf("%s/%s/legacy=%v/%s", store.name, prefix, legacy, action.name)
					t.Run(name, func(t *testing.T) {
						strip := keep
						if legacy {
							strip = func(path string) {
								data, err := os.ReadFile(path)
								if err != nil {
									t.Fatal(err)
								}
								if err := os.WriteFile(path, []byte(strings.ReplaceAll(string(data), "stream = true\n", "")), 0o644); err != nil {
									t.Fatal(err)
								}
							}
						}
						saved := playlist.Track{Path: prefix + "1", Title: "Song", Stream: true, DurationSecs: 200}
						track := store.reload(t, saved, strip)
						if track.Stream == legacy {
							t.Fatalf("reloaded Stream = %v, want %v", track.Stream, !legacy)
						}

						player := &playbackFakeEngine{seekable: true, duration: 200 * time.Second}
						m := newCustomStreamModel(player)
						m.player = sourceResolverEngine{player, prefixes}
						m.playlist.Replace([]playlist.Track{track})
						m.playlist.SetIndex(0)
						if action.resume {
							m.SetResume(track.Path, 30)
						}

						cmd := m.playTrack(track)
						if len(player.playCalls) != 0 || !m.buffering {
							t.Fatalf("playTrack opened the track in Update: play calls %v, buffering %v", player.playCalls, m.buffering)
						}
						played := streamPlayedFrom(t, cmd)
						if played.path != track.Path {
							t.Fatalf("async start = %+v, want %s", played, track.Path)
						}

						seek := action.act(&m, played)
						if len(player.seekCalls) != 0 || seek == nil {
							t.Fatalf("the seek ran in Update: seek calls %v, command %v", player.seekCalls, seek != nil)
						}
						if action.resume && (!m.seek.inFlight || m.seek.targetPos != 30*time.Second) {
							t.Fatalf("resume seek in flight %v to %v, want an async seek to 30s", m.seek.inFlight, m.seek.targetPos)
						}
					})
				}
			}
		}
	}
}
