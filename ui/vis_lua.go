package ui

import "strings"

// LuaVisRenderer is the callback type for rendering a Lua visualizer frame.
type LuaVisRenderer func(name string, bands [DefaultSpectrumBands]float64, rows, cols int, frame uint64) string

// RegisterLuaVisualizers adds Lua visualizer names so they can be cycled
// through with the v key. renderer is called when a Lua visualizer is active.
func (v *Visualizer) RegisterLuaVisualizers(names []string, renderer LuaVisRenderer) {
	v.luaVisNames = names
	v.luaRender = renderer
	clear(v.luaDriverCache)
	// Add to name map for StringToVisModeExact lookups.
	for i, name := range names {
		visNameMap[strings.ToLower(name)] = VisCount + VisMode(i)
	}
}

type luaModeDriver struct {
	spectrumDriverBase
	index int
}

func (d *luaModeDriver) Render(v *Visualizer) string {
	if v == nil || d.index < 0 || d.index >= len(v.luaVisNames) || v.luaRender == nil {
		return ""
	}
	return v.luaRender(v.luaVisNames[d.index], luaBands(v.bands), v.Rows, v.columns(), v.frame)
}

func (d *luaModeDriver) Tick(v *Visualizer, ctx VisTickContext) {
	defaultDriverTick(v, ctx, d.AnalysisSpec(v))
}

func (*luaModeDriver) OnEnter(*Visualizer) {}

func luaBands(src []float64) [DefaultSpectrumBands]float64 {
	var bands [DefaultSpectrumBands]float64
	copy(bands[:], src)
	return bands
}
