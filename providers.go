package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/config"
	"github.com/bjarneo/cliamp/external/audiobookshelf"
	"github.com/bjarneo/cliamp/external/emby"
	"github.com/bjarneo/cliamp/external/jellyfin"
	"github.com/bjarneo/cliamp/external/local"
	"github.com/bjarneo/cliamp/external/lyrion"
	"github.com/bjarneo/cliamp/external/mixcloud"
	"github.com/bjarneo/cliamp/external/navidrome"
	"github.com/bjarneo/cliamp/external/netease"
	"github.com/bjarneo/cliamp/external/plex"
	"github.com/bjarneo/cliamp/external/podcast"
	"github.com/bjarneo/cliamp/external/qobuz"
	"github.com/bjarneo/cliamp/external/radio"
	"github.com/bjarneo/cliamp/external/radiometa"
	"github.com/bjarneo/cliamp/external/soundcloud"
	"github.com/bjarneo/cliamp/external/spotify"
	"github.com/bjarneo/cliamp/external/tidal"
	"github.com/bjarneo/cliamp/external/yandex"
	"github.com/bjarneo/cliamp/external/ytmusic"
	"github.com/bjarneo/cliamp/internal/resume"
	"github.com/bjarneo/cliamp/player"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
	"github.com/bjarneo/cliamp/resolve"
	"github.com/bjarneo/cliamp/ui/model"
)

// providerSet holds the providers of one run, in the order of the provider
// list. The player hooks, the sign-in observers and the shutdown find each
// capability in entries, so a new provider needs no change here for them.
type providerSet struct {
	entries []provider.Entry
	// local is nil when the config directory is unavailable.
	local          *local.Provider
	radioFavorites *radio.Favorites
}

// buildProviders creates the providers that cfg enables. The public
// providers are always available. The account providers register when they
// are configured. interactive allows the yt-dlp install prompt.
func buildProviders(cfg config.Config, interactive bool) *providerSet {
	s := &providerSet{radioFavorites: radio.LoadFavorites()}
	radioProv := radio.New(radio.Options{
		Favorites:   s.radioFavorites,
		Country:     cfg.Radio.Country,
		SaveCountry: config.SaveRadioCountry,
	})
	s.local = local.New()
	// Bookmarks became favorites. Copy the old bookmarks one time.
	if added, err := s.local.MigrateBookmarks(); err != nil {
		applog.Warn("bookmark migration: %v", err)
	} else if added > 0 {
		applog.Info("copied %d bookmarks into favorites", added)
	}

	add := func(key, name string, p playlist.Provider) {
		s.entries = append(s.entries, provider.Entry{Key: key, Name: name, Provider: p})
	}
	// The cliamp radio channels come first: they are the view cliamp opens on.
	add("cliamp", "cliamp radio", radio.NewChannels())
	add("radio", "Radio", radioProv)
	if s.local != nil {
		add("local", "Local", s.local)
	} else {
		logProviderSkipped("Local", "local", "config directory unavailable")
	}
	add("podcast", "Podcasts", podcast.New(cfg.Podcast.Country))

	if c := navidrome.NewFromConfig(cfg.Navidrome); c != nil {
		add("navidrome", "Navidrome", c)
	} else if c := navidrome.NewFromEnv(cfg.Navidrome); c != nil {
		add("navidrome", "Navidrome", c)
	}
	if c := lyrion.NewFromConfig(cfg.Lyrion); c != nil {
		add("lyrion", "Lyrion", c)
	} else if c := lyrion.NewFromEnv(); c != nil {
		add("lyrion", "Lyrion", c)
	}
	if p := plex.NewFromConfig(cfg.Plex); p != nil {
		add("plex", "Plex", p)
	}
	if p := jellyfin.NewFromConfig(cfg.Jellyfin); p != nil {
		add("jellyfin", "Jellyfin", p)
	}
	if p := emby.NewFromConfig(cfg.Emby); p != nil {
		add("emby", "Emby", p)
	}
	if p := audiobookshelf.NewFromConfig(cfg.Audiobookshelf); p != nil {
		add("audiobookshelf", "Audiobookshelf", p)
	}
	if cfg.Spotify.IsSet() {
		clientID := cfg.Spotify.ResolveClientID(spotify.DefaultClientID)
		add("spotify", "Spotify", spotify.New(nil, clientID, cfg.Spotify.Bitrate))
	}
	if cfg.Qobuz.IsSet() {
		add("qobuz", "Qobuz", qobuz.New(cfg.Qobuz.Quality))
	}
	if cfg.Tidal.IsSet() {
		add("tidal", "Tidal", tidal.New(cfg.Tidal.Quality, cfg.Tidal.ClientID, cfg.Tidal.ClientSecret))
	}
	if p := soundcloud.NewFromConfig(soundcloud.Config{
		Enabled:     cfg.SoundCloud.Enabled,
		User:        cfg.SoundCloud.User,
		CookiesFrom: cfg.SoundCloud.CookiesFrom,
	}); p != nil {
		add("soundcloud", "SoundCloud", p)
	}
	if p := mixcloud.NewFromConfig(mixcloud.Config{
		Enabled:        cfg.Mixcloud.Enabled,
		Username:       cfg.Mixcloud.Username,
		AccessToken:    cfg.Mixcloud.AccessToken,
		CookiesFrom:    cfg.Mixcloud.CookiesFrom,
		Styles:         cfg.Mixcloud.Styles,
		StylesSet:      cfg.Mixcloud.StylesSet,
		MaxItems:       cfg.Mixcloud.MaxItems,
		StreamCreators: cfg.Mixcloud.StreamCreators,
		SaveStyles:     config.SaveMixcloudStyles,
	}); p != nil {
		add("mixcloud", "Mixcloud", p)
	}
	if p := netease.NewFromConfig(netease.Config{
		Enabled:     cfg.NetEase.Enabled,
		CookiesFrom: cfg.NetEase.CookiesFrom,
		UserID:      cfg.NetEase.UserID,
	}); p != nil {
		add("netease", "NetEase", p)
	}
	if p := yandex.NewFromConfig(yandex.Config{
		Enabled: cfg.Yandex.Enabled,
		Token:   cfg.Yandex.Token,
	}); p != nil {
		add("yandex", "Yandex Music", p)
	}
	s.entries = append(s.entries, youTubeEntries(cfg, interactive)...)

	logProviderWiring(s.entries)
	return s
}

