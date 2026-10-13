package notify

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Notifications are only for real failures that have no other way to reach the
// user. Nothing that is normal (the app starting, a site being learned, a switch
// being turned) may pop one up: the tray icon, the menu checkmarks and the
// dashboard are the feedback. This test fails when a new notify.Send appears, so
// adding one has to be a conscious decision (update the list below with a reason).
func TestNotificationsAreOnlyForRealFailures(t *testing.T) {
	allowed := map[string]int{
		"cmd/dpi-bypass/main.go":        1, // another copy is already running: the app exits, there is no other way to say so
		"internal/tray/tray_windows.go": 2, // sharing could not be switched on / start with Windows could not be changed
	}

	root := filepath.Join("..", "..")
	found := map[string]int{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if n := strings.Count(string(data), "notify.Send("); n > 0 {
			rel, _ := filepath.Rel(root, path)
			found[filepath.ToSlash(rel)] = n
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	for file, n := range found {
		if allowed[file] != n {
			t.Errorf("%s calls notify.Send %d time(s), the allowed number is %d: notifications are only for real failures", file, n, allowed[file])
		}
	}
	for file, n := range allowed {
		if found[file] != n {
			t.Errorf("%s should still call notify.Send %d time(s) (found %d): update this list if a failure notification was removed on purpose", file, n, found[file])
		}
	}
}

// The app package (learning, sharing, modes) must never notify at all.
func TestTheAppPackageNeverNotifies(t *testing.T) {
	err := filepath.WalkDir(filepath.Join("..", "app"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		data, _ := os.ReadFile(path)
		if strings.Contains(string(data), "internal/notify") {
			t.Errorf("%s imports the notify package", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
