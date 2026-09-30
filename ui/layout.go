package ui

// PaddingH is the horizontal padding inside the frame.
var PaddingH = 3

// paddingV is the vertical padding inside the frame.
var paddingV = 1

// SetPadding sets the configured frame padding. The model clamps it to the
// terminal size when it lays out the frame.
func SetPadding(h, v int) {
	PaddingH = h
	paddingV = v
}

// VerticalPadding returns the current frame padding above and below content.
func VerticalPadding() int {
	return paddingV
}
