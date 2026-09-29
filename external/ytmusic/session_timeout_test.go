package ytmusic

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// setOAuthTransport sends OAuth token requests of the test through rt.
func setOAuthTransport(t *testing.T, timeout time.Duration, rt http.RoundTripper) {
	t.Helper()
	original := oauthHTTPClient
	oauthHTTPClient = &http.Client{Timeout: timeout, Transport: rt}
	t.Cleanup(func() { oauthHTTPClient = original })
}

func TestSilentSessionTimesOut(t *testing.T) {
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
	if err := credsFile.Save(&storedCreds{RefreshToken: "refresh"}); err != nil {
		t.Fatal(err)
	}
	setOAuthTransport(t, 50*time.Millisecond, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		<-req.Context().Done()
		return nil, req.Context().Err()
	}))

	done := make(chan error, 1)
	go func() {
		_, err := NewSessionSilent(context.Background(), "client", "secret")
		done <- err
	}()
	select {
	case err := <-done:
		var urlErr *url.Error
		if !errors.As(err, &urlErr) || !urlErr.Timeout() {
			t.Fatalf("NewSessionSilent() error = %v, want a client timeout", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("NewSessionSilent() did not time out")
	}
}

// TestSessionTokenRefreshOutlivesSetupContext checks that a session still
// refreshes its access token after the context that built it ends.
func TestSessionTokenRefreshOutlivesSetupContext(t *testing.T) {
	setOAuthTransport(t, 5*time.Second, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if err := req.Context().Err(); err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"access_token":"new","token_type":"Bearer","expires_in":3600}`)),
			Request:    req,
		}, nil
	}))

	ctx, cancel := context.WithCancel(context.Background())
	expired := &oauth2.Token{AccessToken: "old", RefreshToken: "refresh", Expiry: time.Now().Add(-time.Hour)}
	s, err := newTokenSession(ctx, "client", "secret", expired, "refresh")
	if err != nil {
		t.Fatalf("newTokenSession() error = %v", err)
	}
	cancel()

	token, err := s.tokenSource.Token()
	if err != nil {
		t.Fatalf("Token() after the setup context ended: %v", err)
	}
	if token.AccessToken != "new" {
		t.Errorf("access token = %q, want new", token.AccessToken)
	}
}
