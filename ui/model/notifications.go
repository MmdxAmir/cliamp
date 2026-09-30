package model

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/history"
	"github.com/bjarneo/cliamp/internal/playback"
	"github.com/bjarneo/cliamp/luaplugin"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// notifyAll sends the current playback state to both OS media controls and Lua plugins.
func (m *Model) notifyAll() {
	m.notifyPlayback()
	m.notifyPlugins()
}

func (m *Model) attachNotifier(notifier playback.Notifier) {
	m.notifier = notifier
	m.notifyAll()
}

// notifyPlugins emits a playback state event to Lua plugins.
func (m *Model) notifyPlugins() {
	if m.luaMgr == nil || !m.luaMgr.HasHook(luaplugin.EventPlaybackState) {
		return
	}
	track, _ := m.currentPlaybackTrack()
	artist, title := m.resolveTrackDisplay(track)
	data := trackToMap(track)
	data["status"] = m.playerStatus()
	data["title"] = title
	data["artist"] = artist
	data["position"] = m.player.Position().Seconds()
	m.emitPlugin(luaplugin.EventPlaybackState, data)
}

// playerStatus returns the player state that Lua plugins see: "playing",
// "paused" or "stopped".
func (m *Model) playerStatus() string {
	switch {
	case !m.player.IsPlaying():
		return "stopped"
	case m.player.IsPaused():
		return "paused"
	}
	return "playing"
}

// resolveTrackDisplay returns the display artist and title, applying ICY
// stream title override for radio streams.
func (m *Model) resolveTrackDisplay(track playlist.Track) (artist, title string) {
	return streamDisplay(track, m.streamTitle)
}

// streamDisplay returns the artist and title to show for track while its
// stream sends streamTitle. A title in the form "Artist - Title" replaces
// both. A title without the separator replaces the title. A title with the
// separator and an empty part keeps the track values, so a broken tag does
// not show.
func streamDisplay(track playlist.Track, streamTitle string) (artist, title string) {
	artist, title = track.Artist, track.Title
	if streamTitle == "" || !track.Stream {
		return artist, title
	}
	if a, t, ok := splitStreamTitle(streamTitle); ok {
		return a, t
	}
	if !strings.Contains(streamTitle, " - ") {
		title = streamTitle
	}
	return artist, title
}

// splitStreamTitle splits an ICY stream title such as "Artist - Title" at
// the first " - ". It trims both parts and reports false unless both are
// set. Every surface that shows or looks up a stream song uses this rule.
func splitStreamTitle(s string) (artist, title string, ok bool) {
	artist, title, ok = strings.Cut(s, " - ")
	artist, title = strings.TrimSpace(artist), strings.TrimSpace(title)
	if !ok || artist == "" || title == "" {
		return "", "", false
	}
	return artist, title, true
}

// trackToMap builds a metadata map from a track for Lua plugin events.
func trackToMap(track playlist.Track) map[string]any {
	return luaplugin.TrackData(pluginTrack(track))
}

// pluginTrack returns track as Lua plugins see it. It leaves Live unset,
// because that depends on the engine.
func pluginTrack(track playlist.Track) luaplugin.Track {
	return luaplugin.Track{
		Title:    track.Title,
		Artist:   track.Artist,
		Album:    track.Album,
		Genre:    track.Genre,
		Path:     track.Path,
		Year:     track.Year,
		Number:   track.TrackNumber,
		Duration: track.DurationSecs,
		Stream:   track.Stream,
	}
}

func (m *Model) notifyPlayback() {
	if m.notifier == nil {
		return
	}
	status := playback.StatusStopped
	if m.player.IsPlaying() {
		if m.player.IsPaused() {
			status = playback.StatusPaused
		} else {
			status = playback.StatusPlaying
		}
	}
	track, _ := m.currentPlaybackTrack()
	artist, title := m.resolveTrackDisplay(track)
	m.notifier.Update(playback.State{
		Status: status,
		Track: playback.Track{
			Title:       title,
			Artist:      artist,
			Album:       track.Album,
			Genre:       track.Genre,
			TrackNumber: track.TrackNumber,
			URL:         track.Path,
			ArtURL:      track.AlbumArtURL,
			Duration:    m.player.Duration(),
		},
		VolumeDB:    m.player.Volume(),
		VolumeMinDB: m.player.VolumeMin(),
		Position:    m.player.Position(),
		Seekable:    m.player.Seekable(),
	})
}

