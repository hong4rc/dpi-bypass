package blacklist

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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
	if _, err := l.Add("game.example.org", "auto-skip test"); err != nil {
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

func TestEntriesCarryNotesAndRemoveKeepsTheRest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bl.txt")
	l := New(path)
	if _, err := l.Add("a.example.org", "added in dashboard"); err != nil {
		t.Fatal(err)
	}
	var note string
	for _, e := range l.Entries() {
		if e.Host == "a.example.org" {
			note = e.Note
		}
	}
	if note != "added in dashboard" {
		t.Fatalf("note=%q entries=%v", note, l.Entries())
	}

	if err := l.Remove("a.example.org"); err != nil {
		t.Fatal(err)
	}
	reloaded := New(path)
	if reloaded.Match("a.example.org") || !reloaded.Match("riotgames.com") {
		t.Fatalf("Remove touched the wrong lines: %v", reloaded.Items())
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "# Domains that skip the DPI bypass") {
		t.Fatal("header comment was lost")
	}
}

func TestAddRejectsBadInput(t *testing.T) {
	l := New(filepath.Join(t.TempDir(), "bl.txt"))
	for _, bad := range []string{"", "has space.com", "a/b.com", "x # y"} {
		if _, err := l.Add(bad, "n"); err == nil {
			t.Errorf("Add(%q) should fail", bad)
		}
	}
}

// Many connections to one host fail at the same moment; only one may "win",
// so only one notification is sent and the file gets one line.
func TestConcurrentAddsOfOneHostAddItOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bl.txt")
	l := New(path)
	var wg sync.WaitGroup
	var added atomic.Int32
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, err := l.Add("busy.example.org", "auto-skip"); err != nil {
				t.Error(err)
			} else if ok {
				added.Add(1)
			}
		}()
	}
	wg.Wait()
	data, _ := os.ReadFile(path)
	if added.Load() != 1 || strings.Count(string(data), "busy.example.org") != 1 {
		t.Fatalf("added %d times, %d lines in the file", added.Load(), strings.Count(string(data), "busy.example.org"))
	}
}

func TestAddSkipsHostsCoveredByAParentDomain(t *testing.T) {
	l := New(filepath.Join(t.TempDir(), "bl.txt"))
	if ok, _ := l.Add("api.riotgames.com", "x"); ok {
		t.Fatal("riotgames.com already covers its subdomains")
	}
}
