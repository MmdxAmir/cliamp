//go:build linux

package mediactl

import (
	"io"
	"math"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/prop"

	"github.com/bjarneo/cliamp/internal/playback"
)

// discardTransport is a D-Bus transport that drops every write. It lets
// the tests run the godbus property code without a session bus.
type discardTransport struct{}

func (discardTransport) Read([]byte) (int, error)    { return 0, io.EOF }
func (discardTransport) Write(p []byte) (int, error) { return len(p), nil }
func (discardTransport) Close() error                { return nil }

func newTestService(t *testing.T, send func(tea.Msg)) *Service {
	t.Helper()
	conn, err := dbus.NewConn(discardTransport{})
	if err != nil {
		t.Fatalf("dbus.NewConn() error = %v", err)
	}
	svc, err := newService(conn, send)
	if err != nil {
		t.Fatalf("newService() error = %v", err)
	}
	t.Cleanup(svc.Close)
	return svc
}

// returnsWithin fails the test when fn does not return within 2 seconds.
func returnsWithin(t *testing.T, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		fn()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("%s did not return while send was blocked", what)
	}
}

// TestServiceCallbacksDoNotWaitForSend holds send blocked, as prog.Send
// blocks until the event loop reads the message. Each D-Bus call and the
// event loop Update must still return, and the messages must keep their order.
func TestServiceCallbacksDoNotWaitForSend(t *testing.T) {
	const playerName = "org.mpris.MediaPlayer2.Player"
	type call struct {
		name    string
		do      func(*Service) *dbus.Error
		wantErr *dbus.Error
	}
	setVolume := func(v float64) call {
		return call{name: "Set Volume", do: func(s *Service) *dbus.Error {
			return s.props.Set(playerName, "Volume", dbus.MakeVariant(v))
		}}
	}
	setNaNVolume := setVolume(math.NaN())
	setNaNVolume.wantErr = prop.ErrInvalidArg
	next := call{name: "Next", do: func(s *Service) *dbus.Error { return playerIface{s}.Next() }}
	playPause := call{name: "PlayPause", do: func(s *Service) *dbus.Error { return playerIface{s}.PlayPause() }}
	seek := call{name: "Seek", do: func(s *Service) *dbus.Error { return playerIface{s}.DoSeek(5_000_000) }}

	tests := []struct {
		name  string
		calls []call
		want  []tea.Msg
	}{
		{
			name:  "one volume change",
			calls: []call{setVolume(0.5)},
			want:  []tea.Msg{playback.SetVolumeMsg{VolumeDB: linearToDb(0.5, initialVolumeFloor)}},
		},
		{
			name:  "rapid volume changes keep order",
			calls: []call{setVolume(0.2), setVolume(0.9), setVolume(0.5), setVolume(0.7)},
			want: []tea.Msg{
				playback.SetVolumeMsg{VolumeDB: linearToDb(0.2, initialVolumeFloor)},
				playback.SetVolumeMsg{VolumeDB: linearToDb(0.9, initialVolumeFloor)},
				playback.SetVolumeMsg{VolumeDB: linearToDb(0.5, initialVolumeFloor)},
				playback.SetVolumeMsg{VolumeDB: linearToDb(0.7, initialVolumeFloor)},
			},
		},
		{
			name:  "out of range volume clamps",
			calls: []call{setVolume(1.5), setVolume(-0.5)},
			want: []tea.Msg{
				playback.SetVolumeMsg{VolumeDB: linearToDb(1, initialVolumeFloor)},
				playback.SetVolumeMsg{VolumeDB: linearToDb(0, initialVolumeFloor)},
			},
		},
		{
			name:  "NaN volume sends nothing",
			calls: []call{setVolume(0.3), setNaNVolume, setVolume(0.6)},
			want: []tea.Msg{
				playback.SetVolumeMsg{VolumeDB: linearToDb(0.3, initialVolumeFloor)},
				playback.SetVolumeMsg{VolumeDB: linearToDb(0.6, initialVolumeFloor)},
			},
		},
		{
			name:  "methods and volume keep order",
			calls: []call{next, setVolume(0.3), playPause, seek, setVolume(0.6)},
			want: []tea.Msg{
				playback.NextMsg{},
				playback.SetVolumeMsg{VolumeDB: linearToDb(0.3, initialVolumeFloor)},
				playback.PlayPauseMsg{},
				playback.SeekMsg{Offset: 5 * time.Second},
				playback.SetVolumeMsg{VolumeDB: linearToDb(0.6, initialVolumeFloor)},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			release := make(chan struct{})
			got := make(chan tea.Msg, len(tt.want))
			svc := newTestService(t, func(msg tea.Msg) {
				<-release
				got <- msg
			})

			for _, c := range tt.calls {
				returnsWithin(t, c.name, func() {
					if err := c.do(svc); err != c.wantErr {
						t.Errorf("%s error = %v, want %v", c.name, err, c.wantErr)
					}
				})
			}
			// The event loop calls Update, which takes the godbus
			// Properties lock that the Volume Set held.
			returnsWithin(t, "Update", func() {
				svc.Update(playback.State{Status: playback.StatusPlaying, VolumeDB: -12, VolumeMinDB: -50})
			})

			close(release)
			for i, want := range tt.want {
				select {
				case msg := <-got:
					if msg != want {
						t.Fatalf("message %d = %#v, want %#v", i, msg, want)
					}
				case <-time.After(2 * time.Second):
					t.Fatalf("message %d not sent, want %#v", i, want)
				}
			}
			select {
			case msg := <-got:
				t.Fatalf("unexpected extra message %#v", msg)
			case <-time.After(50 * time.Millisecond):
			}
		})
	}
}

