package luaplugin

import (
	"sync/atomic"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// renderTimeout bounds one render call. RenderVis runs on the UI goroutine,
// so a slow render must not delay the frame. It is a var so tests can change
// it.
var renderTimeout = 20 * time.Millisecond

// luaVis wraps a Lua visualizer plugin, caching function references
// for render() and optional init()/destroy() callbacks.
type luaVis struct {
	name    string
	plugin  *Plugin // owns the LState and mutex
	obj     *lua.LTable
	render  *lua.LFunction
	init    *lua.LFunction
	destroy *lua.LFunction
	last    atomic.Value // string: the previous frame, reused on error or while the plugin is busy
}

// registerVisPlugin is called during plugin.register() for type="visualizer".
func (m *Manager) registerVisPlugin(L *lua.LState, obj *lua.LTable, p *Plugin) {
	vis := &luaVis{
		name:   p.Name,
		plugin: p,
		obj:    obj,
	}

	m.mu.Lock()
	m.visPlugs = append(m.visPlugs, vis)
	m.visMap[p.Name] = vis
	m.mu.Unlock()
}

// finalizeVisualizers is called after all plugins are loaded to resolve
// render/init/destroy function references from the plugin objects.
func (m *Manager) finalizeVisualizers() {
	for _, vis := range m.visPlugs {
		if fn, ok := vis.obj.RawGetString("render").(*lua.LFunction); ok {
			vis.render = fn
		}
		if fn, ok := vis.obj.RawGetString("init").(*lua.LFunction); ok {
			vis.init = fn
		}
		if fn, ok := vis.obj.RawGetString("destroy").(*lua.LFunction); ok {
			vis.destroy = fn
		}
	}
}

// Visualizers returns the names of all Lua visualizer plugins.
func (m *Manager) Visualizers() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make([]string, len(m.visPlugs))
	for i, v := range m.visPlugs {
		names[i] = v.name
	}
	return names
}

// InitVis calls a Lua visualizer's init(rows, cols) if it exists.
func (m *Manager) InitVis(name string, rows, cols int) {
	m.mu.RLock()
	vis, ok := m.visMap[name]
	m.mu.RUnlock()
	if !ok || vis.init == nil {
		return
	}
	m.call(vis.plugin, "init", hookTimeout, 0, fixedArgs(vis.init, vis.obj, lua.LNumber(rows), lua.LNumber(cols)))
}

// DestroyVis calls a Lua visualizer's destroy() if it exists.
func (m *Manager) DestroyVis(name string) {
	m.mu.RLock()
	vis, ok := m.visMap[name]
	m.mu.RUnlock()
	if !ok || vis.destroy == nil {
		return
	}
	m.call(vis.plugin, "destroy", hookTimeout, 0, fixedArgs(vis.destroy, vis.obj))
}

// RenderVis calls a Lua visualizer's render(bands, frame, rows, cols) and
// returns the terminal text. It runs on the UI goroutine, so it never waits:
// it returns the previous frame when another callback of the plugin holds the
// lock, when render fails, or when render runs past renderTimeout.
func (m *Manager) RenderVis(name string, bands [10]float64, rows, cols int, frame uint64) string {
	m.mu.RLock()
	vis, ok := m.visMap[name]
	m.mu.RUnlock()
	if !ok || vis.render == nil {
		return ""
	}

	if vis.plugin.mu.TryLock() {
		ret, _ := m.callLocked(vis.plugin, renderLabel, renderTimeout, 1, func(L *lua.LState) (*lua.LFunction, []lua.LValue) {
			return vis.render, []lua.LValue{vis.obj, floatsToTable(L, bands[:]), lua.LNumber(frame), lua.LNumber(rows), lua.LNumber(cols)}
		})
		vis.plugin.mu.Unlock()
		if str, ok := ret.(lua.LString); ok {
			vis.last.Store(string(str))
		}
	}
	last, _ := vis.last.Load().(string)
	return last
}
