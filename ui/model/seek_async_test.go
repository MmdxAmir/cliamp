package model

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/internal/playback"
	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/playlist"
)

// runSeekCmd runs cmd and the commands of any batch it returns, and gives
// back the first seekTickMsg.
func runSeekCmd(t *testing.T, cmd tea.Cmd) seekTickMsg {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		switch msg := c().(type) {
		case seekTickMsg:
			return msg
		case tea.BatchMsg:
			queue = append(queue, msg...)
		}
	}
	t.Fatal("no command returned a seekTickMsg")
	return seekTickMsg{}
}

// TestYTDLSeekEntryPointsRunAsync checks that no seek entry point restarts a
// yt-dlp pipeline on the Update goroutine.
func TestYTDLSeekEntryPointsRunAsync(t *testing.T) {
	track := playlist.Track{Title: "Video", Path: "https://www.youtube.com/watch?v=abc", Stream: true, DurationSecs: 3600}
	cases := []struct {
		name      string
		invoke    func(*Model) tea.Cmd
		wantDelta time.Duration
		check     func(*testing.T, *Model)
	}{
		{
			name:      "prev key rewinds",
			invoke:    func(m *Model) tea.Cmd { return m.handleKey(tea.KeyPressMsg{Code: '<', Text: "<"}) },
			wantDelta: -10 * time.Second,
		},
		{
			name: "prev message rewinds",
			invoke: func(m *Model) tea.Cmd {
				updated, cmd := m.Update(playback.PrevMsg{})
				*m = updated.(Model)
				return cmd
			},
			wantDelta: -10 * time.Second,
		},
		{
			name: "jump enter",
			invoke: func(m *Model) tea.Cmd {
				m.jumping = true
				m.jumpInput = "1:00"
				return m.handleJumpKey(tea.KeyPressMsg{Code: tea.KeyEnter})
			},
			wantDelta: 50 * time.Second,
			check: func(t *testing.T, m *Model) {
				t.Helper()
				if m.jumping {
					t.Fatal("jump mode remained active after enter")
				}
			},
		},
		{
			name: "seek message",
			invoke: func(m *Model) tea.Cmd {
				updated, cmd := m.Update(playback.SeekMsg{Offset: 30 * time.Second})
				*m = updated.(Model)
				return cmd
			},
			wantDelta: 30 * time.Second,
		},
		{
			name: "set position message",
			invoke: func(m *Model) tea.Cmd {
				updated, cmd := m.Update(playback.SetPositionMsg{Position: 2 * time.Minute})
				*m = updated.(Model)
				return cmd
			},
			wantDelta: 110 * time.Second,
		},
		{
			name: "v2 seek",
			invoke: func(m *Model) tea.Cmd {
				updated, cmd := m.Update(v2Request(t, "seek", ipc.Request{Value: 30}))
				*m = updated.(Model)
				return cmd
			},
			wantDelta: 30 * time.Second,
		},
		{
			name: "v2 seek.absolute",
			invoke: func(m *Model) tea.Cmd {
				updated, cmd := m.Update(v2Request(t, "seek.absolute", ipc.Request{Value: 120}))
				*m = updated.(Model)
				return cmd
			},
			wantDelta: 110 * time.Second,
		},
		{
			name: "resume",
			invoke: func(m *Model) tea.Cmd {
				m.SetResume(track.Path, 90)
				return m.applyResume()
			},
			wantDelta: 80 * time.Second,
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			eng := &playbackFakeEngine{
				playing:  true,
				ytdlSeek: true,
				seekable: true,
				position: 10 * time.Second,
				duration: time.Hour,
			}
			pl := playlist.New()
			pl.Add(track)
			pl.SetIndex(0)
			m := Model{player: eng, playlist: pl, playingTrack: track, playingTrackActive: true}

			cmd := tt.invoke(&m)
			if len(eng.seekCalls) != 0 || len(eng.seekYTDLCalls) != 0 {
				t.Fatalf("inline seeks: Seek %v, SeekYTDL %v, want none", eng.seekCalls, eng.seekYTDLCalls)
			}
			if cmd == nil {
				t.Fatal("cmd = nil, want an async seek command")
			}
			if tt.check != nil {
				tt.check(t, &m)
			}
			msg := runSeekCmd(t, cmd)
			if msg.err != nil {
				t.Fatalf("seek error = %v", msg.err)
			}
			if len(eng.seekYTDLCalls) != 1 || eng.seekYTDLCalls[0] != tt.wantDelta {
				t.Fatalf("SeekYTDL calls = %v, want [%v]", eng.seekYTDLCalls, tt.wantDelta)
			}
		})
	}
}
