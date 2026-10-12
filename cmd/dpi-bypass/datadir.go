package main

import (
	"os"
	"path/filepath"
)

// dataDir is where logs, settings, the site lists and the identify certificate
// live: next to the exe, so a downloaded exe in any folder is all that is needed.
// If that folder is read-only (Program Files, a mounted disk image), nothing
// could be saved there, so the app falls back to its own folder under configDir
// (on Windows %AppData%\dpi-bypass) instead of silently losing every setting.
func dataDir(exeDir, configDir string) string {
	if writable(exeDir) {
		return exeDir
	}
	if configDir == "" {
		return exeDir // nowhere better; the app still runs, it just can not save
	}
	dir := filepath.Join(configDir, "dpi-bypass")
	if os.MkdirAll(dir, 0o755) != nil {
		return exeDir
	}
	return dir
}

// writable reports whether files can be created in dir.
func writable(dir string) bool {
	f, err := os.CreateTemp(dir, ".dpi-write-test-*")
	if err != nil {
		return false
	}
	f.Close()
	os.Remove(f.Name())
	return true
}
