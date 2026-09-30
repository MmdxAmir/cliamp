package model

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// keyCodeNames gives the key name of each tea key code that a handler
// switches on through msg.Code.
var keyCodeNames = map[string]string{
	"KeyEscape": "esc", "KeyEnter": "enter", "KeySpace": "space", "KeyTab": "tab",
	"KeyBackspace": "backspace", "KeyDelete": "delete",
	"KeyUp": "up", "KeyDown": "down", "KeyLeft": "left", "KeyRight": "right",
	"KeyHome": "home", "KeyEnd": "end", "KeyPgUp": "pgup", "KeyPgDown": "pgdown",
}

// modelFuncs parses the non-test Go files of the package and returns the
// functions and methods by name.
func modelFuncs(t *testing.T) map[string]*ast.FuncDecl {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	funcs := make(map[string]*ast.FuncDecl)
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			if _, dup := funcs[fd.Name.Name]; dup {
				// Two declarations share the name. Mark it, so a lookup fails.
				funcs[fd.Name.Name] = nil
				continue
			}
			funcs[fd.Name.Name] = fd
		}
	}
	return funcs
}

// lookupFunc returns the declaration of name, or fails the test.
func lookupFunc(t *testing.T, funcs map[string]*ast.FuncDecl, name string) *ast.FuncDecl {
	t.Helper()
	fd, ok := funcs[name]
	if !ok {
		t.Fatalf("no function %s in package model", name)
	}
	if fd == nil {
		t.Fatalf("more than one function is named %s", name)
	}
	return fd
}

// handlerKeys returns the keys that fd handles itself. A key counts when fd
// compares it with msg.String(), with a variable that holds msg.String(), or
// with a string parameter named key. A tea key code in a switch on msg.Code
// counts too. The keys of the functions that fd calls do not count.
func handlerKeys(t *testing.T, fd *ast.FuncDecl) []string {
	t.Helper()
	holders := make(map[string]bool)
	for _, field := range fd.Type.Params.List {
		if typ, ok := field.Type.(*ast.Ident); ok && typ.Name == "string" {
			for _, name := range field.Names {
				if name.Name == "key" {
					holders[name.Name] = true
				}
			}
		}
	}
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		if assign, ok := n.(*ast.AssignStmt); ok && len(assign.Rhs) == 1 && isMsgString(assign.Rhs[0]) {
			for _, lhs := range assign.Lhs {
				if id, ok := lhs.(*ast.Ident); ok {
					holders[id.Name] = true
				}
			}
		}
		return true
	})
	isKey := func(e ast.Expr) bool {
		if isMsgString(e) {
			return true
		}
		id, ok := e.(*ast.Ident)
		return ok && holders[id.Name]
	}

	var keys []string
	add := func(e ast.Expr) {
		var key string
		switch v := e.(type) {
		case *ast.BasicLit:
			if v.Kind != token.STRING {
				return
			}
			key, _ = strconv.Unquote(v.Value)
		case *ast.SelectorExpr:
			pkg, ok := v.X.(*ast.Ident)
			if !ok || pkg.Name != "tea" {
				return
			}
			if key, ok = keyCodeNames[v.Sel.Name]; !ok {
				t.Fatalf("%s switches on tea.%s. Add it to keyCodeNames.", fd.Name.Name, v.Sel.Name)
			}
		}
		if key != "" && !slices.Contains(keys, key) {
			keys = append(keys, key)
		}
	}
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.SwitchStmt:
			if v.Tag == nil || !isKey(v.Tag) && !isMsgCode(v.Tag) {
				return true
			}
			for _, stmt := range v.Body.List {
				for _, e := range stmt.(*ast.CaseClause).List {
					add(e)
				}
			}
		case *ast.BinaryExpr:
			if v.Op != token.EQL && v.Op != token.NEQ {
				return true
			}
			if isKey(v.X) {
				add(v.Y)
			} else if isKey(v.Y) {
				add(v.X)
			}
		}
		return true
	})
	return keys
}

// handlerCalls returns the names of the m.handle...Key methods that fd calls.
func handlerCalls(fd *ast.FuncDecl) []string {
	var names []string
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		recv, ok := sel.X.(*ast.Ident)
		name := sel.Sel.Name
		if ok && recv.Name == "m" && strings.HasPrefix(name, "handle") && strings.HasSuffix(name, "Key") && !slices.Contains(names, name) {
			names = append(names, name)
		}
		return true
	})
	return names
}

// isMsgString reports whether e is msg.String().
func isMsgString(e ast.Expr) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "String" {
		return false
	}
	recv, ok := sel.X.(*ast.Ident)
	return ok && recv.Name == "msg"
}

// isMsgCode reports whether e is msg.Code.
func isMsgCode(e ast.Expr) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Code" {
		return false
	}
	recv, ok := sel.X.(*ast.Ident)
	return ok && recv.Name == "msg"
}

// TestHandlerKeysFindsEveryForm checks the walker on the forms that the key
// handlers use.
func TestHandlerKeysFindsEveryForm(t *testing.T) {
	src := `package model
func (m *Model) handleProbeKey(msg tea.KeyPressMsg) tea.Cmd {
	if msg.String() == "ctrl+c" || "ctrl+z" != msg.String() {
		return nil
	}
	key := msg.String()
	if key == "N" {
		return m.handleOtherKey(msg)
	}
	switch key {
	case "a", "b":
	}
	switch msg.String() {
	case "c":
		m.handleOtherKey(msg)
	}
	switch msg.Code {
	case tea.KeyEscape, tea.KeyEnter:
	}
	switch m.focus {
	case "not a key":
	}
	if msg.Code == tea.KeySpace || m.name == "not a key" {
		m.editText("field", &m.name, msg)
	}
	return nil
}
func shortcut(key string) string {
	switch key {
	case "S":
		return "spotify"
	}
	return ""
}`
	file, err := parser.ParseFile(token.NewFileSet(), "probe.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	probe := file.Decls[0].(*ast.FuncDecl)
	if got, want := handlerKeys(t, probe), []string{"ctrl+c", "ctrl+z", "N", "a", "b", "c", "esc", "enter"}; !slices.Equal(got, want) {
		t.Errorf("handlerKeys = %q, want %q", got, want)
	}
	if got, want := handlerCalls(probe), []string{"handleOtherKey"}; !slices.Equal(got, want) {
		t.Errorf("handlerCalls = %q, want %q", got, want)
	}
	if got, want := handlerKeys(t, file.Decls[1].(*ast.FuncDecl)), []string{"S"}; !slices.Equal(got, want) {
		t.Errorf("handlerKeys(shortcut) = %q, want %q", got, want)
	}
}
