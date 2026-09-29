package ytmusic

import (
	"context"
	"slices"
	"testing"

	"github.com/bjarneo/cliamp/internal/httpclient/httpclienttest"
)

// TestOAuthUsesEnvironmentProxy checks that token requests follow ALL_PROXY.
// The test proxy refuses https tunnels, so the refresh fails after it
// reaches the proxy.
func TestOAuthUsesEnvironmentProxy(t *testing.T) {
	proxy := httpclienttest.UseAllProxy(t, nil)
	if _, err := silentTokenRefresh(context.Background(), "id", "secret", "refresh"); err == nil {
		t.Fatal("silentTokenRefresh succeeded through a proxy that opens no tunnels")
	}
	if got := proxy.Hosts(); !slices.Equal(got, []string{"oauth2.googleapis.com:443"}) {
		t.Errorf("proxy hosts = %q, want [oauth2.googleapis.com:443]", got)
	}
}
