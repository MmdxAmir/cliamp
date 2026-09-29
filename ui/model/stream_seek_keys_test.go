package model

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/internal/playback"
)

// fakeEngine plays an hour-long track. It overrides only the stream-seek
// methods of playbackFakeEngine.
type fakeEngine struct {
	playbackFakeEngine
	streamSeek bool
}

func newFakeEngine(streamSeek bool, position time.Duration) *fakeEngine {
	return &fakeEngine{
		playbackFakeEngine: playbackFakeEngine{playing: true, duration: time.Hour, position: position},
		streamSeek:         streamSeek,
	}
}

func (f *fakeEngine) Seekable() bool     { return f.streamSeek }
func (f *fakeEngine) IsStreamSeek() bool { return f.streamSeek }

func assertStreamSeekCmd(t *testing.T, eng *fakeEngine, cmd tea.Cmd, want time.Duration) {
	t.Helper()

	if cmd == nil {
		t.Fatal("cmd = nil, want seek cmd for HTTP stream")
	}

	msg := cmd()
	if _, ok := msg.(seekTickMsg); !ok {
		t.Fatalf("cmd() msg = %T, want seekTickMsg", msg)
	}

	if len(eng.seekCalls) != 1 {
		t.Fatalf("Seek call count after cmd() = %d, want 1", len(eng.seekCalls))
	}
	if got := eng.seekCalls[0]; got != want {
		t.Fatalf("Seek arg = %v, want %v", got, want)
	}
}

func assertDeferredStreamSeek(t *testing.T, eng *fakeEngine, cmd tea.Cmd, position, want time.Duration) {
	t.Helper()

	if len(eng.seekCalls) != 0 {
		t.Fatalf("Seek call count before cmd() = %d, want 0", len(eng.seekCalls))
	}

	eng.position = position
	assertStreamSeekCmd(t, eng, cmd, want)
}

func TestDeferredHTTPStreamSeek(t *testing.T) {
	cases := []struct {
		name       string
		initialPos time.Duration
		settlePos  time.Duration
		want       time.Duration
		invoke     func(*Model) tea.Cmd
		check      func(*testing.T, *Model)
	}{
		{
			name:      "right key",
			settlePos: 8 * time.Second,
			want:      5 * time.Second,
			invoke: func(m *Model) tea.Cmd {
				return m.handleKey(tea.KeyPressMsg{Code: tea.KeyRight})
			},
		},
		{
			name:       "set position update",
			initialPos: 3 * time.Second,
			settlePos:  5 * time.Second,
			want:       5 * time.Second,
			invoke: func(m *Model) tea.Cmd {
				_, cmd := m.Update(playback.SetPositionMsg{Position: 10 * time.Second})
				return cmd
			},
		},
		{
			name:       "jump enter",
			initialPos: 3 * time.Second,
			settlePos:  5 * time.Second,
			want:       5 * time.Second,
			invoke: func(m *Model) tea.Cmd {
				m.jumping = true
				m.jumpInput = "10"
				return m.handleJumpKey(tea.KeyPressMsg{Code: tea.KeyEnter})
			},
			check: func(t *testing.T, m *Model) {
				t.Helper()
				if m.jumping {
					t.Fatal("jump mode remained active after enter")
				}
				if m.jumpInput != "" {
					t.Fatalf("jump input = %q, want empty", m.jumpInput)
				}
			},
		},
		{
			name:       "seek message",
			initialPos: 3 * time.Second,
			settlePos:  5 * time.Second,
			want:       4 * time.Second,
			invoke: func(m *Model) tea.Cmd {
				_, cmd := m.Update(playback.SeekMsg{Offset: 4 * time.Second})
				return cmd
			},
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			eng := newFakeEngine(true, tt.initialPos)
			m := Model{player: eng}

			cmd := tt.invoke(&m)
			if tt.check != nil {
				tt.check(t, &m)
			}
			assertDeferredStreamSeek(t, eng, cmd, tt.settlePos, tt.want)
		})
	}
}
