package ytmusic

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOAuthCallbackHandler(t *testing.T) {
	const state = "expected-state"
	tests := []struct {
		name       string
		queries    []string
		wantStatus []int
		wantCode   string // "" means the handler must not pass a code
	}{
		{
			name:       "valid callback",
			queries:    []string{"state=expected-state&code=abc"},
			wantStatus: []int{http.StatusOK},
			wantCode:   "abc",
		},
		{
			name:       "wrong state",
			queries:    []string{"state=other&code=abc"},
			wantStatus: []int{http.StatusBadRequest},
		},
		{
			name:       "missing state",
			queries:    []string{"code=abc"},
			wantStatus: []int{http.StatusBadRequest},
		},
		{
			name:       "missing code",
			queries:    []string{"state=expected-state"},
			wantStatus: []int{http.StatusBadRequest},
		},
		{
			name:       "duplicate callback",
			queries:    []string{"state=expected-state&code=first", "state=expected-state&code=second", "state=expected-state&code=third"},
			wantStatus: []int{http.StatusOK, http.StatusOK, http.StatusOK},
			wantCode:   "first",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			codeCh := make(chan string, 1)
			handler := oauthCallbackHandler(state, codeCh)
			for i, query := range tt.queries {
				rec := httptest.NewRecorder()
				done := make(chan struct{})
				go func() {
					defer close(done)
					handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/callback?"+query, nil))
				}()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Fatalf("callback %d blocked", i)
				}
				if rec.Code != tt.wantStatus[i] {
					t.Errorf("callback %d status = %d, want %d", i, rec.Code, tt.wantStatus[i])
				}
			}

			var got string
			select {
			case got = <-codeCh:
			default:
			}
			if got != tt.wantCode {
				t.Errorf("code = %q, want %q", got, tt.wantCode)
			}
		})
	}
}
