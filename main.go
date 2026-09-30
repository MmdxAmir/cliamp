package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/config"
	"github.com/bjarneo/cliamp/external/radio"
	"github.com/bjarneo/cliamp/internal/appdir"
	"github.com/bjarneo/cliamp/internal/appmeta"
	"github.com/bjarneo/cliamp/internal/embyapi"
	"github.com/bjarneo/cliamp/internal/playback"
	"github.com/bjarneo/cliamp/internal/resume"
	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/luaplugin"
	"github.com/bjarneo/cliamp/mediactl"
	"github.com/bjarneo/cliamp/player"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/resolve"
	"github.com/bjarneo/cliamp/theme"
	"github.com/bjarneo/cliamp/ui"
	"github.com/bjarneo/cliamp/ui/model"
)

// version is set at build time via -ldflags "-X main.version=vX.Y.Z".
var version string

const (
	defaultUIFPS  = 20
	lowPowerUIFPS = 5
)

func run(overrides config.Overrides, positional []string, daemon, visualizer60FPS bool) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	overrides.Apply(&cfg)

	closeLog, appliedLevel, logErr := initLogging(cfg.LogLevel)
	defer closeLog()
	if logErr != nil {
		fmt.Fprintf(os.Stderr, "logging: %v (continuing without file log)\n", logErr)
		applog.Status("logging: %v", logErr)
	} else {
		applog.Info("cliamp starting (version=%s level=%s)", appmeta.Version(), appliedLevel)
	}

	providers := buildProviders(cfg, !daemon && isCharDevice(os.Stdin))
	defer providers.Close()

	if len(positional) > 0 && (positional[0] == "search" || positional[0] == "search-sc") {
		if len(positional) == 1 {
			return fmt.Errorf("search requires a query string (e.g. cliamp search \"never gonna give you up\")")
		}
		prefix := "ytsearch1:"
		if positional[0] == "search-sc" {
			prefix = "scsearch1:"
		}
		query := strings.Join(positional[1:], " ")
		positional = []string{prefix + query}
	}

	if cfg.YouTubeMusic.ExpandPlaylist != nil {
		resolve.ExpandYTPlaylist = *cfg.YouTubeMusic.ExpandPlaylist
	}

	resolved, err := resolve.Args(positional)
	if err != nil {
		return err
	}

	defaultProvider := cfg.Provider
	if defaultProvider == "" {
		defaultProvider = "cliamp"
	}
	defaultRadio := len(positional) == 0 && defaultProvider == "radio"
	// The cliamp radio view waits for the listener to pick a channel. The
	// daemon has no view, and auto-play expects sound without a keypress, so
	// both start with the live channel streams instead.
	liveChannels := defaultRadio ||
		(len(positional) == 0 && defaultProvider == "cliamp" && (daemon || cfg.AutoPlay))
	resumeState := resume.Load()

	pl := playlist.New()
	if cfg.Playlist != "" && providers.local != nil {
		tracks, err := providers.local.Tracks(cfg.Playlist)
		if err != nil {
			return fmt.Errorf("playlist %q: %w", cfg.Playlist, err)
		}
		pl.Add(tracks...)
	} else if liveChannels {
		// The channel list lives in the M3U the radio provider already serves,
		// so resolve that instead of restating it here: the startup playlist
		// then matches what browsing "cliamp radio" shows -- same channels,
		// same order, same titles -- and a new channel needs no code change.
		// It goes through the normal pending path, so the fetch happens in the
		// background rather than delaying launch.
		resolved.Pending = append(resolved.Pending, radio.BuiltinURL)
	}
	pl.Add(resolved.Tracks...)

	resumeServer := providers.resumeServer(defaultProvider)
	restoredContext := false
	restoredIndex := 0
	restoredResumePath := ""
	if !daemon && resumeServer != nil && cfg.Playlist == "" && len(positional) == 0 && len(resolved.Pending) == 0 && pl.Len() == 0 {
		if tracks, index, activePath, ok := restoreServerContext(resumeState, resumeServer); ok {
			pl.Add(tracks...)
			restoredContext = true
			restoredIndex = index
			restoredResumePath = activePath
		}
	}

	if cfg.AudioDevice != "" {
		cleanup := player.PrepareAudioDevice(cfg.AudioDevice)
		defer cleanup()
	}

	sampleRate := cfg.SampleRate
	if sampleRate == 0 {
		if detected := player.DeviceSampleRate(); detected > 0 {
			sampleRate = detected
		} else {
			sampleRate = 44100
		}
	}

	p, err := player.New(player.Quality{
		SampleRate:      sampleRate,
		BufferMs:        cfg.BufferMs,
		ResampleQuality: cfg.ResampleQuality,
		BitDepth:        cfg.BitDepth,
	})
	if err != nil {
		return fmt.Errorf("player: %w", err)
	}
	defer p.Close()

	providers.registerPlayerHooks(p)

	cfg.ApplyPlayer(p)
	cfg.ApplyPlaylist(pl)
	ui.SetPadding(cfg.PaddingH, cfg.PaddingV)

	themes := theme.LoadAll()

	pluginBroker := ipc.NewBroker()
	defer pluginBroker.Close()

	luaMgr, luaErr := luaplugin.New(cfg.Plugins, pluginBroker)
	if luaErr != nil {
		fmt.Fprintf(os.Stderr, "lua plugins: %v\n", luaErr)
	}
	if luaMgr != nil {
		luaMgr.SetReservedKeys(model.ReservedKeys())
		defer luaMgr.Close()
	}

	m := model.New(p, pl, providers.entries, defaultProvider, providers.localPlaylists(), themes, luaMgr, config.SaveFunc{})
	m.SetRadioFavorites(providers.radioFavorites)
	if resumeServer != nil {
		m.SetResumeSaver(func(track playlist.Track, positionSec int, context []playlist.Track, contextIndex int) {
			if _, ok := resumeServer.RestoreTrack(track); !ok {
				return
			}
			resume.SaveState(resume.State{
				Path: track.Path, PositionSec: positionSec,
				Context: context, ContextIndex: contextIndex,
			})
		})
	}
	if restoredContext {
		m.SetInitialTrack(restoredIndex)
	}
	m.SetIPCBroker(pluginBroker)
	m.SetCustomEQBands(cfg.EQ)
	m.SetVisVolumeLinked(cfg.VisVolumeLinked)

	if luaMgr != nil {
		luaMgr.SetStateProvider(luaplugin.StateProvider{
			PlayerState: func() string {
				if !p.IsPlaying() {
					return "stopped"
				}
				if p.IsPaused() {
					return "paused"
				}
				return "playing"
			},
			Position:      func() float64 { return p.Position().Seconds() },
			Duration:      func() float64 { return p.Duration().Seconds() },
			Volume:        func() float64 { return p.Volume() },
			Speed:         func() float64 { return p.Speed() },
			Mono:          func() bool { return p.Mono() },
			RepeatMode:    func() string { return pl.Repeat().String() },
			Shuffle:       func() bool { return pl.Shuffled() },
			EQBands:       func() [10]float64 { return p.EQBands() },
			TrackTitle:    func() string { t, _ := pl.Current(); return t.Title },
			TrackArtist:   func() string { t, _ := pl.Current(); return t.Artist },
			TrackAlbum:    func() string { t, _ := pl.Current(); return t.Album },
			TrackGenre:    func() string { t, _ := pl.Current(); return t.Genre },
			TrackYear:     func() int { t, _ := pl.Current(); return t.Year },
			TrackNumber:   func() int { t, _ := pl.Current(); return t.TrackNumber },
			TrackPath:     func() string { t, _ := pl.Current(); return t.Path },
			TrackIsStream: func() bool { t, _ := pl.Current(); return t.Stream },
			TrackIsLive:   func() bool { t, _ := pl.Current(); return model.PlaysLive(t, p) },
			TrackDuration: func() int { t, _ := pl.Current(); return t.DurationSecs },
			PlaylistCount: func() int { return pl.Len() },
			CurrentIndex:  func() int { return pl.Index() },
			HasNext:       pl.HasNext,
			QueueList: func() []luaplugin.QueueEntry {
				tracks := pl.Tracks()
				out := make([]luaplugin.QueueEntry, len(tracks))
				for i, t := range tracks {
					out[i] = luaplugin.QueueEntry{
						Title:    t.Title,
						Artist:   t.Artist,
						Album:    t.Album,
						Genre:    t.Genre,
						Year:     t.Year,
						Path:     t.Path,
						Duration: t.DurationSecs,
						Stream:   t.Stream,
						Index:    i,
						Queued:   pl.QueuePosition(i) > 0, // 1-based; 0 means not queued
					}
				}
				return out
			},
		})
	}

	if luaMgr != nil {
		if names := luaMgr.Visualizers(); len(names) > 0 {
			m.RegisterLuaVisualizers(names, luaMgr.RenderVis)
		}
	}

	m.SetSeekStepLarge(cfg.SeekStepLargeDuration())
	m.SetLyricsOffset(cfg.LyricsOffsetMs)
	m.SetInitialDirectory(cfg.InitialDirectory)
	m.SetDownloadsDirectory(cfg.Downloads.Directory)
	m.SetPendingURLs(resolved.Pending)
	if cfg.Playlist != "" && len(resolved.Tracks) == 0 && len(resolved.Pending) == 0 {
		m.SetLoadedPlaylist(cfg.Playlist)
	}
	if !daemon && len(resolved.Tracks) == 0 && len(resolved.Pending) == 0 && pl.Len() == 0 {
		m.StartInProvider()
	}
	if cfg.EQPreset != "" && cfg.EQPreset != "Custom" {
		m.SetEQPreset(cfg.EQPreset, nil)
	}
	if cfg.Theme != "" {
		m.SetTheme(cfg.Theme)
	}
	if cfg.AutoPlay && !restoredContext {
		m.SetAutoPlay(true)
	}
	if daemon {
		// Headless mode has no screen, so the view settings do not apply. The
		// default visualizer stays, because it serves spectrum.get.
		m.SetHeadless(true)
	} else {
		m.SetVisRows(cfg.VisRows)
		m.SetVisualizer60FPS(visualizer60FPS)
		if cfg.Visualizer != "" {
			m.SetVisualizer(cfg.Visualizer)
		}
		if cfg.LowPower {
			m.SetLowPower(true)
		}
		if cfg.Simplified {
			m.SetSimplified(true)
		}
		if cfg.HideHelpBar {
			m.SetHideHelpBar(true)
		}
		if cfg.HideSettingsPane {
			m.SetHideSettingsPane(true)
		}
		if cfg.ShowMetadata {
			m.SetShowMetadata(true)
		}
		if cfg.Expanded {
			m.SetExpanded(true)
		}
	}

	if resumeState.Path != "" && resumeState.PositionSec > 0 {
		// Jellyfin and Emby resume the restored context above. Mixcloud is also commonly
		// opened from its provider browser rather than a positional URL; preserve
		// cliamp's existing positional-file behavior for other providers.
		switch {
		case restoredResumePath != "":
			m.SetResume(restoredResumePath, resumeState.PositionSec)
		case playlist.IsMixcloudURL(resumeState.Path) || (!defaultRadio && len(positional) > 0):
			m.SetResume(resumeState.Path, resumeState.PositionSec)
		}
	}

	progOpts := []tea.ProgramOption{tea.WithFPS(defaultUIFPS)}
	switch {
	case daemon:
		progOpts = headlessProgramOptions()
	case cfg.LowPower:
		progOpts[0] = tea.WithFPS(lowPowerUIFPS)
	}
	prog := tea.NewProgram(m, progOpts...)
	if daemon {
		stopSignals := quitOnSignals(prog.Send)
		defer stopSignals()
	}

	defer providers.observeAuthURLs(prog.Send)()

	svc, svcErr := wireMediaCtl(prog)
	if svcErr != nil {
		applog.Warn("media control (MPRIS/NowPlaying) unavailable: %v", svcErr)
	} else if svc != nil {
		defer svc.Close()
	}

	if luaMgr != nil {
		luaMgr.SetControlProvider(luaplugin.ControlProvider{
			SetVolume:   func(db float64) { p.SetVolume(db) },
			SetSpeed:    func(ratio float64) { p.SetSpeed(ratio) },
			SetEQBand:   func(band int, db float64) { prog.Send(model.SetEQBandMsg{Band: band, Gain: db}) },
			ToggleMono:  func() { p.ToggleMono() },
			TogglePause: func() { prog.Send(playback.PlayPauseMsg{}) },
			Stop:        func() { prog.Send(playback.StopMsg{}) },
			Seek: func(secs float64) {
				prog.Send(playback.SeekMsg{Offset: time.Duration(secs * float64(time.Second))})
			},
			SetEQPreset: func(name string, bands *[10]float64) {
				prog.Send(model.SetEQPresetMsg{Name: name, Bands: bands})
			},
			Next: func() { prog.Send(playback.NextMsg{}) },
			Prev: func() { prog.Send(playback.PrevMsg{}) },
			QueueAdd: func(path string) {
				prog.Send(model.PluginQueueMsg{Op: "add", Path: path})
			},
			QueueAddTrack: func(t luaplugin.QueueTrack) {
				prog.Send(model.PluginQueueMsg{Op: "add_track", Track: playlist.Track{
					Path: t.Path, Title: t.Title, Artist: t.Artist, Album: t.Album,
					Genre: t.Genre, Year: t.Year, DurationSecs: t.Duration, Stream: t.Stream,
				}})
			},
			QueueJump: func(index int) {
				prog.Send(model.PluginQueueMsg{Op: "jump", Index: index})
			},
			QueueRemove: func(index int) {
				prog.Send(model.PluginQueueMsg{Op: "remove", Index: index})
			},
			QueueMove: func(from, to int) {
				prog.Send(model.PluginQueueMsg{Op: "move", Index: from, To: to})
			},
		})
		luaMgr.SetUIProvider(luaplugin.UIProvider{
			ShowMessage: func(text string, duration time.Duration) {
				prog.Send(model.ShowStatusMsg{Text: text, Duration: duration})
			},
		})
	}

	ipcSrv, ipcErr := ipc.NewServerWithBroker(ipc.DefaultSocketPath(), pluginBroker)
	if ipcErr != nil {
		if daemon {
			// Headless mode is controlled only through the socket.
			return fmt.Errorf("ipc: %w", ipcErr)
		}
		fmt.Fprintf(os.Stderr, "ipc: %v\n", ipcErr)
	} else {
		defer ipcSrv.Close()
		ipcSrv.SetV2Dispatcher(newTUIV2Dispatcher(prog, ipcSrv.JobStore(), luaMgr))
		ipcSrv.SetOperationRegistry(v2Operations(daemon, luaMgr != nil))
		go publishV2JobEvents(ipcSrv.Done(), ipcSrv.JobStore(), pluginBroker)
	}
	if daemon {
		fmt.Fprintf(os.Stderr, "cliamp: running headless (socket: %s)\n", ipc.DefaultSocketPath())
		applog.Info("running headless")
	}

	finalModel, err := mediactl.Run(prog, svc)
	if err != nil {
		return err
	}

	if fm, ok := finalModel.(model.Model); ok {
		// Headless mode has no theme keys, so it keeps the saved theme.
		if !daemon {
			themeName := fm.ThemeName()
			if theme.IsDefaultName(themeName) {
				themeName = ""
			}
			_ = config.Save("theme", fmt.Sprintf("%q", themeName))
		}

		if path, secs, playlistName := fm.ResumeState(); path != "" && secs > 0 {
			if resumeServer != nil && embyapi.IsStreamURL(path) {
				context, index := fm.ResumeContext()
				resume.SaveState(resume.State{
					Path: path, PositionSec: secs, Playlist: playlistName,
					Context: context, ContextIndex: index,
				})
			} else {
				resume.Save(path, secs, playlistName)
			}
		}
	}

	return nil
}

