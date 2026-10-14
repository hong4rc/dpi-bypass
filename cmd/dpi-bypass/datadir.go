package main

import (
	"os"
	"path/filepath"
	"strings"
)

// dataDir is where logs, settings, the site lists and the identify certificate
// live: next to the exe, so a downloaded exe in any folder is all that is needed.
// If that folder is read-only (Program Files, a mounted disk image), nothing
// could be saved there, so the app falls back to its own folder under configDir
// (on Windows %AppData%\dpi-bypass) instead of silently losing every setting.
// The same fallback applies when the exe sits in a place the package manager
// owns (a Homebrew Cellar folder, an .app bundle): it is writable, but an upgrade
// replaces that folder and would take the settings and the learned site lists with it.
func dataDir(exeDir, configDir string) string {
	if writable(exeDir) && !managedInstall(exeDir) {
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

// managedInstall reports whether dir belongs to Homebrew or an app bundle, which
// an upgrade replaces wholesale.
func managedInstall(dir string) bool {
	d := filepath.ToSlash(dir) + "/"
	return strings.Contains(d, "/Cellar/") || strings.Contains(d, ".app/Contents/")
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
