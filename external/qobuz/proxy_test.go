package qobuz

import (
	"context"
	"io"
	"net/http"
	"slices"
	"testing"

	"github.com/bjarneo/cliamp/internal/httpclient/httpclienttest"
)

// TestClientUsesEnvironmentProxy checks that API calls follow ALL_PROXY.
func TestClientUsesEnvironmentProxy(t *testing.T) {
	proxy := httpclienttest.UseAllProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{}`)
	}))
	c := newClient("app", nil)
	c.baseURL = "http://www.qobuz.invalid/api.json/0.2/"
	if err := c.doGet(context.Background(), "track/get", nil, nil); err != nil {
		t.Fatalf("doGet: %v", err)
	}
	if got := proxy.Hosts(); !slices.Equal(got, []string{"www.qobuz.invalid"}) {
		t.Errorf("proxy hosts = %q, want [www.qobuz.invalid]", got)
	}
}