// youTubeEntries returns the YouTube (All), YouTube and YouTube Music
// entries, which share one sign-in. It returns none when YouTube is not
// wanted, has no credentials or has no yt-dlp.
func youTubeEntries(cfg config.Config, interactive bool) []provider.Entry {
	wanted := cfg.YouTubeMusic.IsSet()
	if !wanted {
		switch cfg.Provider {
		case "yt", "youtube", "ytmusic":
			wanted = true
		}
	}
	if !wanted {
		logYouTubeSkipped("not configured")
		return nil
	}
	clientID := strings.TrimSpace(cfg.YouTubeMusic.ClientID)
	clientSecret := strings.TrimSpace(cfg.YouTubeMusic.ClientSecret)
	hasOAuth := clientID != "" && clientSecret != ""
	hasCookies := strings.TrimSpace(cfg.YouTubeMusic.CookiesFrom) != ""
	if hasCookies {
		for _, host := range []string{"youtube.com", "youtu.be", "music.youtube.com"} {
			resolve.SetYTDLCookiesForHost(host, cfg.YouTubeMusic.CookiesFrom)
		}
	}
	if !hasOAuth && !hasCookies {
		fmt.Fprintf(os.Stderr, "YouTube: no credentials available (configure client_id/client_secret or cookies_from in config.toml)\n")
		logYouTubeSkipped("no credentials available")
		return nil
	}

	if !player.YTDLPAvailable() {
		fmt.Fprintf(os.Stderr, "\nYouTube requires yt-dlp for audio playback.\n")
		fmt.Fprintf(os.Stderr, "Install command: %s\n\n", player.YtdlpInstallHint())
		if offerYTDLPInstall(interactive, os.Stdin, os.Stderr) {
			fmt.Fprintf(os.Stderr, "Installing yt-dlp...\n")
			if err := player.InstallYTDLP(); err != nil {
				fmt.Fprintf(os.Stderr, "Installation failed: %v\n", err)
				fmt.Fprintf(os.Stderr, "YouTube providers disabled. Install manually and restart.\n\n")
			} else {
				fmt.Fprintf(os.Stderr, "yt-dlp installed successfully!\n\n")
			}
		}
	}
	if !player.YTDLPAvailable() {
		logYouTubeSkipped("yt-dlp not available")
		return nil
	}

	var all, video, music playlist.Provider
	if hasOAuth {
		p := ytmusic.New(nil, clientID, clientSecret, hasCookies)
		all, video, music = p.All, p.Video, p.Music
	} else {
		p := ytmusic.NewCookieProviders(cfg.YouTubeMusic.CookiesFrom)
		all, video, music = p.All, p.Video, p.Music
	}
	return []provider.Entry{
		{Key: "yt", Name: "YouTube (All)", Provider: all},
		{Key: "youtube", Name: "YouTube", Provider: video},
		{Key: "ytmusic", Name: "YouTube Music", Provider: music},
	}
}

// Close releases the providers that implement provider.Closer. The three
// YouTube providers share one base, and its close is safe to repeat.
func (s *providerSet) Close() {
	for _, e := range s.entries {
		if c, ok := e.Provider.(provider.Closer); ok {
			c.Close()
		}
	}
}

