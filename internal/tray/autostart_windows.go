package tray

import (
	"os"

	"golang.org/x/sys/windows/registry"
)

const (
	runPath  = `Software\Microsoft\Windows\CurrentVersion\Run`
	runValue = "dpi-bypass"
)

func autostartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runPath, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(runValue)
	return err == nil
}

func setAutostart(on bool) error {
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
