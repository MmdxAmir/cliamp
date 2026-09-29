package luaplugin

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// hookTimeout bounds each event hook, key bind, timer and exec callback, and
// each visualizer init or destroy call. It is a var so tests can shorten it.
var hookTimeout = 5 * time.Second

// errClosed is the error for a call into a plugin whose VM is closed.
var errClosed = errors.New("plugin is closed")

// Event name constants.
const (
	EventAppStart      = "app.start"
	EventAppQuit       = "app.quit"
	EventPlaybackState = "playback.state"
	EventTrackChange   = "track.change"
	EventTrackScrobble = "track.scrobble"
	EventPlayerSeek    = "player.seek"   // data: position, duration (seconds)
	EventPlayerVolume  = "player.volume" // data: db
	EventPlayerEQ      = "player.eq"     // data: bands (10-array), preset
	EventPlayerMode    = "player.mode"   // data: shuffle (bool), repeat ("Off"/"All"/"One")
	EventQueueChange   = "queue.change"  // data: count, index, queued
	EventQueueEnd      = "queue.end"     // data: the finished track (same shape as track.change)
	EventPlaybackStop  = "playback.stop" // data: none; an explicit stop by the user, never a queue running out
)

// Permission strings declared via plugin.register({ permissions = {...} }).
// Kept as named constants so the guard call sites and docs don't drift.
const (
	PermControl = "control"
	PermExec    = "exec"
	PermKeymap  = "keymap"
)

// luaHook is a single event callback registered by a plugin.
type luaHook struct {
	plugin *Plugin
	fn     *lua.LFunction
}

// callBuilder returns the Lua function to call and its arguments. call runs
// it under the plugin lock, so it can make tables on L. A nil function skips
// the call.
type callBuilder func(L *lua.LState) (*lua.LFunction, []lua.LValue)

// fixedArgs returns a callBuilder for fn with arguments that need no LState.
func fixedArgs(fn *lua.LFunction, args ...lua.LValue) callBuilder {
	return func(*lua.LState) (*lua.LFunction, []lua.LValue) { return fn, args }
}

// call is the one way Go calls into a plugin's Lua VM. It holds p.mu for the
// whole call because an LState is not safe for concurrent use. It returns
// errClosed after the VM is closed. See callLocked for the rest.
func (m *Manager) call(p *Plugin, label string, timeout time.Duration, nret int, build callBuilder) (lua.LValue, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return m.callLocked(p, label, timeout, nret, build)
}

// callLocked is call for a caller that already holds p.mu. The call stops
// after timeout. When nret > 0, it returns the first result. It logs a Lua
// error under label, but only when the error differs from the last one logged
// for label. Thus a timer or a render that fails each time logs once.
func (m *Manager) callLocked(p *Plugin, label string, timeout time.Duration, nret int, build callBuilder) (lua.LValue, error) {
	if p.closed {
		return lua.LNil, errClosed
	}
	fn, args := build(p.L)
	if fn == nil {
		return lua.LNil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	p.L.SetContext(ctx)
	defer p.L.RemoveContext()

	if err := p.L.CallByParam(lua.P{Fn: fn, NRet: nret, Protect: true}, args...); err != nil {
		// A timeout stops the VM at a different line each time, so key it by
		// the context error and not by the Lua message.
		key := err.Error()
		if ctx.Err() != nil {
			key = ctx.Err().Error()
		}
		if p.lastErr[label] != key {
			if p.lastErr == nil {
				p.lastErr = make(map[string]string)
			}
			p.lastErr[label] = key
			m.logHookErr(p.installName, label, err)
		}
		return lua.LNil, err
	}
	delete(p.lastErr, label)
	if nret == 0 {
		return lua.LNil, nil
	}
	ret := p.L.Get(-nret)
	p.L.Pop(nret)
	return ret, nil
}

// logHookErr records a callback error to stderr and the plugin log.
func (m *Manager) logHookErr(name, label string, err error) {
	log.Printf("[lua:%s] %s error: %v", name, label, err)
	if m.logger != nil {
		m.logger.log(name, "error", "%s error: %v", label, err)
	}
}

// filterOutPlugin returns hooks with all entries owned by p removed. Reuses
// the existing backing slice and zeroes the tail so dropped LFunction pointers
// become garbage-collectible.
func filterOutPlugin(hooks []*luaHook, p *Plugin) []*luaHook {
	filtered := hooks[:0]
	for _, h := range hooks {
		if h.plugin != p {
			filtered = append(filtered, h)
		}
	}
	for i := len(filtered); i < len(hooks); i++ {
		hooks[i] = nil
	}
	return filtered
}

// Emit dispatches an event to all plugins that registered for it.
// Each callback runs in its own goroutine with a timeout. The plugin's
// mutex serializes all LState access so concurrent events are safe.
func (m *Manager) Emit(event string, data map[string]any) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closing {
		return
	}
	hooks := m.hooks[event]
	label := event + " handler"
	for _, h := range hooks {
		// Add under RLock so Close (which sets closing under Lock, then Wait)
		// can never miss an in-flight goroutine: either we register it before
		// closing is set, or we observe closing and skip.
		m.wg.Add(1)
		go func(h *luaHook) {
			defer m.wg.Done()
			m.fire(h, label, data)
		}(h)
	}
}

// EmitSync dispatches an event synchronously, blocking until all callbacks
// finish or time out. Close uses it for app.quit before it closes the VMs.
func (m *Manager) EmitSync(event string, data map[string]any) {
	m.mu.RLock()
	hooks := m.hooks[event]
	m.mu.RUnlock()

	label := event + " handler"
	for _, h := range hooks {
		m.fire(h, label, data)
	}
}

// fire calls an event hook with data as its table argument.
func (m *Manager) fire(h *luaHook, label string, data map[string]any) {
	m.call(h.plugin, label, hookTimeout, 0, func(L *lua.LState) (*lua.LFunction, []lua.LValue) {
		return h.fn, []lua.LValue{dataToTable(L, data)}
	})
}

// dataToTable converts a Go map to a Lua table.
func dataToTable(L *lua.LState, data map[string]any) *lua.LTable {
	tbl := L.NewTable()
	if data == nil {
		return tbl
	}
	for k, v := range data {
		tbl.RawSetString(k, goToLua(L, v))
	}
	return tbl
}

// goToLua converts a Go value to a Lua value.
func goToLua(L *lua.LState, v any) lua.LValue {
	switch val := v.(type) {
	case nil:
		return lua.LNil
	case string:
		return lua.LString(val)
	case int:
		return lua.LNumber(val)
	case int64:
		return lua.LNumber(val)
	case float64:
		return lua.LNumber(val)
	case bool:
		return lua.LBool(val)
	case map[string]any:
		return dataToTable(L, val)
	case []float64:
		tbl := L.NewTable()
		for i, f := range val {
			tbl.RawSetInt(i+1, lua.LNumber(f))
		}
		return tbl
	default:
		return lua.LString(fmt.Sprintf("%v", val))
	}
}
