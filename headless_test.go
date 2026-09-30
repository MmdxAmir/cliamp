package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/config"
	"github.com/bjarneo/cliamp/internal/plugintrust"
	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/luaplugin"
	"github.com/bjarneo/cliamp/player"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui/model"
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

// newTestPlugins loads the trusted plugins in sources, keyed by name, from a
// new config directory. With no sources the manager has no plugins.
func newTestPlugins(t *testing.T, sources map[string]string) *luaplugin.Manager {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CLIAMP_CONFIG_DIR", dir)
	pluginDir := filepath.Join(dir, "plugins")
	for name, src := range sources {
		if err := os.MkdirAll(pluginDir, 0o755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(pluginDir, name+".lua")
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := plugintrust.Approve(pluginDir, name, path); err != nil {
			t.Fatal(err)
		}
	}
	mgr, err := luaplugin.New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mgr.Close)
	return mgr
}

// A plugin job lists the plugin commands or runs one. It fails with no
// plugin manager, with bad parameters and when the command fails. A canceled
// job does not run.
func TestRunV2PluginJob(t *testing.T) {
	empty := newTestPlugins(t, nil)
	echo := newTestPlugins(t, map[string]string{"echo": `local p = plugin.register({name = "echo", type = "hook"})
p:command("say", function(args) return "said " .. args[1] end)`})
	for _, tt := range []struct {
		name      string
		plugins   *luaplugin.Manager
		operation string
		params    string
		cancel    bool
		want      ipc.JobState
		result    ipc.Response
		code      string
	}{
		{name: "no plugin manager", operation: "plugin.commands", want: ipc.JobFailed, code: ipc.V2ErrorCodeUnavailable},
		{name: "no plugins", plugins: empty, operation: "plugin.commands", want: ipc.JobSucceeded, result: ipc.Response{OK: true}},
		{name: "commands", plugins: echo, operation: "plugin.commands", want: ipc.JobSucceeded, result: ipc.Response{OK: true, Items: []string{"echo say"}}},
		{name: "call", plugins: echo, operation: "plugin.call", params: `{"name":"echo","sub":"say","args":["hi"]}`, want: ipc.JobSucceeded, result: ipc.Response{OK: true, Output: "said hi"}},
		{name: "unknown command", plugins: echo, operation: "plugin.call", params: `{"name":"echo","sub":"shout"}`, want: ipc.JobFailed, code: ipc.V2ErrorCodeInternal},
		{name: "call without plugins", plugins: empty, operation: "plugin.call", params: `{"name":"echo","sub":"say"}`, want: ipc.JobFailed, code: ipc.V2ErrorCodeInternal},
		{name: "no command name", plugins: echo, operation: "plugin.call", params: `{"name":"echo"}`, want: ipc.JobFailed, code: ipc.V2ErrorCodeInvalidParams},
		{name: "bad params", plugins: echo, operation: "plugin.call", params: `[1]`, want: ipc.JobFailed, code: ipc.V2ErrorCodeInvalidParams},
		{name: "canceled", plugins: echo, operation: "plugin.commands", cancel: true, want: ipc.JobCanceled, code: ipc.V2ErrorCodeCanceled},
	} {
		t.Run(tt.name, func(t *testing.T) {
			jobs := ipc.NewJobStore()
			job, err := jobs.Create(tt.operation)
			if err != nil {
				t.Fatal(err)
			}
			if tt.cancel {
				if err := jobs.Cancel(job.ID); err != nil {
					t.Fatal(err)
				}
			}
			runV2PluginJob(jobs, job.ID, ipc.V2Request{Operation: tt.operation, Params: json.RawMessage(tt.params)}, tt.plugins)

			got, ok := jobs.Get(job.ID)
			if !ok {
				t.Fatal("the job is gone")
			}
			if got.State != tt.want {
				t.Fatalf("state = %s, want %s (error %+v)", got.State, tt.want, got.Error)
			}
			if tt.code != "" {
				if got.Error == nil || got.Error.Code != tt.code {
					t.Fatalf("error = %+v, want code %s", got.Error, tt.code)
				}
				return
			}
			var result ipc.Response
			if err := json.Unmarshal(got.Result, &result); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(result, tt.result) {
				t.Fatalf("result = %+v, want %+v", result, tt.result)
			}
		})
	}
}

