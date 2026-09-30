package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	cli "github.com/urfave/cli/v3"

	"github.com/bjarneo/cliamp/config"
	"github.com/bjarneo/cliamp/ipc"
)

func TestInverseBoolFlags(t *testing.T) {
	tests := []struct {
		flag string
		get  func(config.Overrides) *bool
		want bool
	}{
		{"--shuffle", func(ov config.Overrides) *bool { return ov.Shuffle }, true},
		{"--no-shuffle", func(ov config.Overrides) *bool { return ov.Shuffle }, false},
		{"--mono", func(ov config.Overrides) *bool { return ov.Mono }, true},
		{"--no-mono", func(ov config.Overrides) *bool { return ov.Mono }, false},
		{"--auto-play", func(ov config.Overrides) *bool { return ov.Play }, true},
		{"--no-auto-play", func(ov config.Overrides) *bool { return ov.Play }, false},
		{"--simplified", func(ov config.Overrides) *bool { return ov.Simplified }, true},
		{"--no-simplified", func(ov config.Overrides) *bool { return ov.Simplified }, false},
		{"--help-bar", func(ov config.Overrides) *bool { return ov.HideHelpBar }, false},
		{"--no-help-bar", func(ov config.Overrides) *bool { return ov.HideHelpBar }, true},
		{"--expanded", func(ov config.Overrides) *bool { return ov.Expanded }, true},
		{"--no-expanded", func(ov config.Overrides) *bool { return ov.Expanded }, false},
		{"--expand-playlist", func(ov config.Overrides) *bool { return ov.ExpandPlaylist }, true},
		{"--no-expand-playlist", func(ov config.Overrides) *bool { return ov.ExpandPlaylist }, false},
		{"--low-power", func(ov config.Overrides) *bool { return ov.LowPower }, true},
		{"--no-low-power", func(ov config.Overrides) *bool { return ov.LowPower }, false},
	}

	for _, tt := range tests {
		t.Run(tt.flag, func(t *testing.T) {
			app := buildApp()
			var got config.Overrides
			app.Action = func(_ context.Context, c *cli.Command) error {
				var err error
				got, err = overridesFromFlags(c)
				return err
			}

			if err := app.Run(context.Background(), []string{"cliamp", tt.flag}); err != nil {
				t.Fatalf("Run: %v", err)
			}
			value := tt.get(got)
			if value == nil || *value != tt.want {
				t.Errorf("value = %v, want %t", value, tt.want)
			}
		})
	}
}

// Every key and alias in providerKeys parses through --provider in any case,
// and the flag help names each one. An unknown value fails with a message
// that names every key. A key missing from the table would make
// --provider and the provider config key fail for a provider that works.
func TestProviderFlag(t *testing.T) {
	parse := func(t *testing.T, value string) (config.Overrides, error) {
		t.Helper()
		app := buildApp()
		var got config.Overrides
		var flagErr error
		app.Action = func(_ context.Context, c *cli.Command) error {
			got, flagErr = overridesFromFlags(c)
			return nil
		}
		if err := app.Run(t.Context(), []string{"cliamp", "--provider", value}); err != nil {
			t.Fatalf("Run: %v", err)
		}
		return got, flagErr
	}

	type flagCase struct{ value, want string }
	var cases []flagCase
	for _, pk := range providerKeys {
		cases = append(cases, flagCase{pk.key, pk.key}, flagCase{strings.ToUpper(pk.key), pk.key})
		if pk.alias != "" {
			cases = append(cases, flagCase{pk.alias, pk.key})
		}
	}
	for _, tc := range cases {
		t.Run(tc.value, func(t *testing.T) {
			got, err := parse(t, tc.value)
			if err != nil {
				t.Fatalf("--provider %s rejected: %v", tc.value, err)
			}
			if got.Provider == nil || *got.Provider != tc.want {
				t.Fatalf("provider = %v, want %s", got.Provider, tc.want)
			}
		})
	}

	t.Run("unknown", func(t *testing.T) {
		_, err := parse(t, "winamp")
		if err == nil || !strings.HasSuffix(err.Error(), `(got "winamp")`) {
			t.Fatalf("error = %v, want a --provider error", err)
		}
		for _, pk := range providerKeys {
			if !strings.Contains(err.Error(), pk.key) {
				t.Errorf("error %q does not name %s", err, pk.key)
			}
		}
	})

	t.Run("help", func(t *testing.T) {
		var usage string
		for _, f := range buildApp().Flags {
			if sf, ok := f.(*cli.StringFlag); ok && sf.Name == "provider" {
				usage = sf.Usage
			}
		}
		for _, pk := range providerKeys {
			for _, value := range []string{pk.key, pk.alias} {
				if value != "" && !slices.Contains(strings.Split(strings.TrimPrefix(usage, "default provider: "), ", "), value) {
					t.Errorf("--provider help %q does not name %s", usage, value)
				}
			}
		}
	})
}

func TestRadioCommandFlags(t *testing.T) {
	app := buildApp()
	radioCmd := app.Command("radio")
	if radioCmd == nil {
		t.Fatal("radio command not registered")
	}
	for _, name := range []string{"stats", "globe", "json"} {
		if !slices.ContainsFunc(radioCmd.Flags, func(f cli.Flag) bool { return slices.Contains(f.Names(), name) }) {
			t.Errorf("radio command lacks --%s", name)
		}
	}
	// --globe --json is contradictory and must fail before any network call.
	err := app.Run(context.Background(), []string{"cliamp", "radio", "--globe", "--json"})
	if err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Errorf("--globe --json error = %v", err)
	}
}

// The bookmark commands stay as aliases of the favorite commands so old
// scripts keep working.
func TestPlaylistBookmarkAliases(t *testing.T) {
	playlistCmd := buildApp().Command("playlist")
	if playlistCmd == nil {
		t.Fatal("playlist command not registered")
	}
	for _, tt := range []struct{ alias, name string }{
		{alias: "bookmark", name: "favorite"},
		{alias: "bookmarks", name: "favorites"},
	} {
		t.Run(tt.alias, func(t *testing.T) {
			got := playlistCmd.Command(tt.alias)
			if got == nil || got.Name != tt.name {
				t.Fatalf("playlist %s = %v, want the %s command", tt.alias, got, tt.name)
			}
		})
	}
}

// The ipc package returns a bare sentinel; the CLI wording is added here.
func TestUserIPCErrorRendersNotRunning(t *testing.T) {
	rendered := userIPCError(fmt.Errorf("dial: %w", ipc.ErrNotRunning))
	want := fmt.Sprintf("cliamp is not running (no socket at %s)", ipc.DefaultSocketPath())
	if rendered.Error() != want {
		t.Errorf("rendered = %q, want %q", rendered.Error(), want)
	}

	other := errors.New("connect: permission denied")
	if got := userIPCError(other); got != other {
		t.Errorf("unrelated error rewritten to %v", got)
	}
}
