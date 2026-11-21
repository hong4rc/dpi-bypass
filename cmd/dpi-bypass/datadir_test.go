package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDataStaysNextToTheExeWhenThatFolderIsWritable(t *testing.T) {
	exeDir, cfg := t.TempDir(), t.TempDir()
	if got := dataDir(exeDir, cfg); got != exeDir {
		t.Fatalf("got %q, want the exe folder %q", got, exeDir)
	}
	if entries, _ := os.ReadDir(exeDir); len(entries) != 0 {
		t.Fatalf("the write test must not leave files behind: %v", entries)
	}
	if entries, _ := os.ReadDir(cfg); len(entries) != 0 {
		t.Fatal("the config folder must not be used when it is not needed")
	}
}

func TestAReadOnlyExeFolderFallsBackToTheConfigFolder(t *testing.T) {
	// a path that is a file, not a folder: nothing can be created inside it, like a read-only location
	notADir := filepath.Join(t.TempDir(), "readonly")
	os.WriteFile(notADir, []byte("x"), 0o644)
	cfg := t.TempDir()

	got := dataDir(notADir, cfg)
	want := filepath.Join(cfg, "dpi-bypass")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if info, err := os.Stat(got); err != nil || !info.IsDir() {
		t.Fatalf("the fallback folder must be created: %v", err)
	}
	if !writable(got) {
		t.Fatal("the fallback folder must be usable")
	}
}

func TestWithNowhereBetterTheExeFolderIsKept(t *testing.T) {
	notADir := filepath.Join(t.TempDir(), "readonly")
	os.WriteFile(notADir, []byte("x"), 0o644)
	if got := dataDir(notADir, ""); got != notADir {
		t.Fatalf("got %q", got)
	}
}

func TestAnInstallThePackageManagerReplacesKeepsDataInTheConfigFolder(t *testing.T) {
	cfg := t.TempDir()
	for _, sub := range []string{"Cellar/dpi-bypass/0.3.1/bin", "DPI Bypass.app/Contents/MacOS"} {
		exeDir := filepath.Join(t.TempDir(), sub)
		os.MkdirAll(exeDir, 0o755)
		want := filepath.Join(cfg, "dpi-bypass")
		if got := dataDir(exeDir, cfg); got != want {
			t.Fatalf("%s: got %q, want %q", sub, got, want)
		}
	}
}

func TestStartingThroughTheHomebrewSymlinkStillCountsAsAHomebrewInstall(t *testing.T) {
	root := t.TempDir()
	cellar := filepath.Join(root, "Cellar/dpi-bypass/0.3.3/bin")
	bin := filepath.Join(root, "bin")
	os.MkdirAll(cellar, 0o755)
	os.MkdirAll(bin, 0o755)
	os.WriteFile(filepath.Join(cellar, "dpi-bypass"), []byte("x"), 0o755)
	if err := os.Symlink("../Cellar/dpi-bypass/0.3.3/bin/dpi-bypass", filepath.Join(bin, "dpi-bypass")); err != nil {
		t.Skip("symlinks not available:", err)
	}

	real, link := exeDirs(filepath.Join(bin, "dpi-bypass"))
	if want, _ := filepath.EvalSymlinks(cellar); real != want {
		t.Fatalf("real folder: got %q, want %q", real, want)
	}
	if link != bin {
		t.Fatalf("link folder: got %q, want %q", link, bin)
	}
	cfg := t.TempDir()
	if got, want := dataDir(real, cfg), filepath.Join(cfg, "dpi-bypass"); got != want {
		t.Fatalf("got %q, want the config folder %q", got, want)
	}
}

func TestAPlainProgramHasOneFolder(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "dpi-bypass")
	os.WriteFile(exe, []byte("x"), 0o755)
	real, link := exeDirs(exe)
	if want, _ := filepath.EvalSymlinks(dir); real != want && real != dir {
		t.Fatalf("real folder: got %q", real)
	}
	if link != dir {
		t.Fatalf("link folder: got %q", link)
	}
}

func TestDataSavedNextToTheSymlinkMovesToTheDataFolder(t *testing.T) {
	from, dir := t.TempDir(), t.TempDir()
	old := time.Now().Add(-time.Hour)
	write := func(d, name, body string, at time.Time) {
		p := filepath.Join(d, name)
		os.WriteFile(p, []byte(body), 0o644)
		os.Chtimes(p, at, at)
	}
	write(from, "dpi-info.log", "log", time.Now())
	write(from, "bypass.txt", "learned", time.Now())
	write(dir, "bypass.txt", "stale", old)
	write(from, "settings.json", "older", old)
	write(dir, "settings.json", "current", time.Now())
	write(from, "devices.json", "names", time.Now())

	moved := adoptData(from, dir)

	read := func(d, name string) string { b, _ := os.ReadFile(filepath.Join(d, name)); return string(b) }
	if read(dir, "bypass.txt") != "learned" {
		t.Fatal("a newer bypass.txt must replace the stale one")
	}
	if read(dir, "devices.json") != "names" {
		t.Fatal("a file the data folder lacks must move")
	}
	if read(dir, "settings.json") != "current" || read(from, "settings.json") != "older" {
		t.Fatal("an older copy must not replace the current one, and must be left where it is")
	}
	if _, err := os.Stat(filepath.Join(from, "bypass.txt")); err == nil {
		t.Fatal("a moved file must leave the old folder")
	}
	if len(moved) != 3 {
		t.Fatalf("moved %v, want dpi-info.log, bypass.txt and devices.json", moved)
	}
}

func TestAFolderTheAppNeverRanFromIsLeftAlone(t *testing.T) {
	from, dir := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(from, "settings.json"), []byte("another program"), 0o644)
	if moved := adoptData(from, dir); len(moved) != 0 {
		t.Fatalf("moved %v from a folder without dpi-info.log", moved)
	}
	if _, err := os.Stat(filepath.Join(from, "settings.json")); err != nil {
		t.Fatal("the other program's file must stay")
	}
}
