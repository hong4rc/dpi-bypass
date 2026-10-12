package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultsWhenFileMissingOrBroken(t *testing.T) {
	dir := t.TempDir()
	if Load(filepath.Join(dir, "nope.json")).Get().LANSharing {
		t.Fatal("missing file must give defaults")
	}
	bad := filepath.Join(dir, "bad.json")
	os.WriteFile(bad, []byte("{not json"), 0o644)
	if Load(bad).Get().LANSharing {
		t.Fatal("broken file must give defaults")
	}
}

func TestUpdatePersistsAndReloads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	st := Load(path)
	if err := st.Update(func(s *Settings) { s.LANSharing = true }); err != nil {
		t.Fatal(err)
	}
	if !Load(path).Get().LANSharing {
		t.Fatal("setting not saved")
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `"lanSharing": true`) {
		t.Fatalf("file should be readable JSON, got %q", data)
	}
	if _, err := os.Stat(path + ".tmp"); err == nil {
		t.Fatal("temp file left behind")
	}
	st.Update(func(s *Settings) { s.LANSharing = false })
	if Load(path).Get().LANSharing {
		t.Fatal("turning it off was not saved")
	}
}
