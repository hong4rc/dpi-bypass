// Package logx holds the two app loggers. They discard output until Init runs,
// so packages (and tests) can log without setup.
package logx

import (
	"io"
	"log"
	"os"
	"path/filepath"
)

var (
	Info  = log.New(io.Discard, "", 0)
	Debug = log.New(io.Discard, "", 0)
)

// lenientWriter swallows write errors so a missing console (GUI build) can't
// stop the other log destinations behind io.MultiWriter.
type lenientWriter struct{ w io.Writer }

func (l lenientWriter) Write(p []byte) (int, error) {
	l.w.Write(p)
	return len(p), nil
}

// Init: Info goes to console + both files; Debug goes to the debug file only.
// Files are recreated on every start.
func Init(dir string) {
	sinks := []io.Writer{lenientWriter{os.Stdout}}
	dbg := io.Writer(io.Discard)
	if f, err := os.Create(filepath.Join(dir, "dpi-info.log")); err == nil {
		sinks = append(sinks, f)
	}
	if f, err := os.Create(filepath.Join(dir, "dpi-debug.log")); err == nil {
		sinks = append(sinks, f)
		dbg = f
	}
	Info = log.New(io.MultiWriter(sinks...), "INFO  ", log.Ltime)
	Debug = log.New(dbg, "DEBUG ", log.Ltime|log.Lmicroseconds)
}
