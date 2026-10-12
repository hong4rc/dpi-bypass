//go:build !windows

package tray

import "dpi/internal/app"

// Run has no tray on macOS/Linux (it would need CGO): the app runs in the
// terminal until Ctrl+C.
func Run(a *app.App) { <-a.Done() }
