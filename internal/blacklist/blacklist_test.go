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

const testHeader = "# Domains that skip the DPI bypass (connect directly). One per line.\n# A domain also covers its subdomains.\n"

// newWith makes a list that already contains the given domains (as if the user or the app had added them).
func newWith(t *testing.T, domains ...string) (*List, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bl.txt")
	l := New(path, testHeader)
	for _, d := range domains {
		if _, err := l.Add(d, "test"); err != nil {
			t.Fatal(err)
		}
	}
	return l, path
}

// A new list holds comments only: no domain is ever pre-filled.
func TestNewCreatesAHeaderOnlyFileWithoutAnyDomain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bl.txt")
	l := New(path, testHeader)
	if l.Len() != 0 || len(l.Items()) != 0 {
		t.Fatalf("a new list must be empty, got %v", l.Items())
	}
	data, _ := os.ReadFile(path)
	if string(data) != testHeader {
		t.Fatalf("file should contain exactly the header, got %q", data)
	}
}

func TestExistingFilesAreNeverOverwrittenByNew(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bl.txt")
	os.WriteFile(path, []byte("keep.example # mine\n"), 0o644)
	l := New(path, testHeader)
	if !l.Match("keep.example") {
		t.Fatal("an existing file must be loaded as is")
	}
	if data, _ := os.ReadFile(path); string(data) != "keep.example # mine\n" {
		t.Fatalf("existing file was modified: %q", data)
	}
}

func TestCommentsAndBlankLinesAreNotEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bl.txt")
	os.WriteFile(path, []byte("# comment\n\na.example # note\n   # indented comment\nb.example\n"), 0o644)
	items := New(path, testHeader).Items()
	if len(items) != 2 || items[0] != "a.example" || items[1] != "b.example" {
		t.Fatalf("items: %v", items)
	}
	for _, d := range items {
		if strings.Contains(d, "#") || strings.Contains(d, " ") {
			t.Fatalf("comment leaked: %q", d)
		}
	}
}

func TestMatchCoversSubdomainsOnly(t *testing.T) {
	l, _ := newWith(t, "example.com")
	for host, want := range map[string]bool{
		"example.com":      true,
		"auth.example.com": true,
		"EXAMPLE.com":      true,
		"notexample.com":   false,
		"example.org":      false,
	} {
		if got := l.Match(host); got != want {
			t.Errorf("Match(%q)=%v want %v", host, got, want)
		}
	}
}

func TestAddPersistsAndInlineCommentParses(t *testing.T) {
	l, path := newWith(t)
	if _, err := l.Add("game.example.org", "auto-skip test"); err != nil {
		t.Fatal(err)
	}
	if !l.Match("game.example.org") {
		t.Fatal("not matched after Add")
	}
	// a fresh instance must read it back from the file despite the inline comment
	if !New(path, testHeader).Match("game.example.org") {
		t.Fatal("Add not persisted")
	}
}

func TestReloadPicksUpEdits(t *testing.T) {
	l, path := newWith(t, "old.example")
	l.Reload() // the watcher notices the Add above once...
	if l.Reload() {
		t.Fatal("Reload reported change on untouched file")
	}
	os.WriteFile(path, []byte("changed.example\n"), 0o644)
	future := time.Now().Add(2 * time.Second)
	os.Chtimes(path, future, future)
	if !l.Reload() || !l.Match("changed.example") || l.Match("old.example") {
		t.Fatalf("edit not picked up: %v", l.Items())
	}
}

func TestEntriesCarryNotesAndRemoveKeepsTheRest(t *testing.T) {
	l, path := newWith(t, "keep.example")
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
	reloaded := New(path, testHeader)
	if reloaded.Match("a.example.org") || !reloaded.Match("keep.example") {
		t.Fatalf("Remove touched the wrong lines: %v", reloaded.Items())
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "# Domains that skip the DPI bypass") {
		t.Fatal("header comment was lost")
	}
}

func TestAddRejectsBadInput(t *testing.T) {
	l, _ := newWith(t)
	for _, bad := range []string{"", "has space.com", "a/b.com", "x # y"} {
		if _, err := l.Add(bad, "n"); err == nil {
			t.Errorf("Add(%q) should fail", bad)
		}
	}
}

// Many connections to one host fail at the same moment; only one may "win",
// so only one notification is sent and the file gets one line.
func TestConcurrentAddsOfOneHostAddItOnce(t *testing.T) {
	l, path := newWith(t)
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
	l, _ := newWith(t, "example.com")
	if ok, _ := l.Add("api.example.com", "x"); ok {
		t.Fatal("example.com already covers its subdomains")
	}
}

func TestBackupCopiesOnceAndNeverOverwritesAnEarlierBackup(t *testing.T) {
	l, path := newWith(t, "a.example")
	made, err := l.Backup(".bak")
	if err != nil || !made {
		t.Fatalf("first backup: made=%v err=%v", made, err)
	}
	orig, _ := os.ReadFile(path + ".bak")
	if !strings.Contains(string(orig), "a.example") {
		t.Fatalf("backup content: %q", orig)
	}

	l.Add("b.example", "later")
	made, err = l.Backup(".bak")
	if err != nil || made {
		t.Fatalf("second backup must do nothing: made=%v err=%v", made, err)
	}
	if again, _ := os.ReadFile(path + ".bak"); string(again) != string(orig) {
		t.Fatal("an earlier backup was overwritten")
	}

	// nothing to back up is not an error
	missing := New(filepath.Join(t.TempDir(), "none.txt"), "")
	os.Remove(missing.Path())
	if made, err := missing.Backup(".bak"); made || err != nil {
		t.Fatalf("missing file: made=%v err=%v", made, err)
	}
}

// A hand edit that is not a usable domain stays in the file but is inert.
func TestUnusableEntriesStayInTheFileButNeverMatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bl.txt")
	os.WriteFile(path, []byte("two words.example\nnot/a/domain\nok.example\n"), 0o644)
	l := New(path, testHeader)
	if l.Len() != 3 {
		t.Fatalf("entries are kept as written: %v", l.Items())
	}
	for _, h := range []string{"two words.example", "not/a/domain", "words.example", "domain"} {
		if l.Match(h) {
			t.Errorf("unusable entry matched %q", h)
		}
	}
	if !l.Match("ok.example") || !l.Match("sub.ok.example") {
		t.Fatal("usable entries must still match")
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "two words.example") {
		t.Fatal("an unusable record must never be deleted")
	}
}
