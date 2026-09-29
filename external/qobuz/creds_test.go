package qobuz

import (
	"os"
	"runtime"
	"testing"
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
			path, err := CredsPath()
			if err != nil {
				t.Fatal(err)
			}
			if tt.existing != 0 {
				if err := os.WriteFile(path, []byte(`{"app_id":"old"}`), tt.existing); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(path, tt.existing); err != nil {
					t.Fatal(err)
				}
			}

			want := storedCreds{AppID: "app", Secrets: []string{"s1"}, UserAuthToken: "token", UserID: "7"}
			if err := saveCreds(&want); err != nil {
				t.Fatalf("saveCreds() error = %v", err)
			}
			got, err := loadCreds()
			if err != nil {
				t.Fatalf("loadCreds() error = %v", err)
			}
			if got.AppID != want.AppID || got.UserAuthToken != want.UserAuthToken || got.UserID != want.UserID {
				t.Errorf("loadCreds() = %+v, want %+v", got, want)
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
