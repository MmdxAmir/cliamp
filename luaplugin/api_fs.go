package luaplugin

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	lua "github.com/yuin/gopher-lua"

	"github.com/bjarneo/cliamp/internal/appdir"
	"github.com/bjarneo/cliamp/ipc"
)

// cachedWriteRules holds the write rules for this process. The paths they
// name never change at runtime.
var cachedWriteRules = sync.OnceValue(loadWriteRules)

// writeRules bounds the paths that plugins can write. Every entry is
// canonicalized like the paths that isWriteAllowed checks, so a symlinked
// dir (e.g. /tmp -> /private/tmp on macOS) cannot bypass the prefix checks.
type writeRules struct {
	allow []string // directories that plugins can write inside
	deny  []string // paths inside allow that plugins cannot write, with their subtrees
}

// loadWriteRules builds the write rules from the current environment.
func loadWriteRules() writeRules {
	var r writeRules
	add := func(list *[]string, path string) {
		if abs, ok := canonicalExistingPath(path); ok {
			*list = append(*list, normalizeWritePath(abs))
		}
	}
	add(&r.allow, "/tmp")
	add(&r.allow, os.TempDir())
	configDir, configErr := appdir.Dir()
	if configErr == nil {
		add(&r.allow, configDir)
	}
	if home, err := os.UserHomeDir(); err == nil {
		add(&r.allow, filepath.Join(home, ".local", "share", "cliamp"))
		add(&r.allow, filepath.Join(home, "Music", "cliamp"))
	}

	// A plugin that writes these could approve its own code in
	// plugins/.trust.json, add a binary to the exec allowlist in config.toml,
	// change the stations in radios.toml, break IPC, or erase what it logged.
	if pluginDir, err := appdir.PluginDir(); err == nil {
		add(&r.deny, pluginDir)
	}
	if configErr == nil {
		add(&r.deny, filepath.Join(configDir, "config.toml"))
		add(&r.deny, filepath.Join(configDir, "radios.toml"))
		add(&r.deny, filepath.Join(configDir, pluginLogName))
	}
	add(&r.deny, ipc.DefaultSocketPath())
	return r
}

// canonicalExistingPath resolves symlinks on the deepest existing ancestor of
// path, re-appending any non-existent tail (e.g. a file about to be created).
// This prevents a symlink planted inside an allowed dir from redirecting a
// write to a target outside it; a purely lexical check cannot catch that.
func canonicalExistingPath(path string) (string, bool) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	suffix := ""
	cur := abs
	for {
		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			if suffix != "" {
				resolved = filepath.Join(resolved, suffix)
			}
			return resolved, true
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return abs, true // nothing along the path exists yet
		}
		suffix = filepath.Join(filepath.Base(cur), suffix)
		cur = parent
	}
}

// isWriteAllowed checks if a path is within one of the allowed write
// directories and outside every denied path, resolving symlinks on both
// sides first.
func isWriteAllowed(path string) bool {
	return cachedWriteRules().allows(path)
}

// allows reports whether r permits a write to path.
func (r writeRules) allows(path string) bool {
	abs, ok := canonicalExistingPath(path)
	if !ok {
		return false
	}
	// An NTFS stream name such as config.toml::$DATA writes to config.toml.
	if runtime.GOOS == "windows" && strings.Contains(abs[len(filepath.VolumeName(abs)):], ":") {
		return false
	}
	abs = normalizeWritePath(abs)
	for _, dir := range r.deny {
		if isWithin(abs, dir) {
			return false
		}
	}
	for _, dir := range r.allow {
		if isWithin(abs, dir) {
			return true
		}
	}
	return false
}

// isWithin reports whether path is dir or lies under dir. Both paths must be
// canonical and normalized.
func isWithin(path, dir string) bool {
	return path == dir || strings.HasPrefix(path, dir+string(os.PathSeparator))
}

// normalizeWritePath canonicalizes an absolute path for prefix comparison:
// cleaned and, on Windows and macOS, case-folded. The default file systems on
// both are case-insensitive, so plugins/ and Plugins/ name the same dir.
func normalizeWritePath(path string) string {
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		path = strings.ToLower(path)
	}
	return path
}

// registerFSAPI adds cliamp.fs.{write,append,read,remove,exists} to the cliamp table.
func registerFSAPI(L *lua.LState, cliamp *lua.LTable) {
	tbl := L.NewTable()

	// cliamp.fs.write(path, content)
	L.SetField(tbl, "write", L.NewFunction(func(L *lua.LState) int {
		path := L.CheckString(1)
		content := L.CheckString(2)
		if !isWriteAllowed(path) {
			L.ArgError(1, "write not allowed to this path")
			return 0
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		L.Push(lua.LTrue)
		return 1
	}))

	// cliamp.fs.append(path, content)
	L.SetField(tbl, "append", L.NewFunction(func(L *lua.LState) int {
		path := L.CheckString(1)
		content := L.CheckString(2)
		if !isWriteAllowed(path) {
			L.ArgError(1, "write not allowed to this path")
			return 0
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		_, err = f.WriteString(content)
		f.Close()
		if err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		L.Push(lua.LTrue)
		return 1
	}))

	// cliamp.fs.read(path) -> string (max 1MB)
	L.SetField(tbl, "read", L.NewFunction(func(L *lua.LState) int {
		path := L.CheckString(1)
		f, err := os.Open(path)
		if err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		defer f.Close()
		const maxSize = 1 << 20 // 1MB
		// Read one byte past the cap so an oversized file is detected without
		// pulling the whole thing into memory, then reject it explicitly
		// rather than returning a silently truncated value.
		data, err := io.ReadAll(io.LimitReader(f, maxSize+1))
		if err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		if len(data) > maxSize {
			L.Push(lua.LNil)
			L.Push(lua.LString("file exceeds 1MB read limit"))
			return 2
		}
		L.Push(lua.LString(string(data)))
		return 1
	}))

	// cliamp.fs.remove(path)
	L.SetField(tbl, "remove", L.NewFunction(func(L *lua.LState) int {
		path := L.CheckString(1)
		if !isWriteAllowed(path) {
			L.ArgError(1, "remove not allowed for this path")
			return 0
		}
		if err := os.Remove(path); err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		L.Push(lua.LTrue)
		return 1
	}))

	// cliamp.fs.exists(path) -> boolean
	L.SetField(tbl, "exists", L.NewFunction(func(L *lua.LState) int {
		path := L.CheckString(1)
		_, err := os.Stat(path)
		L.Push(lua.LBool(err == nil))
		return 1
	}))

	// cliamp.fs.mkdir(path) — recursive; path must be in write allowlist.
	L.SetField(tbl, "mkdir", L.NewFunction(func(L *lua.LState) int {
		path := L.CheckString(1)
		if !isWriteAllowed(path) {
			L.ArgError(1, "mkdir not allowed for this path")
			return 0
		}
		if err := os.MkdirAll(path, 0o755); err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		L.Push(lua.LTrue)
		return 1
	}))

	// cliamp.fs.listdir(path) -> {names}, err
	// Reading is unrestricted (matches cliamp.fs.read); returns entry names only.
	L.SetField(tbl, "listdir", L.NewFunction(func(L *lua.LState) int {
		path := L.CheckString(1)
		entries, err := os.ReadDir(path)
		if err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		result := L.NewTable()
		for i, e := range entries {
			result.RawSetInt(i+1, lua.LString(e.Name()))
		}
		L.Push(result)
		return 1
	}))

	L.SetField(cliamp, "fs", tbl)
}