// TestServiceFirstUpdatePublishesState checks that the first Update
// publishes Volume and CanSeek, also when the new value is the Go zero value.
func TestServiceFirstUpdatePublishesState(t *testing.T) {
	const playerName = "org.mpris.MediaPlayer2.Player"
	tests := []struct {
		name        string
		state       playback.State
		wantVolume  float64
		wantCanSeek bool
	}{
		{name: "muted and not seekable", state: playback.State{VolumeDB: -50, VolumeMinDB: -50}, wantVolume: 0, wantCanSeek: false},
		{name: "full volume and seekable", state: playback.State{VolumeDB: 6, VolumeMinDB: -50, Seekable: true}, wantVolume: 1, wantCanSeek: true},
		{name: "0 dB and seekable", state: playback.State{VolumeDB: 0, VolumeMinDB: -50, Seekable: true}, wantVolume: linearAt(0), wantCanSeek: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestService(t, func(tea.Msg) {})
			svc.Update(tt.state)
			if got := svc.props.GetMust(playerName, "Volume"); got != tt.wantVolume {
				t.Errorf("Volume = %v, want %v", got, tt.wantVolume)
			}
			if got := svc.props.GetMust(playerName, "CanSeek"); got != tt.wantCanSeek {
				t.Errorf("CanSeek = %v, want %v", got, tt.wantCanSeek)
			}
		})
	}
}

// TestServiceVolumeUsesStateFloor checks that the Volume property and a
// Volume Set use the engine floor from the last Update. Before the first
// Update, a Volume Set uses the player default floor.
func TestServiceVolumeUsesStateFloor(t *testing.T) {
	const playerName = "org.mpris.MediaPlayer2.Player"
	tests := []struct {
		name       string
		noUpdate   bool
		floor      float64
		volumeDB   float64
		wantVolume float64 // Volume property after Update
		wantSetDB  float64 // dB that a Volume Set of 0 sends
	}{
		{name: "no Update yet uses the player default", noUpdate: true, wantSetDB: -50},
		{name: "floor 0", floor: 0, volumeDB: 0, wantVolume: 0, wantSetDB: 0},
		{name: "floor -30", floor: -30, volumeDB: -30, wantVolume: 0, wantSetDB: -30},
		{name: "floor -50", floor: -50, volumeDB: -40, wantVolume: linearAt(-40), wantSetDB: -50},
		{name: "floor -90", floor: -90, volumeDB: -80, wantVolume: linearAt(-80), wantSetDB: -90},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := make(chan tea.Msg, 1)
			svc := newTestService(t, func(msg tea.Msg) { got <- msg })

			if !tt.noUpdate {
				svc.Update(playback.State{VolumeDB: tt.volumeDB, VolumeMinDB: tt.floor})
				if v := svc.props.GetMust(playerName, "Volume").(float64); math.Abs(v-tt.wantVolume) > 1e-12 {
					t.Fatalf("Volume = %v, want %v", v, tt.wantVolume)
				}
			}

			if err := svc.props.Set(playerName, "Volume", dbus.MakeVariant(0.0)); err != nil {
				t.Fatalf("Set Volume error = %v", err)
			}
			select {
			case msg := <-got:
				if want := (playback.SetVolumeMsg{VolumeDB: tt.wantSetDB}); msg != want {
					t.Fatalf("message = %#v, want %#v", msg, want)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("Set Volume sent no message")
			}
		})
	}
}