// headlessProgramOptions build a program with no terminal: no renderer, no
// input and no output. The frame ticker runs at its lowest rate. run
// handles the signals itself, see quitOnSignals.
func headlessProgramOptions() []tea.ProgramOption {
	return []tea.ProgramOption{
		tea.WithoutRenderer(),
		tea.WithInput(nil),
		tea.WithOutput(io.Discard),
		tea.WithFPS(1),
		tea.WithoutSignalHandler(),
	}
}

// quitOnSignals sends SIGINT and SIGTERM to quitOnSignal until stop is
// called.
func quitOnSignals(send func(tea.Msg)) (stop func()) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	go quitOnSignal(signals, send)
	return func() {
		signal.Stop(signals)
		close(signals)
	}
}

// quitOnSignal asks the Model to quit on the first SIGINT or SIGTERM, so it
// saves the resume position as the q key does. The signal handler of
// Bubbletea ends the program with no Update, and on SIGINT it also returns
// an error. After the first signal the default action is back, so a second
// signal ends a program that does not quit.
func quitOnSignal(signals chan os.Signal, send func(tea.Msg)) {
	if _, ok := <-signals; ok {
		signal.Stop(signals)
		send(playback.QuitMsg{})
	}
}

// v2Operations returns the V2 operations that this runtime serves. Headless
// mode has no theme or visualizer to change. The plugin operations need the
// plugin manager.
func v2Operations(headless, plugins bool) *ipc.OperationRegistry {
	operations := ipc.DefaultOperationRegistry()
	if headless {
		operations.Unregister("theme", "vis")
	}
	if !plugins {
		operations.Unregister("plugin.call", "plugin.commands")
	}
	return operations
}

