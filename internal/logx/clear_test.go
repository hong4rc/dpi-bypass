package logx

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClearEmptiesFilesAndMemoryAndKeepsLogging(t *testing.T) {
	dir := t.TempDir()
	Init(dir)
	t.Cleanup(func() { Init("") })

	Info.Println("before-clear")
	Debug.Println("before-clear-debug")
	Clear()
	Info.Println("after-clear")

	for _, name := range []string{"dpi-info.log", "dpi-debug.log"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "before-clear") {
			t.Errorf("%s still has old lines: %q", name, data)
		}
		if bytes.Contains(data, []byte{0}) {
			t.Errorf("%s has a hole of NUL bytes after clearing: %q", name, data)
		}
	}
	info, _ := os.ReadFile(filepath.Join(dir, "dpi-info.log"))
	if !strings.Contains(string(info), "after-clear") {
		t.Errorf("new lines must still reach the file: %q", info)
	}

	recent := Recent(0)
	if len(recent) != 1 || !strings.Contains(recent[0].Text, "after-clear") {
		t.Fatalf("memory log after clear: %+v", recent)
	}
}
