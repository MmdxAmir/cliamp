package ui

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

// visWidthTrace runs mode at 20 columns through a short play, fade and pause
// schedule while the PanelWidth global holds panelWidth. It records every
// frame and every cadence answer the model reads between frames.
func visWidthTrace(t *testing.T, mode VisMode, panelWidth int) string {
	t.Helper()
	prev := PanelWidth
	PanelWidth = panelWidth
	t.Cleanup(func() { PanelWidth = prev })

	v := NewVisualizer(44100)
	v.Mode = mode
	v.Cols = 20
	v.Rows = 5

	var trace strings.Builder
	now := time.Unix(1_700_000_000, 0)
	for frame := range 24 {
		now = now.Add(TickAnim)
		playing := frame < 12
		ctx := VisTickContext{
			Now:     now,
			Playing: playing,
			Paused:  frame >= 18,
			Analyze: func(spec VisAnalysisSpec) []float64 {
				spec = NormalizeAnalysisSpec(spec)
				buf := make([]float64, spec.FFTSize)
				if playing {
					for i := range buf {
						buf[i] = 0.8 * math.Sin(2*math.Pi*float64(i*(3+frame%4))/400)
					}
				}
				return v.Analyze(buf, spec)
			},
			StereoSamplesInto: func(dst [][2]float64) int {
				for i := range dst {
					dst[i] = [2]float64{}
					if playing {
						dst[i] = [2]float64{0.7, 0.4}
					}
				}
				return len(dst)
			},
		}
		fmt.Fprintf(&trace, "raw=%t interval=%v pending=%t\n",
			v.UsesRawSamples(), v.TickInterval(ctx), v.PausedDecayPending(ctx))
		v.Tick(ctx)
		trace.WriteString(v.Render())
		trace.WriteByte('\n')
	}
	return trace.String()
}

// TestVisualizerWidthComesFromCols checks that every mode draws and paces
// itself at v.Cols, whatever the PanelWidth global holds. The model narrows
// PanelWidth for the playlist column, so a mode that reads the global between
// frames draws or ticks for the wrong width.
func TestVisualizerWidthComesFromCols(t *testing.T) {
	for mode := range VisCount {
		t.Run(visModes[mode].name, func(t *testing.T) {
			want := visWidthTrace(t, mode, 20)
			for _, panelWidth := range []int{0, 8, 57} {
				if got := visWidthTrace(t, mode, panelWidth); got != want {
					t.Errorf("PanelWidth %d changes the trace at 20 columns:\ngot:\n%s\nwant:\n%s", panelWidth, got, want)
				}
			}
		})
	}
}

// TestVisualizerColumnsComeOnlyFromCols pins that the width never comes from
// the PanelWidth global. A visualizer that nobody sized has no width.
func TestVisualizerColumnsComeOnlyFromCols(t *testing.T) {
	prev := PanelWidth
	PanelWidth = 57
	t.Cleanup(func() { PanelWidth = prev })

	tests := []struct {
		name string
		v    *Visualizer
		want int
	}{
		{name: "sized", v: &Visualizer{Cols: 20}, want: 20},
		{name: "unsized", v: &Visualizer{}},
		{name: "negative", v: &Visualizer{Cols: -1}},
		{name: "nil", v: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.v.columns(); got != tt.want {
				t.Fatalf("columns() = %d, want %d", got, tt.want)
			}
		})
	}
}
