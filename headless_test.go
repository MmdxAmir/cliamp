package main

import (
	"os"
	"slices"
	"syscall"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/internal/playback"
)

// quitRecorder quits on playback.QuitMsg, as the Model does after it keeps
// the resume position.
type quitRecorder struct {
	started chan struct{}
	quit    bool
}

func (r quitRecorder) Init() tea.Cmd {
	close(r.started)
	return nil
}

func (r quitRecorder) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(playback.QuitMsg); ok {
		r.quit = true
		return r, tea.Quit
	}
	return r, nil
}

func (quitRecorder) View() tea.View { return tea.NewView("") }

// SIGINT and SIGTERM reach the headless Model as a quit message, so the
// Model keeps the resume position. The program then ends with no error.
func TestHeadlessSignalQuitsThroughTheModel(t *testing.T) {
	for _, sig := range []os.Signal{syscall.SIGINT, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			started := make(chan struct{})
			prog := tea.NewProgram(quitRecorder{started: started}, headlessProgramOptions()...)
			signals := make(chan os.Signal, 1)
			go quitOnSignal(signals, prog.Send)

			type result struct {
				model tea.Model
				err   error
			}
			done := make(chan result, 1)
			go func() {
				model, err := prog.Run()
				done <- result{model, err}
			}()
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				prog.Kill()
				t.Fatal("the headless program did not start")
			}

			signals <- sig
			select {
			case res := <-done:
				if res.err != nil {
					t.Fatalf("Run error = %v, want a clean exit", res.err)
				}
				if !res.model.(quitRecorder).quit {
					t.Fatal("the model got no quit message")
				}
			case <-time.After(5 * time.Second):
				prog.Kill()
				t.Fatal("the headless program did not quit")
			}
		})
	}
}

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
