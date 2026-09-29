package player

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/wav"
)

// installPipelineRouteFixtures puts fake ffmpeg, ffprobe and ssh binaries
// first on PATH. The fake ffmpeg writes one PCM frame and then waits, so every
// ffmpeg route starts without a real decoder.
func installPipelineRouteFixtures(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "ffmpeg"), `#!/bin/sh
printf '\000\100\000\300'
exec sleep 30
`)
	writeExecutable(t, filepath.Join(dir, "ffprobe"), `#!/bin/sh
printf '1\n'
`)
	writeExecutable(t, filepath.Join(dir, "ssh"), `#!/bin/sh
exec sleep 30
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

// testWAV returns a short silent WAV file that the native decoder accepts.
func testWAV(t *testing.T) []byte {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "tone-*.wav")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	format := beep.Format{SampleRate: 44100, NumChannels: 2, Precision: 2}
	if err := wav.Encode(f, beep.Silence(441), format); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// routeServer serves one response shape per path: with or without
// Content-Length, and with or without Icy headers.
func routeServer(t *testing.T, wavData []byte) *httptest.Server {
	t.Helper()
	garbage := bytes.Repeat([]byte("not audio "), 64)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		finite := func(body []byte) {
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			_, _ = w.Write(body)
		}
		chunked := func(body []byte) {
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			_, _ = w.Write(body)
		}
		// live never ends the response, like an Icecast mount.
		live := func(body []byte) {
			chunked(body)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		}
		switch r.URL.Path {
		case "/finite.wav":
			finite(wavData)
		case "/chunked.wav":
			chunked(wavData)
		case "/radio.aac":
			w.Header().Set("Icy-Name", "Test Radio")
			live(garbage)
		case "/radio-length.aac":
			w.Header().Set("Icy-Name", "Test Radio")
			finite(garbage)
		case "/garbage.ogg", "/garbage.mp3":
			chunked(garbage)
		case "/radio.ogg":
			w.Header().Set("Icy-Name", "Test Radio")
			live(garbage)
		case "/buffered", "/seg0", "/seg1":
			chunked(garbage)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestBuildPipelineRoutes pins the pipeline shape of each buildPipeline route.
func TestBuildPipelineRoutes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX shell fixtures")
	}
	fixtures := installPipelineRouteFixtures(t)
	wavData := testWAV(t)
	srv := routeServer(t, wavData)

	localFiles := map[string][]byte{
		"track.m4a": []byte("container bytes"),
		"tone.wav":  wavData,
		"bad.wav":   []byte("not a native wav"),
	}
	for name, data := range localFiles {
		if err := os.WriteFile(filepath.Join(fixtures, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	garbageLen := int64(len(bytes.Repeat([]byte("not audio "), 64)))

	tests := []struct {
		name     string
		path     string
		register func(p *Player)
		wantErr  string

		wantDecoder  string
		wantPath     string
		seekable     bool
		live         bool
		prefetch     bool
		counted      bool
		wantLength   int64
		wantDuration time.Duration
	}{
		{
			name: "custom streamer factory",
			path: "fake:track:1",
			register: func(p *Player) {
				p.RegisterStreamerFactory("fake:", func(string) (beep.StreamSeekCloser, beep.Format, time.Duration, error) {
					return newPlaybackTestDecoder(), beep.Format{SampleRate: 44100, NumChannels: 2, Precision: 2}, time.Minute, nil
				})
			},
			wantDecoder:  "*player.playbackTestDecoder",
			seekable:     true,
			wantDuration: time.Minute,
		},
		{
			name: "source resolver segments",
			path: "segs://track/1",
			register: func(p *Player) {
				p.RegisterSourceResolver("segs://", func(string) (ResolvedSource, error) {
					return ResolvedSource{Segments: []string{srv.URL + "/seg0", srv.URL + "/seg1"}}, nil
				})
			},
			wantDecoder: "*player.navFFmpegStreamer",
			wantPath:    "segs://track/1",
			seekable:    true,
			counted:     true,
			wantLength:  -1,
		},
		{
			name: "source resolver url",
			path: "res://radio/1",
			register: func(p *Player) {
				p.RegisterSourceResolver("res://", func(string) (ResolvedSource, error) {
					return ResolvedSource{URL: srv.URL + "/radio.aac"}, nil
				})
			},
			wantDecoder: "*player.ffmpegPipeStreamer",
			wantPath:    srv.URL + "/radio.aac",
			live:        true,
			prefetch:    true,
			counted:     true,
			wantLength:  -1,
		},
		{
			name: "buffered url matcher",
			path: srv.URL + "/buffered",
			register: func(p *Player) {
				p.RegisterBufferedURLMatcher(func(u string) bool { return strings.HasSuffix(u, "/buffered") })
			},
			wantDecoder: "*player.navFFmpegStreamer",
			wantPath:    srv.URL + "/buffered",
			seekable:    true,
			counted:     true,
			wantLength:  -1,
		},
		{
			name:        "hls playlist",
			path:        srv.URL + "/live.m3u8",
			wantDecoder: "*player.ffmpegPipeStreamer",
			wantPath:    srv.URL + "/live.m3u8",
			prefetch:    true,
		},
		{
			name:        "finite http with content length",
			path:        srv.URL + "/finite.wav",
			wantDecoder: "*player.navFFmpegStreamer",
			wantPath:    srv.URL + "/finite.wav",
			seekable:    true,
			counted:     true,
			wantLength:  int64(len(wavData)),
		},
		{
			name:        "icy response with content length stays live",
			path:        srv.URL + "/radio-length.aac",
			wantDecoder: "*player.ffmpegPipeStreamer",
			wantPath:    srv.URL + "/radio-length.aac",
			live:        true,
			prefetch:    true,
			counted:     true,
			wantLength:  garbageLen,
		},
		{
			name:        "icy radio in an ffmpeg format",
			path:        srv.URL + "/radio.aac",
			wantDecoder: "*player.ffmpegPipeStreamer",
			wantPath:    srv.URL + "/radio.aac",
			live:        true,
			prefetch:    true,
			counted:     true,
			wantLength:  -1,
		},
		{
			name:        "chunked ogg that is not vorbis",
			path:        srv.URL + "/garbage.ogg",
			wantDecoder: "*player.ffmpegPipeStreamer",
			wantPath:    srv.URL + "/garbage.ogg",
			prefetch:    true,
		},
		{
			name:        "icy ogg that is not vorbis",
			path:        srv.URL + "/radio.ogg",
			wantDecoder: "*player.ffmpegPipeStreamer",
			wantPath:    srv.URL + "/radio.ogg",
			live:        true,
			prefetch:    true,
		},
		{
			name:        "chunked native format",
			path:        srv.URL + "/chunked.wav",
			wantDecoder: "*wav.decoder",
			wantPath:    srv.URL + "/chunked.wav",
			prefetch:    true,
			counted:     true,
			wantLength:  -1,
		},
		{
			name:        "chunked native decode failure",
			path:        srv.URL + "/garbage.mp3",
			wantDecoder: "*player.ffmpegPipeStreamer",
			wantPath:    srv.URL + "/garbage.mp3",
			prefetch:    true,
		},
		{
			name:        "local ffmpeg format",
			path:        filepath.Join(fixtures, "track.m4a"),
			wantDecoder: "*player.localFFmpegStreamer",
			wantPath:    filepath.Join(fixtures, "track.m4a"),
			seekable:    true,
		},
		{
			name:        "local native format",
			path:        filepath.Join(fixtures, "tone.wav"),
			wantDecoder: "*wav.decoder",
			wantPath:    filepath.Join(fixtures, "tone.wav"),
			seekable:    true,
			wantLength:  -1,
		},
		{
			name:        "local native decode failure",
			path:        filepath.Join(fixtures, "bad.wav"),
			wantDecoder: "*player.localFFmpegStreamer",
			wantPath:    filepath.Join(fixtures, "bad.wav"),
			seekable:    true,
		},
		{
			name:    "ssh source in an ffmpeg format",
			path:    "ssh://host/music/track.m4a",
			wantErr: "SSH streaming does not support .m4a format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Player{sr: beep.SampleRate(44100), bitDepth: 16, resampleQuality: 1}
			if tt.register != nil {
				tt.register(p)
			}

			tp, err := p.buildPipeline(tt.path)
			if tt.wantErr != "" {
				if tp != nil {
					tp.close()
				}
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("buildPipeline() error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("buildPipeline() error = %v", err)
			}
			defer tp.close()

			got := fmt.Sprintf("decoder=%T path=%q seekable=%v live=%v prefetch=%v counted=%v length=%d duration=%v",
				tp.decoder, tp.path, tp.seekable, tp.live, tp.livePrefetch != nil, tp.bytesRead != nil, tp.contentLength, tp.knownDuration)
			want := fmt.Sprintf("decoder=%s path=%q seekable=%v live=%v prefetch=%v counted=%v length=%d duration=%v",
				tt.wantDecoder, tt.wantPath, tt.seekable, tt.live, tt.prefetch, tt.counted, tt.wantLength, tt.wantDuration)
			if got != want {
				t.Errorf("pipeline:\n got %s\nwant %s", got, want)
			}
			if tp.stream == nil {
				t.Error("pipeline has no stream")
			}
			if tp.format.SampleRate == 0 {
				t.Error("pipeline has no format")
			}
		})
	}
}
