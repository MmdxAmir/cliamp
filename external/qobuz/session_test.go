package qobuz

import (
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestParseQueryParams(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  oauthResult
	}{
		{name: "empty", query: "", want: oauthResult{}},
		{name: "user_auth_token", query: "user_auth_token=uat&user_id=7", want: oauthResult{Token: "uat", UserID: "7"}},
		{name: "token", query: "token=tok", want: oauthResult{Token: "tok"}},
		{name: "user_auth_token wins over token", query: "token=tok&user_auth_token=uat", want: oauthResult{Token: "uat"}},
		{name: "code_autorisation", query: "code_autorisation=fr", want: oauthResult{Code: "fr"}},
		{name: "code", query: "code=en", want: oauthResult{Code: "en"}},
		{name: "code_autorisation wins over code", query: "code=en&code_autorisation=fr", want: oauthResult{Code: "fr"}},
		{name: "token and code are both kept", query: "token=tok&code=en&user_id=7", want: oauthResult{Token: "tok", UserID: "7", Code: "en"}},
		{name: "empty values are ignored", query: "user_auth_token=&token=tok&code_autorisation=&code=en", want: oauthResult{Token: "tok", Code: "en"}},
		{name: "unrelated params", query: "state=x&foo=bar", want: oauthResult{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q, err := url.ParseQuery(tt.query)
			if err != nil {
				t.Fatal(err)
			}
			if got := parseQueryParams(q); got != tt.want {
				t.Errorf("parseQueryParams(%q) = %+v, want %+v", tt.query, got, tt.want)
			}
		})
	}
}

// TestCaptureOAuthRedirectPublishesURL checks that SetAuthURLObserver gets the
// sign-in URL and that the redirect to that URL completes the capture.
func TestCaptureOAuthRedirectPublishesURL(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // browser.Open finds no browser to start
	urls := make(chan string, 1)
	SetAuthURLObserver(func(u string) { urls <- u })
	t.Cleanup(func() { SetAuthURLObserver(nil) })

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	type capture struct {
		res oauthResult
		err error
	}
	done := make(chan capture, 1)
	go func() {
		res, err := captureOAuthRedirect(ctx, "app")
		done <- capture{res, err}
	}()

	var authURL string
	select {
	case authURL = <-urls:
	case <-ctx.Done():
		t.Fatal("observer got no sign-in URL")
	}
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	if got := u.Query().Get("ext_app_id"); got != "app" {
		t.Errorf("ext_app_id = %q, want app", got)
	}
	redirect, err := url.Parse(u.Query().Get("redirect_url"))
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+redirect.Port()+"/?user_auth_token=uat&user_id=7", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("redirect request: %v", err)
	}
	resp.Body.Close()

	got := <-done
	if got.err != nil {
		t.Fatalf("captureOAuthRedirect() error = %v", got.err)
	}
	if want := (oauthResult{Token: "uat", UserID: "7"}); got.res != want {
		t.Errorf("captureOAuthRedirect() = %+v, want %+v", got.res, want)
	}
}
