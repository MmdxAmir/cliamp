//go:build linux

package mediactl

import (
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
	"github.com/godbus/dbus/v5/prop"

	"github.com/bjarneo/cliamp/internal/playback"
)

func Run(prog *tea.Program, svc *Service) (tea.Model, error) {
	return prog.Run()
}

type Service struct {
	conn  *dbus.Conn
	props *prop.Properties
	send  func(tea.Msg)
	mu    sync.Mutex

	queueMu   sync.Mutex
	queue     []tea.Msg     // messages that wait for forwardMessages
	wake      chan struct{} // tells forwardMessages that queue has messages
	done      chan struct{} // closed by Close to stop forwardMessages
	closeOnce sync.Once

	// volFloor holds the math.Float64bits of the last State.VolumeMinDB,
	// or of initialVolumeFloor before the first Update.
	// The Volume callback reads it without mu, because godbus runs the
	// callback under the Properties lock and Update holds mu while it
	// takes that lock.
	volFloor atomic.Uint64

	lastStatus  playback.Status
	lastTrack   playback.Track
	lastVol     float64
	lastCanSeek bool
	trackSeq    int64           // bumped on each track change
	trackID     dbus.ObjectPath // current track's MPRIS object path
}

const introspectXML = `
<node>
  <interface name="org.mpris.MediaPlayer2">
    <method name="Raise"/>
    <method name="Quit"/>
    <property name="Identity" type="s" access="read"/>
    <property name="CanQuit" type="b" access="read"/>
    <property name="CanRaise" type="b" access="read"/>
    <property name="HasTrackList" type="b" access="read"/>
    <property name="SupportedUriSchemes" type="as" access="read"/>
    <property name="SupportedMimeTypes" type="as" access="read"/>
  </interface>
  <interface name="org.mpris.MediaPlayer2.Player">
    <method name="Next"/>
    <method name="Previous"/>
    <method name="Pause"/>
    <method name="PlayPause"/>
    <method name="Stop"/>
    <method name="Play"/>
    <method name="Seek"><arg direction="in" type="x"/></method>
    <method name="SetPosition"><arg direction="in" type="o"/><arg direction="in" type="x"/></method>
    <signal name="Seeked"><arg type="x"/></signal>
    <property name="PlaybackStatus" type="s" access="read"/>
    <property name="Rate" type="d" access="read"/>
    <property name="Metadata" type="a{sv}" access="read"/>
    <property name="Volume" type="d" access="readwrite"/>
    <property name="Position" type="x" access="read"/>
    <property name="MinimumRate" type="d" access="read"/>
    <property name="MaximumRate" type="d" access="read"/>
    <property name="CanGoNext" type="b" access="read"/>
    <property name="CanGoPrevious" type="b" access="read"/>
    <property name="CanPlay" type="b" access="read"/>
    <property name="CanPause" type="b" access="read"/>
    <property name="CanSeek" type="b" access="read"/>
    <property name="CanControl" type="b" access="read"/>
  </interface>
` + introspect.IntrospectDataString + `</node>`

type root struct{ svc *Service }

func (r root) Raise() *dbus.Error { return nil }
func (r root) Quit() *dbus.Error {
	r.svc.dispatch(playback.QuitMsg{})
	return nil
}

type playerIface struct{ svc *Service }

func (p playerIface) Next() *dbus.Error {
	p.svc.dispatch(playback.NextMsg{})
	return nil
}

func (p playerIface) Previous() *dbus.Error {
	p.svc.dispatch(playback.PrevMsg{})
	return nil
}

func (p playerIface) Pause() *dbus.Error {
	p.svc.dispatch(playback.PauseMsg{})
	return nil
}

func (p playerIface) PlayPause() *dbus.Error {
	p.svc.dispatch(playback.PlayPauseMsg{})
	return nil
}

func (p playerIface) Stop() *dbus.Error {
	p.svc.dispatch(playback.StopMsg{})
	return nil
}

func (p playerIface) Play() *dbus.Error {
	p.svc.dispatch(playback.PlayMsg{})
	return nil
}

