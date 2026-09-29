package ui

import "charm.land/lipgloss/v2"

// PaddingH is the horizontal padding inside the frame.
var PaddingH = 3

// paddingV is the vertical padding inside the frame.
var paddingV = 1

// PanelWidth is the usable inner width of the frame.
// Updated dynamically in WindowSizeMsg based on terminal width.
var PanelWidth = 80 - 2*PaddingH

// SetPadding updates the frame padding and derived styles.
func SetPadding(h, v int) {
	PaddingH = h
	paddingV = v
	PanelWidth = 80 - 2*PaddingH
	FrameStyle = FrameStyle.Padding(paddingV, PaddingH)
}

// WithPanelWidth narrows PanelWidth for a scope and returns the restore func,
// so callers rendering into a sub-width column can `defer WithPanelWidth(w)()`
// and be sure the frame width comes back even if the call panics.
func WithPanelWidth(w int) func() {
	previous := PanelWidth
	PanelWidth = w
	return func() { PanelWidth = previous }
}

// VerticalPadding returns the current frame padding above and below content.
func VerticalPadding() int {
	return paddingV
}

// FrameStyle is the outer frame style for the TUI.
var FrameStyle = lipgloss.NewStyle().
	Padding(paddingV, PaddingH).
	Width(80)
