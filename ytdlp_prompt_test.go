package main

import (
	"bytes"
	"strings"
	"testing"
)

// The yt-dlp install prompt installs only after a bare Enter in an
// interactive start. A daemon or a start with stdin that is not a terminal
// never asks, and EOF, as from /dev/null under systemd, skips the install.
func TestOfferYTDLPInstall(t *testing.T) {
	for _, tc := range []struct {
		name        string
		interactive bool
		input       string
		want        bool
	}{
		{name: "enter", interactive: true, input: "\n", want: true},
		{name: "enter with carriage return", interactive: true, input: "\r\n", want: true},
		{name: "answer n", interactive: true, input: "n\n"},
		{name: "eof", interactive: true, input: ""},
		{name: "text without newline", interactive: true, input: "y"},
		{name: "not interactive", input: "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			if got := offerYTDLPInstall(tc.interactive, strings.NewReader(tc.input), &out); got != tc.want {
				t.Fatalf("offerYTDLPInstall() = %v, want %v", got, tc.want)
			}
			if asked := out.Len() > 0; asked != tc.interactive {
				t.Fatalf("prompt written = %v, want %v", asked, tc.interactive)
			}
		})
	}
}
