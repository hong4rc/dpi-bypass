package tray

import "testing"

// Toggles the real HKCU Run entry, then puts it back as it was.
func TestAutostartToggle(t *testing.T) {
	was := autostartEnabled()
	t.Cleanup(func() { setAutostart(was) })

	if err := setAutostart(true); err != nil || !autostartEnabled() {
		t.Fatalf("enable failed: err=%v enabled=%v", err, autostartEnabled())
	}
	if err := setAutostart(false); err != nil || autostartEnabled() {
		t.Fatalf("disable failed: err=%v enabled=%v", err, autostartEnabled())
	}
}
