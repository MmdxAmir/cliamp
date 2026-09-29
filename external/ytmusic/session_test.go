package ytmusic

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestSaveCredsWritesPrivateFile(t *testing.T) {
	tests := []struct {
		name     string
		existing os.FileMode // 0 means no file exists before the save
	}{
		{name: "new file"},
		{name: "replace private file", existing: 0o600},
		{name: "replace readable file", existing: 0o644},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
			path, err := credsFile.Path()
			if err != nil {
				t.Fatal(err)
			}
			if tt.existing != 0 {
				if err := os.WriteFile(path, []byte(`{"refresh_token":"old"}`), tt.existing); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(path, tt.existing); err != nil {
					t.Fatal(err)
				}
			}

			if err := credsFile.Save(&storedCreds{RefreshToken: "new"}); err != nil {
				t.Fatalf("credsFile.Save() error = %v", err)
			}
			got, err := credsFile.Load()
			if err != nil {
				t.Fatalf("credsFile.Load() error = %v", err)
			}
			if got.RefreshToken != "new" {
				t.Errorf("refresh token = %q, want new", got.RefreshToken)
			}
			if runtime.GOOS == "windows" {
				return // Windows does not report Unix permission bits.
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if perm := info.Mode().Perm(); perm != 0o600 {
				t.Errorf("credentials mode = %o, want 600", perm)
			}
		})
	}
}

// TestLoadStoredCredsFile checks that credentials an earlier release wrote
// still load, so an upgrade keeps the user signed in.
func TestLoadStoredCredsFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLIAMP_CONFIG_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "ytmusic_credentials.json"), []byte(`{"refresh_token":"refresh"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := credsFile.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.RefreshToken != "refresh" {
		t.Errorf("refresh token = %q, want refresh", got.RefreshToken)
	}
}

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
