package main

import (
	"os"
	"path/filepath"
	"testing"
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
