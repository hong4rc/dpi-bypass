package autostart

import (
	"testing"

	"golang.org/x/sys/windows/registry"
)

// scratchKey points the package at a throwaway registry key for the test, so the
// real "Run" key of the user (and therefore their real startup entry) is never read or written.
func scratchKey(t *testing.T) {
	t.Helper()
	real := runPath
	runPath = `Software\dpi-bypass-test\` + t.Name()
	t.Cleanup(func() {
		registry.DeleteKey(registry.CURRENT_USER, runPath)
		registry.DeleteKey(registry.CURRENT_USER, `Software\dpi-bypass-test`) // only succeeds when empty
		runPath = real
	})
}

func TestToggle(t *testing.T) {
	scratchKey(t)
	if Enabled() {
		t.Fatal("a fresh scratch key must start disabled")
	}
	if err := Set(true); err != nil || !Enabled() {
		t.Fatalf("enable failed: err=%v enabled=%v", err, Enabled())
	}
	if err := Set(false); err != nil || Enabled() {
		t.Fatalf("disable failed: err=%v enabled=%v", err, Enabled())
	}
	if err := Set(false); err != nil {
		t.Fatalf("disabling twice must not fail: %v", err)
	}
}

// The test must not read or change the real startup entry, whatever it holds.
func TestTestsNeverTouchTheRealStartupEntry(t *testing.T) {
	real := `Software\Microsoft\Windows\CurrentVersion\Run`
	read := func() (string, bool) {
		k, err := registry.OpenKey(registry.CURRENT_USER, real, registry.QUERY_VALUE)
		if err != nil {
			return "", false
		}
		defer k.Close()
		v, _, err := k.GetStringValue(runValue)
		return v, err == nil
	}
	before, had := read()

	scratchKey(t)
	if runPath == real {
		t.Fatal("the test did not switch to the scratch key")
	}
	Set(true)
	Set(false)
	Set(true)

	after, has := read()
	if before != after || had != has {
		t.Fatalf("the real startup entry changed: %q (%v) -> %q (%v)", before, had, after, has)
	}
}