func newTUIV2Dispatcher(prog *tea.Program, jobs *ipc.JobStore, plugins *luaplugin.Manager) ipc.V2Dispatcher {
	return ipc.V2DispatcherFunc(func(ctx context.Context, request ipc.V2Request) (ipc.V2Result, *ipc.V2Error) {
		switch request.Method {
		case "state.get", "spectrum.get":
			reply := make(chan model.V2RequestResult, 1)
			go prog.Send(model.V2RequestMsg{Request: request, Reply: reply})
			select {
			case result := <-reply:
				return result.Result, result.Error
			case <-ctx.Done():
				return ipc.V2Result{}, &ipc.V2Error{Code: ipc.V2ErrorCodeCanceled, Message: ipc.V2MessageCanceled}
			case <-time.After(3 * time.Second):
				return ipc.V2Result{}, &ipc.V2Error{Code: ipc.V2ErrorCodeUnavailable, Message: ipc.V2MessageUnavailable}
			}
		}

		job, err := jobs.CreateWithContext(ctx, request.Operation)
		if err != nil {
			return ipc.V2Result{}, &ipc.V2Error{Code: ipc.V2ErrorCodeConflict, Message: ipc.V2MessageConflict}
		}
		if request.Operation == "plugin.call" || request.Operation == "plugin.commands" {
			go runV2PluginJob(jobs, job.ID, request, plugins)
			return ipc.V2Result{Job: &job}, nil
		}
		// Program.Send may wait for the TUI update loop. Job submission itself
		// stays non-blocking so the IPC response can always acknowledge the job.
		go prog.Send(model.V2RequestMsg{Request: request, Jobs: jobs, JobID: job.ID})
		return ipc.V2Result{Job: &job}, nil
	})
}

