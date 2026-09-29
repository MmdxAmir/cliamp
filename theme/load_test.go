package theme

import (
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/bjarneo/cliamp/applog"
)

func TestLoadAllIncludesBuiltinThemes(t *testing.T) {
	// Point HOME somewhere empty so only embedded themes load.
	t.Setenv("HOME", t.TempDir())

	themes := LoadAll()
	if len(themes) == 0 {
		t.Fatal("LoadAll() returned no themes, expected built-in set")
	}

	// Check a well-known theme is present (dracula ships with the project).
	var hasDracula bool
	for _, th := range themes {
		if th.Name == "dracula" {
			hasDracula = true
			if th.Accent == "" {
				t.Error("dracula theme has empty Accent — embed/parse failed")
			}
			break
		}
	}
	if !hasDracula {
		t.Error("built-in themes missing dracula")
	}
}

func TestLoadAllIncludesWinampPalette(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	want := Theme{
		Name:     "winamp",
		BG:       "#000000",
		Accent:   "#00FF00",
		BrightFG: "#FFFFFF",
		FG:       "#969696",
		Green:    "#29CE10",
		Yellow:   "#D6B521",
		Red:      "#EF3110",
	}

	for _, th := range LoadAll() {
		if th.Name == want.Name {
			if th != want {
				t.Errorf("winamp theme = %+v, want %+v", th, want)
			}
			return
		}
	}
	t.Fatal("built-in themes missing winamp")
}

func TestLoadAllSortedCaseInsensitive(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	themes := LoadAll()
	for i := 1; i < len(themes); i++ {
		a := strings.ToLower(themes[i-1].Name)
		b := strings.ToLower(themes[i].Name)
		if a > b {
			t.Errorf("themes not sorted: %q before %q", themes[i-1].Name, themes[i].Name)
		}
	}
}

func TestLoadAllUserThemeOverridesBuiltin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Put a user override file named "dracula.toml" with a distinctive accent color.
	userDir := filepath.Join(home, ".config", "cliamp", "themes")
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	overridden := `accent = "#ff00ff"
bright_fg = "#f8f8f2"
fg = "#123456"
green = "#50fa7b"
yellow = "#f1fa8c"
red = "#ff5555"
`
	if err := os.WriteFile(filepath.Join(userDir, "dracula.toml"), []byte(overridden), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	themes := LoadAll()
	var got Theme
	for _, th := range themes {
		if strings.EqualFold(th.Name, "dracula") {
			got = th
			break
		}
	}
	if got.Name == "" {
		t.Fatal("dracula theme not present after override")
	}
	if got.Accent != "#ff00ff" {
		t.Errorf("Accent = %q, want #ff00ff (user override)", got.Accent)
	}
	if got.FG != "#123456" {
		t.Errorf("FG = %q, want #123456 (user override)", got.FG)
	}
}

