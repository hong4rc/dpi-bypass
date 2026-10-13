//go:build !windows

package tray

import (
	"os/exec"
	"runtime"

	"dpi/internal/app"
)

// Run has no tray on macOS/Linux (it would need CGO): the app runs in the
// terminal until Ctrl+C.
func Run(a *app.App) { <-a.Done() }

// OpenURL opens url in the default browser (errors are ignored: nothing to show them in).
func OpenURL(url string) {
	name := "xdg-open"
	if runtime.GOOS == "darwin" {
		name = "open"
	}
	exec.Command(name, url).Start()
}
