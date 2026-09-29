// Package theme handles loading and parsing color themes from TOML files.
package theme

import (
	"bufio"
	"cmp"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/bjarneo/cliamp/internal/appdir"
)

//go:embed themes/*.toml
var builtinThemes embed.FS

// DefaultName is the display name for the built-in ANSI fallback theme.
const DefaultName = "Default - Terminal colors"

// Theme holds a named color scheme with hex color values.
type Theme struct {
	Name     string
	BG       string
	Accent   string // hex
	BrightFG string
	FG       string
	Green    string
	Yellow   string
	Red      string
}

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// IsDefault returns true if this is the sentinel default theme (no hex values).
func (t Theme) IsDefault() bool {
	return t.BG == "" && t.Accent == "" && t.Green == "" && t.BrightFG == ""
}

// Validate ensures a custom theme supplies the complete six-color foreground
// palette in CSS hex notation. Background is optional for custom themes.
func (t Theme) Validate() error {
	if t.IsDefault() {
		return nil
	}
	for _, color := range []struct {
		name  string
		value string
	}{
		{"accent", t.Accent},
		{"bright_fg", t.BrightFG},
		{"fg", t.FG},
		{"green", t.Green},
		{"yellow", t.Yellow},
		{"red", t.Red},
	} {
		if color.value == "" {
			return fmt.Errorf("theme %q: %s is required", t.Name, color.name)
		}
		if !hexColor.MatchString(color.value) {
			return fmt.Errorf("theme %q: %s must be #RRGGBB", t.Name, color.name)
		}
	}
	if t.BG != "" && !hexColor.MatchString(t.BG) {
		return fmt.Errorf("theme %q: bg must be #RRGGBB", t.Name)
	}
	return nil
}

// Default returns a sentinel "Default" theme with empty hex values,
// signaling that ANSI fallback colors should be used.
func Default() Theme {
	return Theme{Name: DefaultName}
}

// Parse reads flat TOML key=value lines from r and returns a Theme.
// Uses the same manual parsing approach as config/config.go.
func Parse(name string, r io.Reader) (Theme, error) {
	t := Theme{Name: name}
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = parseValue(val)

		switch key {
		case "bg":
			t.BG = val
		case "accent":
			t.Accent = val
		case "bright_fg":
			t.BrightFG = val
		case "fg":
			t.FG = val
		case "red":
			t.Red = val
		case "yellow":
			t.Yellow = val
		case "green":
			t.Green = val
		}
	}
	return t, scanner.Err()
}

// parseValue returns the value of a key = value line without its quotes and
// without a trailing # comment. A # outside quotes starts a comment only
// after white space, so an unquoted #RRGGBB value stays whole.
func parseValue(val string) string {
	val = strings.TrimSpace(val)
	if val != "" && (val[0] == '"' || val[0] == '\'') {
		if end := strings.IndexByte(val[1:], val[0]); end >= 0 {
			rest := strings.TrimSpace(val[end+2:])
			if rest == "" || rest[0] == '#' {
				return val[1 : end+1]
			}
		}
	}
	for i := 1; i < len(val); i++ {
		if val[i] == '#' && (val[i-1] == ' ' || val[i-1] == '\t') {
			val = strings.TrimSpace(val[:i])
			break
		}
	}
	return strings.Trim(val, `"'`)
}

// Find returns the theme called name, matched case-insensitively across the
// built-in and user themes. An empty name, "default", or DefaultName give the
// ANSI default. ok is false when no theme has that name.
func Find(name string) (Theme, bool) {
	if name == "" || strings.EqualFold(name, "default") || strings.EqualFold(name, DefaultName) {
		return Default(), true
	}
	for _, t := range LoadAll() {
		if strings.EqualFold(t.Name, name) {
			return t, true
		}
	}
	return Theme{}, false
}

// LoadAll loads built-in themes and user custom themes from
// ~/.config/cliamp/themes/*.toml. User themes override built-in
// themes with the same name. Returns a sorted list.
func LoadAll() []Theme {
	themes := make(map[string]Theme)

	// Load embedded built-in themes (lower priority).
	loadFS(builtinThemes, "themes", themes)

	// Load user custom themes (override built-in if same name).
	dir, err := appdir.Dir()
	if err == nil {
		loadFS(os.DirFS(filepath.Join(dir, "themes")), ".", themes)
	}

	// Sort by name.
	result := make([]Theme, 0, len(themes))
	for _, t := range themes {
		result = append(result, t)
	}
	slices.SortFunc(result, func(a, b Theme) int {
		return cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return result
}

// loadFS parses the .toml files in dir of fsys and stores each valid theme
// in themes under its lower-case file name.
func loadFS(fsys fs.FS, dir string, themes map[string]Theme) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".toml")
		f, err := fsys.Open(path.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		t, err := Parse(name, f)
		f.Close()
		if err != nil || t.Validate() != nil {
			continue
		}
		themes[strings.ToLower(name)] = t
	}
}