// The dispatcher sends state.get and spectrum.get to the Model and returns
// its reply. It stops the wait when the request ends or the Model does not
// answer in time.
func TestV2DispatcherReads(t *testing.T) {
	timeout := v2ReplyTimeout
	v2ReplyTimeout = 50 * time.Millisecond
	t.Cleanup(func() { v2ReplyTimeout = timeout })

	snapshot := ipc.RuntimeSnapshot{State: "playing"}
	answer := func(msg tea.Msg) {
		if request, ok := msg.(model.V2RequestMsg); ok && request.Reply != nil {
			request.Reply <- model.V2RequestResult{Result: ipc.V2Result{Snapshot: &snapshot}}
		}
	}
	for _, tt := range []struct {
		name     string
		method   string
		send     func(tea.Msg)
		canceled bool
		code     string
	}{
		{name: "state", method: "state.get", send: answer},
		{name: "spectrum", method: "spectrum.get", send: answer},
		{name: "no answer", method: "state.get", send: func(tea.Msg) {}, code: ipc.V2ErrorCodeUnavailable},
		{name: "canceled", method: "spectrum.get", send: func(tea.Msg) {}, canceled: true, code: ipc.V2ErrorCodeCanceled},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tt.canceled {
				cancel()
			}
			dispatcher := newV2Dispatcher(tt.send, ipc.NewJobStore(), nil)
			result, v2Err := dispatcher.DispatchV2(ctx, ipc.V2Request{Method: tt.method})
			if tt.code != "" {
				if v2Err == nil || v2Err.Code != tt.code {
					t.Fatalf("error = %+v, want code %s", v2Err, tt.code)
				}
				return
			}
			if v2Err != nil {
				t.Fatalf("error = %+v", v2Err)
			}
			if result.Snapshot == nil || result.Snapshot.State != "playing" {
				t.Fatalf("snapshot = %+v, want the reply of the Model", result.Snapshot)
			}
		})
	}
}

