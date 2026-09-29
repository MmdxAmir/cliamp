package main

import (
	"slices"
	"testing"
)

func TestV2Operations(t *testing.T) {
	appearance := []string{"theme", "vis"}
	plugins := []string{"plugin.call", "plugin.commands"}
	for _, tc := range []struct {
		name     string
		headless bool
		plugins  bool
		missing  []string
	}{
		{name: "TUI with plugins", plugins: true},
		{name: "TUI without plugins", missing: plugins},
		{name: "headless with plugins", headless: true, plugins: true, missing: appearance},
		{name: "headless without plugins", headless: true, missing: append(slices.Clone(appearance), plugins...)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			operations := v2Operations(tc.headless, tc.plugins)
			for _, name := range append(append([]string{"play", "queue.list", "provider.search"}, appearance...), plugins...) {
				_, ok := operations.Lookup(name)
				if want := !slices.Contains(tc.missing, name); ok != want {
					t.Errorf("%s registered = %v, want %v", name, ok, want)
				}
			}
		})
	}
}
