package luaplugin

import lua "github.com/yuin/gopher-lua"

// sandbox removes the Lua standard library functions that load code, run
// programs or change files outside cliamp.fs. It does not hide data: a
// plugin can still read files and the environment. It also adds
// compatibility helpers missing from Lua 5.1 (e.g. utf8.char).
func sandbox(L *lua.LState) {
	// Remove top-level functions that can load/execute arbitrary code.
	for _, name := range []string{"dofile", "loadfile", "load", "loadstring", "require", "module"} {
		L.SetGlobal(name, lua.LNil)
	}
	L.SetGlobal("package", lua.LNil)
	L.SetGlobal("debug", lua.LNil)

	// Remove the io module entirely (replaced by cliamp.fs).
	L.SetGlobal("io", lua.LNil)

	// The base print writes to stdout. That corrupts the TUI, and before
	// approval it lets a plugin write into the trust prompt. print does
	// nothing here, and registerLogAPI sends it to plugins.log at runtime.
	L.SetGlobal("print", L.NewFunction(func(*lua.LState) int { return 0 }))

	// Keep time, date, clock and getenv from the os module. getenv reads
	// every variable of the cliamp environment, so it hides no secret.
	if os := L.GetGlobal("os"); os != lua.LNil {
		if tbl, ok := os.(*lua.LTable); ok {
			for _, fn := range []string{"execute", "remove", "rename", "exit", "setlocale", "tmpname"} {
				tbl.RawSetString(fn, lua.LNil)
			}
		}
	}

	// Provide utf8.char() for Lua 5.1 compatibility (GopherLua is 5.1).
	// Plugins need this for Braille character rendering in visualizers.
	utf8Tbl := L.NewTable()
	L.SetField(utf8Tbl, "char", L.NewFunction(luaUTF8Char))
	L.SetGlobal("utf8", utf8Tbl)
}

// luaUTF8Char converts one or more integer codepoints to a UTF-8 string.
// Equivalent to Lua 5.3's utf8.char().
func luaUTF8Char(L *lua.LState) int {
	n := L.GetTop()
	buf := make([]rune, n)
	for i := 1; i <= n; i++ {
		buf[i-1] = rune(L.CheckInt(i))
	}
	L.Push(lua.LString(string(buf)))
	return 1
}