func runV2PluginJob(jobs *ipc.JobStore, jobID string, request ipc.V2Request, plugins *luaplugin.Manager) {
	ctx, err := jobs.Start(jobID)
	if err != nil || ctx.Err() != nil {
		return
	}
	if plugins == nil {
		_ = jobs.Fail(jobID, ipc.V2Error{Code: ipc.V2ErrorCodeUnavailable, Message: ipc.V2MessageUnavailable})
		return
	}
	if request.Operation == "plugin.commands" {
		data, err := json.Marshal(ipc.Response{OK: true, Items: plugins.CommandList()})
		if err != nil {
			_ = jobs.Fail(jobID, ipc.V2Error{Code: ipc.V2ErrorCodeInternal, Message: ipc.V2MessageInternal})
			return
		}
		_ = jobs.Succeed(jobID, data)
		return
	}

	var params ipc.Request
	if err := json.Unmarshal(request.Params, &params); err != nil || params.Name == "" || params.Sub == "" {
		_ = jobs.Fail(jobID, ipc.V2Error{Code: ipc.V2ErrorCodeInvalidParams, Message: ipc.V2MessageInvalidParams})
		return
	}
	output, err := plugins.EmitCommand(params.Name, params.Sub, params.Args)
	if err != nil {
		_ = jobs.Fail(jobID, ipc.V2Error{Code: ipc.V2ErrorCodeInternal, Message: ipc.V2MessageInternal, Detail: err.Error()})
		return
	}
	data, err := json.Marshal(ipc.Response{OK: true, Output: output})
	if err != nil {
		_ = jobs.Fail(jobID, ipc.V2Error{Code: ipc.V2ErrorCodeInternal, Message: ipc.V2MessageInternal})
		return
	}
	_ = jobs.Succeed(jobID, data)
}

