//go:build !windows && !darwin

package tray

import (
	"os/exec"

	"dpi/internal/app"
)

// Run has no tray on Linux: the app runs in the terminal until Ctrl+C.
func Run(a *app.App) { <-a.Done() }

// OpenURL opens url in the default browser (errors are ignored: nothing to show them in).
func OpenURL(url string) { exec.Command("xdg-open", url).Start() }
