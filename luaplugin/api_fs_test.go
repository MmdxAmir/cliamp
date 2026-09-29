package luaplugin

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	lua "github.com/yuin/gopher-lua"
)

func fsAllowedPath(name string) string {
	return filepath.Join(os.TempDir(), name)
}

func fsDisallowedPath() string {
	if runtime.GOOS == "windows" {
		if root := os.Getenv("WINDIR"); root != "" {
			return filepath.Join(root, "System32", "drivers", "etc", "hosts")
		}
		return `C:\Windows\System32\drivers\etc\hosts`
	}
	return "/etc/passwd"
}

func TestFSWriteAndRead(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	cliamp := L.NewTable()
	registerFSAPI(L, cliamp)
	L.SetGlobal("cliamp", cliamp)

	tmp := fsAllowedPath("cliamp-test-" + t.Name())
	defer os.Remove(tmp)

	L.SetGlobal("path", lua.LString(tmp))
	err := L.DoString(`
		local ok = cliamp.fs.write(path, "hello world")
		_G.write_ok = ok
		local content = cliamp.fs.read(path)
		_G.content = content
	`)
	if err != nil {
		t.Fatal(err)
	}

	if L.GetGlobal("write_ok") != lua.LTrue {
		t.Fatal("fs.write returned non-true")
	}
	if L.GetGlobal("content").String() != "hello world" {
		t.Fatalf("fs.read = %q, want %q", L.GetGlobal("content").String(), "hello world")
	}
}

func TestFSAppend(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	cliamp := L.NewTable()
	registerFSAPI(L, cliamp)
	L.SetGlobal("cliamp", cliamp)

	tmp := fsAllowedPath("cliamp-test-append-" + t.Name())
	defer os.Remove(tmp)

	L.SetGlobal("path", lua.LString(tmp))
	err := L.DoString(`
		cliamp.fs.write(path, "hello")
		cliamp.fs.append(path, " world")
		_G.content = cliamp.fs.read(path)
	`)
	if err != nil {
		t.Fatal(err)
	}

	if got := L.GetGlobal("content").String(); got != "hello world" {
		t.Fatalf("after append, content = %q, want %q", got, "hello world")
	}
}

func TestFSExists(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	cliamp := L.NewTable()
	registerFSAPI(L, cliamp)
	L.SetGlobal("cliamp", cliamp)

	tmp := fsAllowedPath("cliamp-test-exists-" + t.Name())
	os.WriteFile(tmp, []byte("x"), 0o644)
	defer os.Remove(tmp)

	L.SetGlobal("path", lua.LString(tmp))
	L.SetGlobal("fake", lua.LString(fsAllowedPath("cliamp-definitely-not-here")))
	err := L.DoString(`
		_G.exists = cliamp.fs.exists(path)
		_G.not_exists = cliamp.fs.exists(fake)
	`)
	if err != nil {
		t.Fatal(err)
	}

	if L.GetGlobal("exists") != lua.LTrue {
		t.Fatal("fs.exists returned false for existing file")
	}
	if L.GetGlobal("not_exists") != lua.LFalse {
		t.Fatal("fs.exists returned true for non-existing file")
	}
}

func TestFSRemove(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	cliamp := L.NewTable()
	registerFSAPI(L, cliamp)
	L.SetGlobal("cliamp", cliamp)

	tmp := fsAllowedPath("cliamp-test-remove-" + t.Name())
	os.WriteFile(tmp, []byte("x"), 0o644)

	L.SetGlobal("path", lua.LString(tmp))
	err := L.DoString(`
		_G.remove_ok = cliamp.fs.remove(path)
		_G.exists_after = cliamp.fs.exists(path)
	`)
	if err != nil {
		t.Fatal(err)
	}

	if L.GetGlobal("remove_ok") != lua.LTrue {
		t.Fatal("fs.remove returned non-true")
	}
	if L.GetGlobal("exists_after") != lua.LFalse {
		t.Fatal("file still exists after remove")
	}
}