func publishV2JobEvents(done <-chan struct{}, jobs *ipc.JobStore, broker *ipc.Broker) {
	for {
		select {
		case <-done:
			return
		case event := <-jobs.Events():
			data, err := json.Marshal(event)
			if err == nil {
				_ = broker.Publish("runtime.job", data, false)
			}
		}
	}
}

// initLogging always returns a non-nil close func so the caller can defer
// it unconditionally, plus the applied level as a string for diagnostics.
// Errors come back as the third return value; the close func is a no-op
// and the level string is empty in that case.
func initLogging(levelStr string) (func() error, string, error) {
	noop := func() error { return nil }
	level, err := applog.ParseLevel(levelStr)
	if err != nil {
		return noop, "", err
	}
	dir, err := appdir.Dir()
	if err != nil {
		return noop, "", fmt.Errorf("resolve config dir: %w", err)
	}
	closeFn, err := applog.Init(filepath.Join(dir, "cliamp.log"), level)
	if err != nil {
		return noop, "", err
	}
	return closeFn, level.String(), nil
}

func wireMediaCtl(prog *tea.Program) (*mediactl.Service, error) {
	svc, err := mediactl.New(prog.Send)
	if err != nil || svc == nil {
		return svc, err
	}
	go prog.Send(model.AttachNotifier(svc))
	return svc, nil
}

