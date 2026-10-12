package blacklist

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewCreatesDefaultsAndSkipsComments(t *testing.T) {
	l := New(filepath.Join(t.TempDir(), "bl.txt"))
	items := l.Items()
	if len(items) == 0 || items[0] != "riotgames.com" {
		t.Fatalf("defaults not loaded: %v", items)
	}
	for _, d := range items {
		if strings.Contains(d, "#") || strings.Contains(d, " ") {
			t.Fatalf("comment leaked: %q", d)
		}
	}
}

func TestMatchCoversSubdomainsOnly(t *testing.T) {
	l := New(filepath.Join(t.TempDir(), "bl.txt"))
	for host, want := range map[string]bool{
		"riotgames.com":      true,
		"auth.riotgames.com": true,
		"RIOTGAMES.com":      true,
		"notriotgames.com":   false,
		"example.com":        false,
	} {
		if got := l.Match(host); got != want {
			t.Errorf("Match(%q)=%v want %v", host, got, want)
		}
	}
}

func TestAddPersistsAndInlineCommentParses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bl.txt")
	l := New(path)
	if err := l.Add("game.example.org"); err != nil {
		t.Fatal(err)
	}
	if !l.Match("game.example.org") {
		t.Fatal("not matched after Add")
	}
	// a fresh instance must read it back from the file despite the inline comment
	if !New(path).Match("game.example.org") {
		t.Fatal("Add not persisted")
	}
}

func TestReloadPicksUpEdits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bl.txt")
	l := New(path)
	if l.Reload() {
		t.Fatal("Reload reported change on untouched file")
	}
	os.WriteFile(path, []byte("changed.example\n"), 0o644)
	future := time.Now().Add(2 * time.Second)
	os.Chtimes(path, future, future)
	if !l.Reload() || !l.Match("changed.example") || l.Match("riotgames.com") {
		t.Fatalf("edit not picked up: %v", l.Items())
	}
}