// testHomeOutsideTemp sets HOME to a new dir outside the temp roots, so that
// only the cliamp roots can make a path under it writable.
func testHomeOutsideTemp(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Skipf("get working dir: %v", err)
	}
	home, err := os.MkdirTemp(wd, ".testhome-")
	if err != nil {
		t.Skipf("create home outside the temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	canonHome, _ := canonicalExistingPath(home)
	for _, tmp := range []string{"/tmp", os.TempDir()} {
		canonTmp, _ := canonicalExistingPath(tmp)
		if isWithin(normalizeWritePath(canonHome), normalizeWritePath(canonTmp)) {
			t.Skipf("working dir %s is inside the temp dir %s", wd, tmp)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir reads it on Windows
	return home
}

func TestIsWriteAllowed(t *testing.T) {
	caseFolded := runtime.GOOS == "windows" || runtime.GOOS == "darwin"
	layouts := []struct {
		name       string
		linkConfig bool // ~/.config/cliamp is a symlink to a dotfiles dir
	}{
		{name: "real config dir"},
		{name: "symlinked config dir", linkConfig: true},
	}
	for _, layout := range layouts {
		t.Run(layout.name, func(t *testing.T) {
			home := testHomeOutsideTemp(t)
			cfg := filepath.Join(home, ".config", "cliamp")
			if layout.linkConfig {
				target := filepath.Join(home, "dotfiles", "cliamp")
				mustMkdirAll(t, target)
				mustMkdirAll(t, filepath.Dir(cfg))
				if err := os.Symlink(target, cfg); err != nil {
					t.Skipf("symlink: %v", err)
				}
			}
			plugins := filepath.Join(cfg, "plugins")
			data := filepath.Join(home, ".local", "share", "cliamp")
			mustMkdirAll(t, filepath.Join(plugins, "pkg"))
			mustMkdirAll(t, data)
			for _, f := range []string{
				filepath.Join(plugins, ".trust.json"),
				filepath.Join(plugins, "hello.lua"),
				filepath.Join(plugins, "pkg", "init.lua"),
				filepath.Join(cfg, "config.toml"),
				filepath.Join(cfg, "radios.toml"),
				filepath.Join(cfg, "plugins.log"),
			} {
				if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			// Symlinks inside an allowed dir that point at denied paths.
			linked := true
			for name, target := range map[string]string{
				"to-plugins":     plugins,
				"to-config.toml": filepath.Join(cfg, "config.toml"),
				"to-etc":         filepath.Dir(fsDisallowedPath()),
			} {
				if err := os.Symlink(target, filepath.Join(data, name)); err != nil {
					linked = false
				}
			}

			rules := loadWriteRules()
			tests := []struct {
				name string
				path string
				want bool
				link bool // the path goes through a symlink in data
			}{
				{name: "temp dir", path: fsAllowedPath("test.txt"), want: true},
				{name: "system file", path: fsDisallowedPath()},
				{name: "home dotfile", path: filepath.Join(home, ".bashrc")},
				{name: "config dir", path: cfg, want: true},
				{name: "new file in config dir", path: filepath.Join(cfg, "notes.txt"), want: true},
				{name: "theme file", path: filepath.Join(cfg, "themes", "mine.toml"), want: true},
				{name: "own data dir", path: filepath.Join(data, "plugins", "hello", "store.json"), want: true},
				{name: "music dir", path: filepath.Join(home, "Music", "cliamp", "album", "01.mp3"), want: true},
				{name: "plugins dir", path: plugins},
				{name: "trust manifest", path: filepath.Join(plugins, ".trust.json")},
				{name: "existing plugin", path: filepath.Join(plugins, "hello.lua")},
				{name: "new plugin", path: filepath.Join(plugins, "evil.lua")},
				{name: "dir plugin", path: filepath.Join(plugins, "pkg", "init.lua")},
				{name: "new dir plugin", path: filepath.Join(plugins, "evil", "init.lua")},
				{name: "config.toml", path: filepath.Join(cfg, "config.toml")},
				{name: "config.toml through traversal", path: filepath.Join(cfg, "themes", "..", "config.toml")},
				{name: "radios.toml", path: filepath.Join(cfg, "radios.toml")},
				{name: "IPC socket", path: filepath.Join(cfg, "cliamp.sock")},
				{name: "plugin log", path: filepath.Join(cfg, "plugins.log")},
				{name: "plugins dir with other case", path: filepath.Join(cfg, "PLUGINS", "evil.lua"), want: !caseFolded},
				{name: "config.toml with other case", path: filepath.Join(cfg, "Config.toml"), want: !caseFolded},
				{name: "config.toml stream", path: filepath.Join(cfg, "config.toml::$DATA"), want: runtime.GOOS != "windows"},
				{name: "symlink to plugins dir", path: filepath.Join(data, "to-plugins", "evil.lua"), link: true},
				{name: "symlink to config.toml", path: filepath.Join(data, "to-config.toml"), link: true},
				{name: "symlink out of allowed dir", path: filepath.Join(data, "to-etc", "new.txt"), link: true},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					if tt.link && !linked {
						t.Skip("symlinks are not available")
					}
					if got := rules.allows(tt.path); got != tt.want {
						t.Errorf("allows(%q) = %v, want %v", tt.path, got, tt.want)
					}
				})
			}
		})
	}
}

func mustMkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestFSMkdirAndListdir(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	cliamp := L.NewTable()
	registerFSAPI(L, cliamp)
	L.SetGlobal("cliamp", cliamp)

	base := fsAllowedPath("cliamp-test-mkdir-" + t.Name())
	defer os.RemoveAll(base)

	L.SetGlobal("base", lua.LString(base))
	err := L.DoString(`
		_G.mkdir_ok = cliamp.fs.mkdir(base .. "/sub")
		cliamp.fs.write(base .. "/a.txt", "a")
		cliamp.fs.write(base .. "/b.txt", "b")
		local names, err = cliamp.fs.listdir(base)
		_G.names = names
		_G.err = err
	`)
	if err != nil {
		t.Fatal(err)
	}
	if L.GetGlobal("mkdir_ok") != lua.LTrue {
		t.Fatal("fs.mkdir returned non-true")
	}
	names, ok := L.GetGlobal("names").(*lua.LTable)
	if !ok {
		t.Fatalf("listdir returned %T, want table", L.GetGlobal("names"))
	}
	if n := names.Len(); n != 3 {
		t.Fatalf("listdir returned %d entries, want 3", n)
	}
}

func TestFSMkdirRejectsOutsideAllowlist(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	cliamp := L.NewTable()
	registerFSAPI(L, cliamp)
	L.SetGlobal("cliamp", cliamp)

	err := L.DoString(fmt.Sprintf("cliamp.fs.mkdir(%q)", fsDisallowedPath()))
	if err == nil {
		t.Fatal("expected error for path outside allowlist")
	}
}
