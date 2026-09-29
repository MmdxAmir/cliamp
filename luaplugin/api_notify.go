package luaplugin

import (
	"os/exec"

	lua "github.com/yuin/gopher-lua"
)

// registerNotifyAPI adds cliamp.notify(title, body) which sends a desktop
// notification via notify-send. Safe alternative to os.execute for this
// specific use case.
func registerNotifyAPI(L *lua.LState, cliamp *lua.LTable, p *Plugin) {
	L.SetField(cliamp, "notify", L.NewFunction(func(L *lua.LState) int {
		title := L.CheckString(1)
		body := L.OptString(2, "")

		args := []string{title}
		if body != "" {
			args = append(args, body)
		}

		path, err := exec.LookPath("notify-send")
		if err != nil {
			p.logger.log(p.installName, "warn", "notify-send not found: %v", err)
			return 0
		}

		cmd := exec.Command(path, args...)
		if err := cmd.Run(); err != nil {
			p.logger.log(p.installName, "error", "notify-send failed: %v", err)
		}
		return 0
	}))
}
