package autostart

import "testing"

// Toggles the real HKCU Run entry, then puts it back as it was.
func TestToggle(t *testing.T) {
	was := Enabled()
	t.Cleanup(func() { Set(was) })

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