// stopByUser is an explicit stop from a key, IPC, or media controls. Besides
// stopping, it tells plugins so they can drop any continuation they planned.
// A queue running out never comes through here.
func (m *Model) stopByUser() {
	m.stopPlayback()
	m.emitPlugin(luaplugin.EventPlaybackStop, nil)
}

// nowPlaying fires a now-playing notification for the given track if configured.
func (m *Model) nowPlaying(track playlist.Track) {
	m.playingTrackStarted = true
	if m.luaMgr != nil && m.luaMgr.HasHook(luaplugin.EventTrackChange) {
		m.emitPlugin(luaplugin.EventTrackChange, trackToMap(track))
	}

	reporter := m.findPlaybackReporter(track)
	if reporter == nil {
		return
	}
	canSeek := m.player.Seekable()
	position := m.player.Position()
	m.queueReport(func() {
		if err := reporter.ReportNowPlaying(track, position, canSeek); err != nil {
			applog.Warn("now-playing report failed for %q: %v", track.Title, err)
		}
	})
}

// queueReport runs report on the report queue, after the reports that
// Update added before it.
func (m *Model) queueReport(report func()) {
	if m.reports == nil {
		m.reports = &reportQueue{}
	}
	m.reports.add(report)
}

// recordListenedTrack adds a starting track to local history and refreshes
// any Recently Played surfaces. Called when playback of the track begins so
// the list mirrors what is playing right now, not the previous song.
//
// The write is synchronous so successive entries preserve their ordering on
// disk; the file is small (~30 KB at the 200-entry cap) so latency is
// sub-millisecond.
func (m *Model) recordListenedTrack(track playlist.Track) tea.Cmd {
	if m.historyStore == nil {
		return nil
	}
	if err := m.historyStore.Record(track, time.Now()); err != nil {
		applog.Warn("history record failed for %q: %v", track.Path, err)
		return nil
	}
	if m.plManager.visible {
		m.plMgrRefreshList()
		if m.plManager.screen == plMgrScreenTracks && m.plManager.selPlaylist == history.PlaylistName {
			m.plMgrReloadTracks(history.PlaylistName)
		}
	}
	return m.refreshPaneAfterLocalWrite()
}

// maybeScrobble fires a playback-complete report for the given track when it
// is left (skip, stop, natural end) and past 50% of its known duration,
// matching Last.fm-style play-count conventions. Local history is recorded
// separately at track start via recordListenedTrack.
func (m *Model) maybeScrobble(track playlist.Track, elapsed, duration time.Duration) tea.Cmd {
	dur := duration
	if dur <= 0 {
		dur = time.Duration(track.DurationSecs) * time.Second
	}
	pastThreshold := dur > 0 && elapsed >= dur/2

	var refresh tea.Cmd

	// Emit scrobble event to Lua plugins for all tracks (not just Navidrome).
	if m.luaMgr != nil && pastThreshold && m.luaMgr.HasHook(luaplugin.EventTrackScrobble) {
		data := trackToMap(track)
		data["played_secs"] = elapsed.Seconds()
		m.emitPlugin(luaplugin.EventTrackScrobble, data)
	}

	reporter := m.findPlaybackReporter(track)
	if reporter == nil {
		return refresh
	}
	if duration <= 0 {
		// Unknown duration: use DurationSecs metadata as fallback.
		duration = time.Duration(track.DurationSecs) * time.Second
	}
	if duration <= 0 {
		return refresh // still unknown — skip
	}
	if elapsed < duration/2 {
		return refresh // less than 50% played
	}
	canSeek := m.player.Seekable()
	m.queueReport(func() {
		if err := reporter.ReportScrobble(track, elapsed, duration, canSeek); err != nil {
			applog.Warn("scrobble failed for %q: %v", track.Title, err)
		}
	})
	return refresh
}