// localPlaylists returns the local provider for model.New. With no config
// directory it returns a nil interface. A nil *local.Provider in the
// interface would look set to the Model, which then calls it and panics.
func (s *providerSet) localPlaylists() playlist.Provider {
	if s.local == nil {
		return nil
	}
	return s.local
}

// jellyfin returns the Jellyfin provider, or nil when it is not configured.
func (s *providerSet) jellyfin() *jellyfin.Provider {
	for _, e := range s.entries {
		if p, ok := e.Provider.(*jellyfin.Provider); ok {
			return p
		}
	}
	return nil
}

// registerPlayerHooks registers with p the stream factories and source
// resolvers of the providers, and the rules that pick the pipeline of a URL.
func (s *providerSet) registerPlayerHooks(p *player.Player) {
	for _, e := range s.entries {
		if cs, ok := e.Provider.(provider.CustomStreamer); ok {
			for _, scheme := range cs.URISchemes() {
				p.RegisterStreamerFactory(scheme, cs.NewStreamer)
			}
		}
		switch prov := e.Provider.(type) {
		case *yandex.Provider:
			// Yandex tracks carry yandex:track: URIs; the provider resolves them
			// to a fresh signed stream URL when playback starts.
			p.RegisterSourceResolver(yandex.TrackURIPrefix, func(uri string) (player.ResolvedSource, error) {
				u, err := prov.ResolveSource(uri)
				if err != nil {
					return player.ResolvedSource{}, fmt.Errorf("resolve Yandex source: %w", err)
				}
				return player.ResolvedSource{URL: u}, nil
			})
		case *qobuz.QobuzProvider:
			// Qobuz tracks carry qobuz:// URIs. The provider resolves them to a
			// fresh signed URL when playback starts.
			p.RegisterSourceResolver(qobuz.TrackURIPrefix, func(uri string) (player.ResolvedSource, error) {
				u, err := prov.ResolveSource(uri)
				return player.ResolvedSource{URL: u}, err
			})
		case *tidal.TidalProvider:
			// Tidal tracks carry tidal:// URIs; the provider resolves them to a
			// fresh signed URL or DASH segment list when playback starts.
			p.RegisterSourceResolver(tidal.TrackURIPrefix, func(uri string) (player.ResolvedSource, error) {
				u, segments, err := prov.ResolveSource(uri)
				return player.ResolvedSource{URL: u, Segments: segments}, err
			})
		case *lyrion.Client:
			p.RegisterSourceResolver(lyrion.TrackURIPrefix, func(uri string) (player.ResolvedSource, error) {
				u, segments, err := prov.ResolveSource(uri)
				return player.ResolvedSource{URL: u, Segments: segments}, err
			})
		case *jellyfin.Provider:
			// Refresh restored Jellyfin URLs without changing logical playlist paths.
			for _, scheme := range []string{"http://", "https://"} {
				p.RegisterSourceResolver(scheme, func(rawURL string) (player.ResolvedSource, error) {
					u, err := prov.ResolveSource(rawURL)
					return player.ResolvedSource{URL: u}, err
				})
			}
		}
	}

	p.RegisterBufferedURLMatcher(isBufferedProviderURL)

	// Pull now-playing for stations that carry no inline ICY metadata (NTS, FIP).
	p.RegisterStreamMetadataResolver(radiometa.Resolver)
}

// isBufferedProviderURL reports whether u is a provider stream endpoint that
// needs the buffered download pipeline rather than the live-stream one. These
// are finite files with a known length, so buffering gives seeking and gapless
// playback. It matches every provider, configured or not, because history,
// favorites and saved playlists keep these URLs.
func isBufferedProviderURL(u string) bool {
	return navidrome.IsSubsonicStreamURL(u) ||
		jellyfin.IsStreamURL(u) ||
		emby.IsStreamURL(u) ||
		plex.IsStreamURL(u) ||
		qobuz.IsStreamURL(u) ||
		tidal.IsStreamURL(u) ||
		audiobookshelf.IsStreamURL(u) ||
		lyrion.IsStreamURL(u) ||
		yandex.IsStreamURL(u)
}