// userIPCError renders ipc.ErrNotRunning as the wording users see. The ipc
// package returns a bare sentinel, so all CLI copy stays in the command layer.
func userIPCError(err error) error {
	if errors.Is(err, ipc.ErrNotRunning) {
		return fmt.Errorf("cliamp is not running (no socket at %s)", ipc.DefaultSocketPath())
	}
	return err
}

func ipcSend(operation string, params ipc.Request) (ipc.Response, error) {
	return ipcSendWithContext(context.Background(), operation, params)
}

// ipcSendLong waits for a V2 job under the supplied deadline. Plugin commands
// can legitimately run for minutes (for example, yt-dlp downloads).
func ipcSendLong(operation string, params ipc.Request, deadline time.Duration) (ipc.Response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	return ipcSendWithContext(ctx, operation, params)
}

func ipcSendWithContext(ctx context.Context, operation string, params ipc.Request) (ipc.Response, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return ipc.Response{}, fmt.Errorf("marshal %s parameters: %w", operation, err)
	}
	response, err := ipc.SendV2(ipc.DefaultSocketPath(), ipc.V2Request{
		ID:        json.RawMessage(`"cliamp"`),
		Method:    "operation.submit",
		Operation: operation,
		Params:    raw,
	})
	if err != nil {
		return ipc.Response{}, userIPCError(err)
	}
	if err := v2ResponseError(response); err != nil {
		return ipc.Response{}, err
	}
	if response.Job == nil {
		return ipc.Response{}, fmt.Errorf("%s returned no job", operation)
	}
	response, err = waitForV2Job(ctx, response.Job.ID)
	if err != nil {
		return ipc.Response{}, err
	}
	if response.Job == nil {
		return ipc.Response{}, fmt.Errorf("%s completed without a job", operation)
	}
	var result ipc.Response
	if err := json.Unmarshal(response.Job.Result, &result); err != nil {
		return ipc.Response{}, fmt.Errorf("decode %s result: %w", operation, err)
	}
	if !result.OK {
		return result, fmt.Errorf("%s", result.Error)
	}
	return result, nil
}

func ipcState() (ipc.RuntimeSnapshot, error) {
	response, err := ipc.SendV2(ipc.DefaultSocketPath(), ipc.V2Request{ID: json.RawMessage(`"cliamp"`), Method: "state.get"})
	if err != nil {
		return ipc.RuntimeSnapshot{}, userIPCError(err)
	}
	if err := v2ResponseError(response); err != nil {
		return ipc.RuntimeSnapshot{}, err
	}
	if response.Snapshot == nil {
		return ipc.RuntimeSnapshot{}, fmt.Errorf("state response has no snapshot")
	}
	return *response.Snapshot, nil
}

func stateResult(snapshot ipc.RuntimeSnapshot) ipc.Response {
	return ipc.Response{
		OK:         true,
		State:      snapshot.State,
		Track:      snapshot.Track,
		Position:   snapshot.Position,
		Duration:   snapshot.Duration,
		Volume:     snapshot.Volume,
		Playlist:   snapshot.Playlist,
		Index:      snapshot.Index,
		Total:      snapshot.Total,
		Visualizer: snapshot.Visualizer,
		Shuffle:    snapshot.Shuffle,
		Repeat:     snapshot.Repeat,
		Mono:       snapshot.Mono,
		Speed:      snapshot.Speed,
		EQPreset:   snapshot.EQPreset,
		Theme:      snapshot.Theme,
		EQBands:    snapshot.EQBands,
	}
}

func main() {
	appmeta.SetVersion(version)
	app := buildApp()
	if err := app.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
