package model

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui"
)

// paneSearchProvider keeps one catalog search for the whole provider, as
// radio.Provider does. The pane shows that search. SearchStations is the
// search without state that IPC uses.
type paneSearchProvider struct {
	mu           sync.Mutex
	results      []playlist.PlaylistInfo // nil when no catalog search is active
	catalogCalls int
	queries      []string
}

func (p *paneSearchProvider) Name() string { return "Radio" }

func (p *paneSearchProvider) Playlists() ([]playlist.PlaylistInfo, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.results), nil
}

func (p *paneSearchProvider) Tracks(id string) ([]playlist.Track, error) {
	return []playlist.Track{{Path: "https://radio.example/" + id, Title: id, Stream: true}}, nil
}

func (p *paneSearchProvider) SearchCatalog(query string) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.catalogCalls++
	p.results = []playlist.PlaylistInfo{{ID: "s:0", Name: query + " FM"}}
	return 1, nil
}

func (p *paneSearchProvider) ClearSearch() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.catalogCalls++
	p.results = nil
}

func (p *paneSearchProvider) IsSearching() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.results != nil
}

func (p *paneSearchProvider) SearchStations(_ context.Context, query string, limit int) ([]playlist.Track, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.queries = append(p.queries, query)
	return []playlist.Track{{Path: "https://radio.example/rock", Title: "Rock FM", Stream: true}}[:min(1, limit)], nil
}

// An IPC provider.search runs beside the TUI. It must not replace, clear or
// cancel the search that the radio pane shows.
func TestIPCProviderSearchKeepsPaneSearch(t *testing.T) {
	prov := &paneSearchProvider{results: []playlist.PlaylistInfo{{ID: "s:0", Name: "Jazz FM"}}}
	engine := &playbackFakeEngine{}
	m := Model{
		player:    engine,
		playlist:  playlist.New(),
		vis:       ui.NewVisualizer(float64(engine.SampleRate())),
		provider:  prov,
		providers: []ProviderEntry{{Key: "radio", Name: "Radio", Provider: prov}},
	}

	got := runV2(t, &m, "provider.search", ipc.Request{Provider: "radio", Query: "rock"})

	if !got.OK || len(got.Tracks) != 1 || got.Tracks[0].Title != "Rock FM" {
		t.Fatalf("response = %+v, want the Rock FM result", got)
	}
	if !slices.Equal(prov.queries, []string{"rock"}) {
		t.Fatalf("stateless searches = %v, want [rock]", prov.queries)
	}
	if prov.catalogCalls != 0 {
		t.Fatalf("IPC search changed the pane search state %d times", prov.catalogCalls)
	}
	if lists, _ := prov.Playlists(); !prov.IsSearching() || len(lists) != 1 || lists[0].Name != "Jazz FM" {
		t.Fatalf("pane search = %+v, want the Jazz FM search kept", lists)
	}
}
