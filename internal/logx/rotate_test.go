package logx

import (
	"os"
	"path/filepath"
	"testing"
)

// An app left running for years must not fill the disk: a full log file moves to
// "<name>.old" and a new one starts, so at most two files of the limit exist.
func TestALogFileRollsOverInsteadOfGrowing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.log")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	lf := &logFile{f: f, path: path, limit: 100}
	t.Cleanup(func() { lf.mu.Lock(); lf.f.Close(); lf.mu.Unlock() })

	for i := 0; i < 50; i++ {
		if _, err := lf.Write([]byte("0123456789\n")); err != nil {
			t.Fatal(err)
		}
	}
	cur, err := os.Stat(path)
	if err != nil || cur.Size() > 100 || cur.Size() == 0 {
		t.Fatalf("current file: %v %v", cur, err)
	}
	old, err := os.Stat(path + ".old")
	if err != nil || old.Size() > 100 || old.Size() == 0 {
		t.Fatalf("rolled file: %v %v", old, err)
	}
}

func TestClearingAFileRestartsItsSizeCount(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.log")
	f, _ := os.Create(path)
	lf := &logFile{f: f, path: path, limit: 100}
	t.Cleanup(func() { lf.mu.Lock(); lf.f.Close(); lf.mu.Unlock() })
	lf.Write([]byte("0123456789012345678901234567890123456789012345678901234567890123456789\n"))
	lf.clear()
	lf.Write([]byte("0123456789012345678901234567890123456789012345678901234567890123456789\n"))
	if _, err := os.Stat(path + ".old"); err == nil {
		t.Fatal("a cleared file must not roll over just because of what was cleared")
	}
}
