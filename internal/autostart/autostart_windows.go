// Package autostart turns "start with the computer" on and off (Windows only).
package autostart

import (
	"os"

	"golang.org/x/sys/windows/registry"
)

const runValue = "dpi-bypass"

// runPath is the registry key Windows reads at login. It is a variable only so the
// tests can point it at a scratch key and never touch the user's real startup entry.
var runPath = `Software\Microsoft\Windows\CurrentVersion\Run`

// Supported reports whether this platform can start the app at login.
func Supported() bool { return true }

func Enabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runPath, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(runValue)
	return err == nil
}

func Set(on bool) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runPath, registry.SET_VALUE) // key may not exist on a fresh profile
	if err != nil {
		return err
	}
	defer k.Close()
	if !on {
		if err := k.DeleteValue(runValue); err != nil && err != registry.ErrNotExist {
			return err
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return k.SetStringValue(runValue, `"`+exe+`"`)
}
