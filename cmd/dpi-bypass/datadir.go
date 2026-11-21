package main

import (
	"os"
	"path/filepath"
	"strings"

	"dpi/internal/identify"
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

// exeDirs returns the folder of the real program file and the folder of the path
// it was started through. They differ when it was started through a symlink, as
// Homebrew does: /opt/homebrew/bin/dpi-bypass points into the Cellar, and only the
// real folder shows that Homebrew owns the install.
func exeDirs(exe string) (real, link string) {
	link = filepath.Dir(exe)
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		return filepath.Dir(r), link
	}
	return link, link
}

// dataFiles are the files the app keeps in its data folder.
var dataFiles = []string{"settings.json", "blacklist.txt", "bypass.txt", "devices.json", identify.CertFile, "dpi-info.log", "dpi-debug.log"}

// adoptData moves the app's files from a folder an earlier version saved into
// (next to the symlink it was started through) into dir, and returns the names it
// moved. It only touches a folder holding dpi-info.log, which every run writes,
// so a settings.json of another program is never taken. A file moves when dir has
// no copy of it or an older one; an older copy in from is left where it is.
func adoptData(from, dir string) []string {
	if from == dir {
		return nil
	}
	if _, err := os.Stat(filepath.Join(from, "dpi-info.log")); err != nil {
		return nil
	}
	var moved []string
	for _, name := range dataFiles {
		src, dst := filepath.Join(from, name), filepath.Join(dir, name)
		old, err := os.Stat(src)
		if err != nil {
			continue
		}
		if cur, err := os.Stat(dst); err == nil && !cur.ModTime().Before(old.ModTime()) {
			continue
		}
		if move(src, dst) == nil {
			moved = append(moved, name)
		}
	}
	return moved
}

// move renames src to dst, copying when they are on different disks.
func move(src, dst string) error {
	if os.Rename(src, dst) == nil {
		return nil
	}
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if err := os.WriteFile(dst, b, info.Mode().Perm()); err != nil {
		return err
	}
	os.Chtimes(dst, info.ModTime(), info.ModTime())
	return os.Remove(src)
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