func TestLoadAllAddsUserOnlyTheme(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	userDir := filepath.Join(home, ".config", "cliamp", "themes")
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	custom := `accent = "#abcdef"
bright_fg = "#ffffff"
fg = "#112233"
green = "#44aa55"
yellow = "#ddcc44"
red = "#cc4455"
`
	if err := os.WriteFile(filepath.Join(userDir, "mytheme.toml"), []byte(custom), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	themes := LoadAll()
	var found bool
	for _, th := range themes {
		if th.Name == "mytheme" {
			found = true
			if th.Accent != "#abcdef" {
				t.Errorf("Accent = %q, want #abcdef", th.Accent)
			}
		}
	}
	if !found {
		t.Error("user theme mytheme not loaded")
	}
}

func TestLoadAllIgnoresInvalidUserTheme(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	userDir := filepath.Join(home, ".config", "cliamp", "themes")
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(userDir, "broken.toml"), []byte(`accent = "blue"`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	themes, errs := loadAll()
	for _, th := range themes {
		if th.Name == "broken" {
			t.Fatal("invalid custom theme was loaded")
		}
	}
	if len(errs) != 1 {
		t.Fatalf("loadAll() errors = %v, want 1 error for broken.toml", errs)
	}
	want := `skip broken.toml in ` + userDir + `: theme "broken": accent must be #RRGGBB`
	if got := errs[0].Error(); got != want {
		t.Errorf("loadAll() error = %q, want %q", got, want)
	}
}

func TestLoadAllLogsSkippedTheme(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	userDir := filepath.Join(home, ".config", "cliamp", "themes")
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(userDir, "broken.toml"), []byte(`accent = "blue"`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	logPath := filepath.Join(t.TempDir(), "cliamp.log")
	closeLog, err := applog.Init(logPath, applog.LevelWarn)
	if err != nil {
		t.Fatalf("applog.Init: %v", err)
	}
	t.Cleanup(func() { _ = closeLog() })

	LoadAll()

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	for _, want := range []string{"level=WARN", "broken.toml in " + userDir, "accent must be #RRGGBB"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("log = %q, want it to contain %q", data, want)
		}
	}
}

func TestLoadAllBuiltinThemesHaveNoErrors(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if _, errs := loadAll(); len(errs) != 0 {
		t.Errorf("loadAll() errors = %v, want none for the built-in themes", errs)
	}
}

func TestBuiltinThemesKeepStateColorsDistinct(t *testing.T) {
	for _, th := range LoadAll() {
		if th.IsDefault() {
			continue
		}
		if err := th.Validate(); err != nil {
			t.Fatalf("built-in theme %q is invalid: %v", th.Name, err)
		}
		for _, state := range []struct {
			name  string
			color string
		}{
			{"selection", th.Accent},
			{"focus", th.BrightFG},
			{"warning", th.Yellow},
			{"error", th.Red},
		} {
			if state.color == th.FG {
				t.Errorf("theme %q %s color matches disabled text", th.Name, state.name)
			}
		}
	}
}

func TestLoadAllIgnoresNonTomlFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	userDir := filepath.Join(home, ".config", "cliamp", "themes")
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(userDir, "notatheme.txt"), []byte("ignored"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	// Subdirectory should also be ignored.
	if err := os.MkdirAll(filepath.Join(userDir, "nested"), 0o755); err != nil {
		t.Fatalf("MkdirAll nested: %v", err)
	}

	themes := LoadAll()
	for _, th := range themes {
		if th.Name == "notatheme" || th.Name == "nested" {
			t.Errorf("non-toml entry %q leaked into LoadAll()", th.Name)
		}
	}
}

func TestLoadAllMissingUserDir(t *testing.T) {
	// HOME points at a dir where ~/.config/cliamp/themes doesn't exist.
	t.Setenv("HOME", t.TempDir())
	themes := LoadAll()
	if len(themes) == 0 {
		t.Error("LoadAll() with missing user dir should still return built-in themes")
	}
}

func TestLoadFS(t *testing.T) {
	const good = `bg = "#002b36"
accent = "#268bd2"
bright_fg = "#eee8d5"
fg = "#839496"
green = "#859900"
yellow = "#b58900"
red = "#dc322f"
`
	goodTheme := Theme{
		BG:       "#002b36",
		Accent:   "#268bd2",
		BrightFG: "#eee8d5",
		FG:       "#839496",
		Green:    "#859900",
		Yellow:   "#b58900",
		Red:      "#dc322f",
	}
	named := func(name string) Theme {
		th := goodTheme
		th.Name = name
		return th
	}

	tests := []struct {
		name    string
		files   fstest.MapFS
		want    map[string]Theme
		wantErr []string
	}{
		{
			name:  "good theme",
			files: fstest.MapFS{"themes/Solarized.toml": {Data: []byte(good)}},
			want:  map[string]Theme{"solarized": named("Solarized")},
		},
		{
			name: "theme with inline comments",
			files: fstest.MapFS{"themes/commented.toml": {Data: []byte(
				"# Solarized with notes\n" + strings.ReplaceAll(good, "\n", " # note\n"),
			)}},
			want: map[string]Theme{"commented": named("commented")},
		},
		{
			name:  "broken theme",
			files: fstest.MapFS{"themes/broken.toml": {Data: []byte(`accent = "blue"`)}},
			want:  map[string]Theme{},
			wantErr: []string{
				`skip broken.toml in test dir: theme "broken": accent must be #RRGGBB`,
			},
		},
		{
			name: "line too long",
			files: fstest.MapFS{
				"themes/long.toml":      {Data: []byte("# " + strings.Repeat("x", 70000))},
				"themes/Solarized.toml": {Data: []byte(good)},
			},
			want: map[string]Theme{"solarized": named("Solarized")},
			wantErr: []string{
				"skip long.toml in test dir: bufio.Scanner: token too long",
			},
		},
		{
			name:    "theme dir is a file",
			files:   fstest.MapFS{"themes": {Data: []byte(good)}},
			want:    map[string]Theme{},
			wantErr: []string{"read test dir: "},
		},
		{
			name: "non-toml file and nested dir",
			files: fstest.MapFS{
				"themes/notes.txt":         {Data: []byte(good)},
				"themes/nested/inner.toml": {Data: []byte(good)},
			},
			want: map[string]Theme{},
		},
		{
			name:  "missing dir",
			files: fstest.MapFS{"other/solarized.toml": {Data: []byte(good)}},
			want:  map[string]Theme{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := make(map[string]Theme)
			errs := loadFS(tt.files, "themes", "test dir", got)
			if !maps.Equal(got, tt.want) {
				t.Errorf("loadFS() themes = %+v, want %+v", got, tt.want)
			}
			if len(errs) != len(tt.wantErr) {
				t.Fatalf("loadFS() errors = %v, want %d errors", errs, len(tt.wantErr))
			}
			for i, err := range errs {
				if !strings.HasPrefix(err.Error(), tt.wantErr[i]) {
					t.Errorf("loadFS() error %d = %q, want prefix %q", i, err, tt.wantErr[i])
				}
			}
		})
	}
}