func (p playerIface) DoSeek(offset int64) *dbus.Error {
	p.svc.dispatch(playback.SeekMsg{Offset: time.Duration(offset) * time.Microsecond})
	return nil
}

func (p playerIface) SetPosition(trackID dbus.ObjectPath, position int64) *dbus.Error {
	// Ignore a seek aimed at a track that is no longer current (the MPRIS
	// spec treats a mismatched TrackId as stale).
	p.svc.mu.Lock()
	cur := p.svc.trackID
	p.svc.mu.Unlock()
	if trackID != cur {
		return nil
	}
	p.svc.dispatch(playback.SetPositionMsg{Position: time.Duration(position) * time.Microsecond})
	return nil
}

func New(send func(tea.Msg)) (*Service, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, fmt.Errorf("mpris: session bus: %w", err)
	}

	reply, err := conn.RequestName("org.mpris.MediaPlayer2.cliamp",
		dbus.NameFlagDoNotQueue)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("mpris: request name: %w", err)
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		conn.Close()
		return nil, fmt.Errorf("mpris: name already taken")
	}

	svc, err := newService(conn, send)
	if err != nil {
		conn.Close()
		return nil, err
	}
	return svc, nil
}

// initialVolumeFloor is the player default volume_min in dB. A Volume Set
// uses it until the first Update gives the engine floor.
const initialVolumeFloor = -50

// newService exports the MPRIS objects and properties on conn. It starts
// the goroutine that forwards the queued messages to send.
func newService(conn *dbus.Conn, send func(tea.Msg)) (*Service, error) {
	// lastVol and lastCanSeek are the initial property values, so that
	// Update publishes the first state that differs from them.
	svc := &Service{
		conn:        conn,
		send:        send,
		wake:        make(chan struct{}, 1),
		done:        make(chan struct{}),
		lastVol:     1.0,
		lastCanSeek: true,
		trackSeq:    1,
		trackID:     trackPath(1),
	}
	svc.volFloor.Store(math.Float64bits(initialVolumeFloor))
	path := dbus.ObjectPath("/org/mpris/MediaPlayer2")

	if err := conn.Export(root{svc}, path, "org.mpris.MediaPlayer2"); err != nil {
		return nil, fmt.Errorf("mpris: export root: %w", err)
	}
	if err := conn.ExportWithMap(playerIface{svc}, map[string]string{
		"DoSeek": "Seek",
	}, path, "org.mpris.MediaPlayer2.Player"); err != nil {
		return nil, fmt.Errorf("mpris: export player: %w", err)
	}
	if err := conn.Export(introspect.Introspectable(introspectXML), path,
		"org.freedesktop.DBus.Introspectable"); err != nil {
		return nil, fmt.Errorf("mpris: export introspect: %w", err)
	}

	propsSpec := map[string]map[string]*prop.Prop{
		"org.mpris.MediaPlayer2": {
			"Identity":            {Value: "Cliamp", Writable: false, Emit: prop.EmitTrue},
			"CanQuit":             {Value: true, Writable: false, Emit: prop.EmitTrue},
			"CanRaise":            {Value: false, Writable: false, Emit: prop.EmitTrue},
			"HasTrackList":        {Value: false, Writable: false, Emit: prop.EmitTrue},
			"SupportedUriSchemes": {Value: []string{}, Writable: false, Emit: prop.EmitTrue},
			"SupportedMimeTypes":  {Value: []string{}, Writable: false, Emit: prop.EmitTrue},
		},
		"org.mpris.MediaPlayer2.Player": {
			"PlaybackStatus": {Value: string(playback.StatusStopped), Writable: false, Emit: prop.EmitTrue},
			"Metadata":       {Value: makeMetadata(playback.Track{}, svc.trackID), Writable: false, Emit: prop.EmitTrue},
			"Volume": {Value: svc.lastVol, Writable: true, Emit: prop.EmitTrue, Callback: func(c *prop.Change) *dbus.Error {
				v, ok := c.Value.(float64)
				if !ok {
					return nil
				}
				if v < 0 {
					v = 0
				}
				if v > 1 {
					v = 1
				}
				// godbus holds the Properties lock while it runs this callback,
				// and Update needs that lock on the event loop. prog.Send blocks
				// until the event loop reads the message, so a direct send can
				// deadlock the TUI. dispatch only queues the message. The queue
				// is filled under the lock, so it keeps the order of the changes.
				floor := math.Float64frombits(svc.volFloor.Load())
				svc.dispatch(playback.SetVolumeMsg{VolumeDB: linearToDb(v, floor)})
				return nil
			}},
			"Position":      {Value: int64(0), Writable: false, Emit: prop.EmitFalse},
			"Rate":          {Value: 1.0, Writable: false, Emit: prop.EmitTrue},
			"MinimumRate":   {Value: 1.0, Writable: false, Emit: prop.EmitTrue},
			"MaximumRate":   {Value: 1.0, Writable: false, Emit: prop.EmitTrue},
			"CanControl":    {Value: true, Writable: false, Emit: prop.EmitTrue},
			"CanPlay":       {Value: true, Writable: false, Emit: prop.EmitTrue},
			"CanPause":      {Value: true, Writable: false, Emit: prop.EmitTrue},
			"CanGoNext":     {Value: true, Writable: false, Emit: prop.EmitTrue},
			"CanGoPrevious": {Value: true, Writable: false, Emit: prop.EmitTrue},
			"CanSeek":       {Value: svc.lastCanSeek, Writable: false, Emit: prop.EmitTrue},
		},
	}

	props, err := prop.Export(conn, path, propsSpec)
	if err != nil {
		return nil, fmt.Errorf("mpris: export props: %w", err)
	}
	svc.props = props

	go svc.forwardMessages()
	return svc, nil
}