// findTrackPosition returns the provider that can report track's saved
// position, independent of whether it also reports playback.
func (m *Model) findTrackPosition(track playlist.Track) provider.TrackPosition {
	match := func(p playlist.Provider) provider.TrackPosition {
		tp, ok := p.(provider.TrackPosition)
		if !ok {
			return nil
		}
		if !tp.CanTrackPosition(track) {
			return nil
		}
		return tp
	}

	if tp := match(m.provider); tp != nil {
		return tp
	}
	for _, pe := range m.providers {
		if pe.Provider == nil {
			continue
		}
		if tp := match(pe.Provider); tp != nil {
			return tp
		}
	}
	return nil
}

// findPlaybackReporter returns the first registered provider that can report
// playback for the given track.
func (m *Model) findPlaybackReporter(track playlist.Track) provider.PlaybackReporter {
	match := func(p playlist.Provider) provider.PlaybackReporter {
		reporter, ok := p.(provider.PlaybackReporter)
		if !ok || !reporter.CanReportPlayback(track) {
			return nil
		}
		return reporter
	}

	if reporter := match(m.provider); reporter != nil {
		return reporter
	}
	for _, pe := range m.providers {
		if pe.Provider == nil {
			continue
		}
		if reporter := match(pe.Provider); reporter != nil {
			return reporter
		}
	}
	return nil
}

// progressReportInterval bounds how often interim listening positions are
// pushed to providers that accept them.
const progressReportInterval = 15 * time.Second

// tickProgressReport pushes an interim position update for the playing track to
// providers that accept them, at most once per progressReportInterval.
func (m *Model) tickProgressReport(now time.Time) {
	if m.player == nil || !m.player.IsPlaying() || m.player.IsPaused() {
		return
	}
	if !m.lastProgressReport.IsZero() && now.Sub(m.lastProgressReport) < progressReportInterval {
		return
	}
	track, idx := m.currentPlaybackTrack()
	if idx < 0 {
		return
	}
	reporter, ok := m.findPlaybackReporter(track).(provider.ProgressReporter)
	if !ok {
		return
	}
	m.lastProgressReport = now
	position := m.player.Position()
	m.queueReport(func() {
		if err := reporter.ReportProgress(track, position); err != nil {
			applog.Warn("progress report failed for %q: %v", track.Title, err)
		}
	})
}

// hasPlaybackState reports whether any provider stores local listening state,
// so a render pass can decide whether to reserve the played-marker column.
func (m Model) hasPlaybackState() bool {
	for _, p := range m.playbackStateReporters() {
		if p.HasPlaybackState() {
			return true
		}
	}
	return false
}

// playbackStateReporters returns the providers that keep listening state
// locally. Implementations answer without I/O, so a render pass may call them
// for every visible row.
func (m Model) playbackStateReporters() []provider.PlaybackStateReporter {
	var reporters []provider.PlaybackStateReporter
	if r, ok := m.provider.(provider.PlaybackStateReporter); ok {
		reporters = append(reporters, r)
	}
	for _, pe := range m.providers {
		if pe.Provider == nil || pe.Provider == m.provider {
			continue
		}
		if r, ok := pe.Provider.(provider.PlaybackStateReporter); ok {
			reporters = append(reporters, r)
		}
	}
	return reporters
}

// playbackStateFrom returns the stored listening state for track, from the
// first reporter that claims it. A render pass resolves the reporters once and
// passes them in for every row.
func playbackStateFrom(reporters []provider.PlaybackStateReporter, track playlist.Track) (provider.PlaybackState, bool) {
	for _, r := range reporters {
		if state, ok := r.PlaybackState(track); ok {
			return state, true
		}
	}
	return provider.PlaybackState{}, false
}
