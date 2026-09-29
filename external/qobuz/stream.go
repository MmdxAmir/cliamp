package qobuz

import "sync"

// urlRegistry maps resolved track IDs to their latest signed CDN URL. The
// player consults IsStreamURL through a registered buffered-URL matcher, so
// Qobuz FLAC streams go through the buffer-while-playing and ffmpeg pipeline,
// like Navidrome's raw streams. That pipeline detects the codec and supports
// seeking. Each play resolves the track again and replaces its previous
// entry, so the registry holds at most one URL for each track played in this
// session.
type urlRegistry struct {
	mu      sync.Mutex
	byTrack map[string]string
	urls    map[string]struct{}
}

var streamURLs = urlRegistry{
	byTrack: make(map[string]string),
	urls:    make(map[string]struct{}),
}

// register records u as trackID's current stream URL, evicting the track's
// previous URL.
func (r *urlRegistry) register(trackID, u string) {
	if u == "" {
		return
	}
	r.mu.Lock()
	if old, ok := r.byTrack[trackID]; ok {
		delete(r.urls, old)
	}
	r.byTrack[trackID] = u
	r.urls[u] = struct{}{}
	r.mu.Unlock()
}

// IsStreamURL reports whether u is a live Qobuz stream URL previously
// resolved by the provider. It is registered with the player's buffered-URL
// matcher in main.go.
func IsStreamURL(u string) bool {
	streamURLs.mu.Lock()
	defer streamURLs.mu.Unlock()
	_, ok := streamURLs.urls[u]
	return ok
}
