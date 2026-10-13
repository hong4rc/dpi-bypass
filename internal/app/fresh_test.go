package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dpi/internal/policy"
)

// A new install must not be pre-filled with any site, and starts in Learn mode.
func TestAFreshFolderStartsEmptyAndInLearnMode(t *testing.T) {
	dir := t.TempDir()
	a := New(dir, "127.0.0.1:0", "test")
	if a.Policy.Skip.Len() != 0 || a.Policy.Bypass.Len() != 0 {
		t.Fatalf("a new install must start with empty lists, got never-bypass %v, needs-bypass %v", a.Policy.Skip.Items(), a.Policy.Bypass.Items())
	}
	for _, name := range []string{"blacklist.txt", "bypass.txt"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
				t.Fatalf("%s contains an entry on a fresh install: %q", name, line)
			}
		}
	}
	if a.Mode() != policy.Learn {
		t.Fatalf("the default mode is Learn, got %v", a.Mode())
	}
}
