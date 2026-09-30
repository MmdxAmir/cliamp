package ui

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// fakeLuaHost records the calls that the Visualizer makes to a LuaVisHost.
type fakeLuaHost struct {
	calls []string
	bands [DefaultSpectrumBands]float64
}

func (h *fakeLuaHost) RenderVis(name string, bands [DefaultSpectrumBands]float64, rows, cols int, _ uint64) string {
	h.bands = bands
	return name
}

func (h *fakeLuaHost) InitVis(name string, rows, cols int) {
	h.calls = append(h.calls, fmt.Sprintf("init %s %dx%d", name, rows, cols))
}

func (h *fakeLuaHost) DestroyVis(name string) {
	h.calls = append(h.calls, "destroy "+name)
}

// newLuaTestVisualizer returns a sized Visualizer with the Lua modes a and b.
func newLuaTestVisualizer(t *testing.T, host LuaVisHost) *Visualizer {
	t.Helper()
	v := NewVisualizer(44100)
	v.Rows, v.Cols = 8, 40
	v.RegisterLuaVisualizers([]string{"a", "b"}, host)
	t.Cleanup(func() {
		delete(visNameMap, "a")
		delete(visNameMap, "b")
	})
	return v
}

// A Lua mode gets its init before the first frame after it is selected, and
// its destroy when the mode is left after an init. A mode that is left
// before it drew a frame gets neither.
func TestLuaModeInitAndDestroy(t *testing.T) {
	const (
		modeA = VisCount
		modeB = VisCount + 1
	)
	tests := []struct {
		name  string
		steps func(v *Visualizer)
		want  []string
	}{
		{
			name: "select, draw twice, leave",
			steps: func(v *Visualizer) {
				v.SetMode(modeA)
				v.Render()
				v.Render()
				v.SetMode(VisBars)
				v.Render()
			},
			want: []string{"init a 8x40", "destroy a"},
		},
		{
			name: "switch from one Lua mode to another",
			steps: func(v *Visualizer) {
				v.SetMode(modeA)
				v.Render()
				v.SetMode(modeB)
				v.Render()
			},
			want: []string{"init a 8x40", "destroy a", "init b 8x40"},
		},
		{
			name: "leave before the first frame",
			steps: func(v *Visualizer) {
				v.SetMode(modeA)
				v.TickInterval(VisTickContext{})
				v.SetMode(VisBars)
				v.Render()
			},
			want: nil,
		},
		{
			name: "cycle through a mode and back",
			steps: func(v *Visualizer) {
				v.SetMode(modeA)
				v.Render()
				v.SetMode(VisBars)
				v.Render()
				v.SetMode(modeA)
				v.Render()
			},
			want: []string{"init a 8x40", "destroy a", "init a 8x40"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host := &fakeLuaHost{}
			v := newLuaTestVisualizer(t, host)
			tt.steps(v)
			if !reflect.DeepEqual(host.calls, tt.want) {
				t.Fatalf("calls = %q, want %q", host.calls, tt.want)
			}
		})
	}
}

// A Lua mode with no host draws a blank frame and does not panic.
func TestLuaModeWithoutHost(t *testing.T) {
	v := newLuaTestVisualizer(t, nil)
	v.SetMode(VisCount)
	if got := strings.TrimSpace(v.Render()); got != "" {
		t.Fatalf("Render() = %q, want a blank frame", got)
	}
	v.SetMode(VisBars)
	v.Render()
}
