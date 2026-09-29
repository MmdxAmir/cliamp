package qobuz

import "testing"

func TestStreamURLRegistryEvictsOnReResolve(t *testing.T) {
	const first = "https://streaming-qobuz.example/file/abc123?sig=1"
	const second = "https://streaming-qobuz.example/file/abc123?sig=2"

	streamURLs.register("track-1", first)
	if !IsStreamURL(first) {
		t.Fatal("registered URL not recognized")
	}

	// Re-resolving the same track replaces its URL: the stale one must be
	// evicted so the registry stays bounded by tracks, not resolutions.
	streamURLs.register("track-1", second)
	if IsStreamURL(first) {
		t.Error("stale URL still registered after re-resolve")
	}
	if !IsStreamURL(second) {
		t.Error("fresh URL not recognized")
	}
	if IsStreamURL("https://other.example/track") {
		t.Error("unrelated URL must not match")
	}

	streamURLs.register("track-2", "")
	if IsStreamURL("") {
		t.Error("empty URL must not be registered")
	}
}