// dispatch queues msg for send and returns at once. D-Bus handlers use it
// so that no handler waits for the event loop.
func (s *Service) dispatch(msg tea.Msg) {
	s.queueMu.Lock()
	s.queue = append(s.queue, msg)
	s.queueMu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// forwardMessages calls send for each queued message on one goroutine, in
// the order that dispatch queued them.
func (s *Service) forwardMessages() {
	for {
		select {
		case <-s.done:
			return
		case <-s.wake:
		}
		s.queueMu.Lock()
		msgs := s.queue
		s.queue = nil
		s.queueMu.Unlock()
		for _, msg := range msgs {
			s.send(msg)
		}
	}
}

func (s *Service) Update(state playback.State) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.props == nil {
		return
	}

	iface := "org.mpris.MediaPlayer2.Player"

	if state.Status != s.lastStatus {
		s.props.SetMust(iface, "PlaybackStatus", string(state.Status))
		s.lastStatus = state.Status
	}

	if state.Track != s.lastTrack {
		s.trackSeq++
		s.trackID = trackPath(s.trackSeq)
		s.props.SetMust(iface, "Metadata", makeMetadata(state.Track, s.trackID))
		s.lastTrack = state.Track
	}

	s.volFloor.Store(math.Float64bits(state.VolumeMinDB))
	vol := dbToLinear(state.VolumeDB, state.VolumeMinDB)
	if vol != s.lastVol {
		s.props.SetMust(iface, "Volume", vol)
		s.lastVol = vol
	}

	s.props.SetMust(iface, "Position", state.Position.Microseconds())

	if state.Seekable != s.lastCanSeek {
		s.props.SetMust(iface, "CanSeek", state.Seekable)
		s.lastCanSeek = state.Seekable
	}
}

func (s *Service) Seeked(position time.Duration) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		return
	}
	s.conn.Emit(
		dbus.ObjectPath("/org/mpris/MediaPlayer2"),
		"org.mpris.MediaPlayer2.Player.Seeked",
		position.Microseconds(),
	)
}

func (s *Service) Close() {
	if s == nil {
		return
	}
	s.closeOnce.Do(func() { close(s.done) })
	if s.conn != nil {
		s.conn.Close()
	}
}