// Every other operation becomes a queued job. The Model runs it, except
// plugin.call and plugin.commands, which run against the plugin manager. A
// full job store refuses the job.
func TestV2DispatcherJobs(t *testing.T) {
	for _, tt := range []struct {
		name      string
		operation string
		full      bool
		code      string
		toModel   bool
		failure   string
	}{
		{name: "Model job", operation: "next", toModel: true},
		{name: "plugin job", operation: "plugin.commands", failure: ipc.V2ErrorCodeUnavailable},
		{name: "full job store", operation: "next", full: true, code: ipc.V2ErrorCodeConflict},
	} {
		t.Run(tt.name, func(t *testing.T) {
			jobs := ipc.NewJobStore(ipc.WithJobStoreCapacity(1))
			if tt.full {
				if _, err := jobs.Create("pause"); err != nil {
					t.Fatal(err)
				}
			}
			sent := make(chan model.V2RequestMsg, 1)
			send := func(msg tea.Msg) {
				if request, ok := msg.(model.V2RequestMsg); ok {
					sent <- request
				}
			}
			result, v2Err := newV2Dispatcher(send, jobs, nil).DispatchV2(context.Background(), ipc.V2Request{Operation: tt.operation})
			if tt.code != "" {
				if v2Err == nil || v2Err.Code != tt.code {
					t.Fatalf("error = %+v, want code %s", v2Err, tt.code)
				}
				return
			}
			if v2Err != nil {
				t.Fatalf("error = %+v", v2Err)
			}
			if result.Job == nil || result.Job.State != ipc.JobQueued || result.Job.Operation != tt.operation {
				t.Fatalf("job = %+v, want a queued %s job", result.Job, tt.operation)
			}

			if tt.toModel {
				select {
				case request := <-sent:
					if request.Jobs != jobs || request.JobID != result.Job.ID || request.Request.Operation != tt.operation {
						t.Fatalf("the Model got %+v, want job %s", request, result.Job.ID)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("the Model got no job")
				}
				return
			}
			select {
			case event := <-jobs.Events():
				if event.Job.ID != result.Job.ID || event.Job.Error == nil || event.Job.Error.Code != tt.failure {
					t.Fatalf("job event = %+v, want job %s to fail with %s", event, result.Job.ID, tt.failure)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the plugin job did not finish")
			}
			select {
			case request := <-sent:
				t.Fatalf("the Model got the plugin job %+v", request)
			default:
			}
		})
	}
}

// captureStderr returns what fn writes to os.Stderr.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	stderr := os.Stderr
	os.Stderr = f
	fn()
	os.Stderr = stderr
	data, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// When another instance holds the socket, headless mode cannot start, so
// startIPC returns an error. The TUI prints the error and runs without the
// socket.
func TestStartIPCWithTheSocketInUse(t *testing.T) {
	for _, tt := range []struct {
		name     string
		headless bool
	}{
		{name: "headless", headless: true},
		{name: "TUI"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			startTestIPC(t, ipc.RuntimeSnapshot{}, func(*ipc.JobStore, string, ipc.V2Request) {})
			var stop func()
			var err error
			printed := captureStderr(t, func() {
				stop, err = startIPC(func(tea.Msg) {}, ipc.NewBroker(), nil, tt.headless)
			})
			message := printed
			if tt.headless {
				if err == nil || stop != nil {
					t.Fatalf("startIPC = %v, want an error", err)
				}
				message = err.Error()
			} else if err != nil || stop == nil {
				t.Fatalf("startIPC error = %v, want the TUI to run on", err)
			}
			if !strings.Contains(message, "cliamp is already running") {
				t.Fatalf("message = %q, want the running instance", message)
			}
			if tt.headless && printed != "" {
				t.Fatalf("stderr = %q, want nothing", printed)
			}
		})
	}
}

// startIPC serves the V2 requests through send and the operations of the
// mode. stop removes the socket.
func TestStartIPCServesTheModel(t *testing.T) {
	for _, tt := range []struct {
		name     string
		headless bool
	}{
		{name: "headless", headless: true},
		{name: "TUI"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
			send := func(msg tea.Msg) {
				if request, ok := msg.(model.V2RequestMsg); ok && request.Reply != nil {
					request.Reply <- model.V2RequestResult{Result: ipc.V2Result{Snapshot: &ipc.RuntimeSnapshot{State: "paused"}}}
				}
			}
			stop, err := startIPC(send, ipc.NewBroker(), nil, tt.headless)
			if err != nil {
				t.Fatal(err)
			}
			socket := ipc.DefaultSocketPath()

			response, err := ipc.SendV2(socket, ipc.V2Request{ID: json.RawMessage(`1`), Method: "state.get"})
			if err != nil {
				stop()
				t.Fatal(err)
			}
			if response.Snapshot == nil || response.Snapshot.State != "paused" {
				stop()
				t.Fatalf("state.get = %+v, want the snapshot of the Model", response)
			}
			response, err = ipc.SendV2(socket, ipc.V2Request{ID: json.RawMessage(`2`), Operation: "theme", Params: json.RawMessage(`{"name":"x"}`)})
			if err != nil {
				stop()
				t.Fatal(err)
			}
			if unknown := response.Error != nil && response.Error.Code == ipc.V2ErrorCodeUnknownOperation; unknown != tt.headless {
				stop()
				t.Fatalf("theme error = %+v, want unknown operation %v", response.Error, tt.headless)
			}

			stop()
			if _, err := os.Stat(socket); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("socket after stop: %v, want it removed", err)
			}
		})
	}
}

// Headless mode has no screen, so it keeps the default visualizer that
// spectrum.get uses. The TUI applies the configured visualizer.
func TestConfigureModel(t *testing.T) {
	for _, tt := range []struct {
		name     string
		headless bool
		want     string
	}{
		{name: "headless", headless: true, want: "Bars"},
		{name: "TUI", want: "Wave"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := model.New(&player.Player{}, playlist.New(), nil, "cliamp", nil, nil, nil, nil, nil, config.SaveFunc{})
			configureModel(&m, config.Config{Visualizer: "Wave"}, tt.headless, false)
			if got := m.VisualizerName(); got != tt.want {
				t.Fatalf("visualizer = %q, want %q", got, tt.want)
			}
		})
	}
}
