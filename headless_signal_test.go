//go:build unix

package main

import (
	"os"
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

// A real SIGINT or SIGTERM reaches the headless Model as a quit message, so
// the Model keeps the resume position. The program then ends with no error.
// The signal handler of Bubbletea would end the program with no Update, and
// on SIGINT with an error.
func TestHeadlessSignalQuitsThroughTheModel(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			started := make(chan struct{})
			prog := tea.NewProgram(quitRecorder{started: started}, headlessProgramOptions()...)
			stop := quitOnSignals(prog.Send)
			defer stop()

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

			if err := syscall.Kill(os.Getpid(), sig); err != nil {
				prog.Kill()
				t.Fatal(err)
			}
			select {
			case res := <-done:
				if res.err != nil {
					t.Fatalf("Run error = %v, want a clean exit", res.err)
				}
				if !res.model.(quitRecorder).quit {
					t.Fatal("the model got no quit message")
				}
			case <-time.After(5 * time.Second):
				// The signal handler of Bubbletea can block the shutdown, so
				// Kill could block as well.
				t.Fatal("the headless program did not quit")
			}
		})
	}
}