// observeAuthURLs sends the sign-in URL of a provider to the Model, so the
// provider pane can show it when no browser opens. restore removes the
// observers.
func (s *providerSet) observeAuthURLs(send func(tea.Msg)) (restore func()) {
	var restores []func()
	observe := func(set func(func(string)), names ...string) {
		set(func(u string) {
			for _, name := range names {
				send(model.ProvAuthURLMsg{ProviderName: name, URL: u})
			}
		})
		restores = append(restores, func() { set(nil) })
	}
	// The three YouTube providers share one sign-in. The model shows the URL
	// only for the provider that is active.
	var youTube []string
	for _, e := range s.entries {
		switch p := e.Provider.(type) {
		case *spotify.SpotifyProvider:
			observe(spotify.SetAuthURLObserver, p.Name())
		case *qobuz.QobuzProvider:
			observe(qobuz.SetAuthURLObserver, p.Name())
		case *tidal.TidalProvider:
			observe(tidal.SetAuthURLObserver, p.Name())
		case *ytmusic.YouTubeAllProvider, *ytmusic.YouTubeProvider, *ytmusic.YouTubeMusicProvider:
			youTube = append(youTube, p.Name())
		}
	}
	if len(youTube) > 0 {
		observe(ytmusic.SetAuthURLObserver, youTube...)
	}
	return func() {
		for _, restore := range restores {
			restore()
		}
	}
}

func restoreJellyfinContext(state resume.State, prov *jellyfin.Provider) ([]playlist.Track, int, string, bool) {
	if prov == nil || len(state.Context) == 0 {
		return nil, 0, "", false
	}
	index := state.ContextIndex
	if index < 0 || index >= len(state.Context) || state.Context[index].Path != state.Path {
		index = -1
		for i, track := range state.Context {
			if track.Path == state.Path {
				index = i
				break
			}
		}
	}
	if index < 0 {
		return nil, 0, "", false
	}
	if _, ok := prov.RestoreTrack(state.Context[index]); !ok {
		return nil, 0, "", false
	}

	tracks := append([]playlist.Track(nil), state.Context...)
	for i, track := range tracks {
		if restored, ok := prov.RestoreTrack(track); ok {
			tracks[i] = restored
		}
	}
	return tracks, index, tracks[index].Path, true
}

// logProviderRegistered records that a provider joined the active set. It
// writes to the log file only, so it never disturbs the TUI. See issue #406.
func logProviderRegistered(name, key string) {
	applog.Info("provider registered: name=%s key=%s", name, key)
}

// logProviderSkipped records why a provider did not register. It writes to
// the log file only, so it never disturbs the TUI. See issue #406.
func logProviderSkipped(name, key, reason string) {
	applog.Info("provider skipped: name=%s key=%s reason=%s", name, key, reason)
}

// logYouTubeSkipped records the skip reason for all three YouTube providers
// (All, video, music), since they register or skip as one group.
func logYouTubeSkipped(reason string) {
	logProviderSkipped("YouTube (All)", "yt", reason)
	logProviderSkipped("YouTube", "youtube", reason)
	logProviderSkipped("YouTube Music", "ytmusic", reason)
}

// offerYTDLPInstall asks on in whether to install yt-dlp now. It asks only
// when interactive is true. A bare Enter, y or yes in any case confirms. Any
// other answer, EOF or a read error skips the install. So a start with stdin
// at /dev/null, as under systemd, never installs a package.
func offerYTDLPInstall(interactive bool, in io.Reader, out io.Writer) bool {
	if !interactive {
		return false
	}
	fmt.Fprint(out, "Press Enter to install it now, or type n and press Enter to skip... ")
	answer, err := bufio.NewReader(in).ReadString('\n')
	if err == nil {
		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "", "y", "yes":
			return true
		}
	} else {
		// EOF leaves the cursor on the prompt line.
		fmt.Fprintln(out)
	}
	fmt.Fprint(out, "Skipped. YouTube providers are disabled.\n\n")
	return false
}

// isCharDevice reports whether f is a character device, such as a terminal.
// A pipe or a regular file is not. The null device is also a character
// device, so a start with stdin at /dev/null shows the install prompt. The
// EOF that follows skips the install.
func isCharDevice(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// optionalProviders lists the providers that register only when configured
// and skip with a plain "not configured" reason. YouTube and Local are not
// listed here: they have their own specific skip reasons.
var optionalProviders = []struct{ key, name string }{
	{"navidrome", "Navidrome"},
	{"lyrion", "Lyrion"},
	{"plex", "Plex"},
	{"jellyfin", "Jellyfin"},
	{"emby", "Emby"},
	{"audiobookshelf", "Audiobookshelf"},
	{"spotify", "Spotify"},
	{"qobuz", "Qobuz"},
	{"tidal", "Tidal"},
	{"soundcloud", "SoundCloud"},
	{"mixcloud", "Mixcloud"},
	{"netease", "NetEase"},
	{"yandex", "Yandex Music"},
}

// logProviderWiring logs the final provider registry: one line per
// registered provider, plus a skip line for each optional provider absent
// from it. See issue #406.
func logProviderWiring(providers []provider.Entry) {
	registered := make(map[string]bool, len(providers))
	for _, p := range providers {
		logProviderRegistered(p.Name, p.Key)
		registered[p.Key] = true
	}
	for _, p := range optionalProviders {
		if !registered[p.key] {
			logProviderSkipped(p.name, p.key, "not configured")
		}
	}
}
