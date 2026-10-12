// Package logx holds the two app loggers. Everything logged is also kept in a
// small in-memory ring so the dashboard can show it live. Before Init the
// loggers only feed that ring, so packages (and tests) can log without setup.
package logx

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	ringSize  = 3000         // lines kept for the dashboard
	ringSlack = ringSize / 4 // trimmed in one go instead of copying the ring for every line
)

// Line is one log line kept for the dashboard.
type Line struct {
	Seq   int64  `json:"seq"`
	Level string `json:"level"` // "info" or "debug"
	Text  string `json:"text"`
}

var (
	mem   = &ring{subs: map[chan Line]struct{}{}}
	Info  = log.New(ringWriter{"info", mem}, "INFO  ", log.Ltime)
	Debug = log.New(ringWriter{"debug", mem}, "DEBUG ", log.Ltime|log.Lmicroseconds)

	filesMu sync.Mutex
	files   []*logFile
)

type ring struct {
	mu    sync.Mutex
	lines []Line
	seq   int64
	subs  map[chan Line]struct{}
}

func (r *ring) add(level, text string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	l := Line{Seq: r.seq, Level: level, Text: text}
	r.lines = append(r.lines, l)
	if len(r.lines) > ringSize+ringSlack {
		r.lines = append([]Line(nil), r.lines[len(r.lines)-ringSize:]...)
	}
	for ch := range r.subs {
		select {
		case ch <- l:
		default: // a slow viewer drops lines instead of blocking the proxy
		}
	}
}

type ringWriter struct {
	level string
	r     *ring
}

func (w ringWriter) Write(p []byte) (int, error) {
	w.r.add(w.level, strings.TrimRight(string(p), "\r\n"))
	return len(p), nil
}

// Size limits of the log files. A file that would grow past its limit becomes
// "<name>.old" (replacing the previous one) and a new file starts, so the logs of
// an app left running for years take at most twice the limit on disk.
const (
	infoLimit  = 5 << 20
	debugLimit = 20 << 20
)

// logFile is a log file that can be emptied while the app keeps writing to it,
// and that rolls over instead of growing without bound.
type logFile struct {
	mu    sync.Mutex
	f     *os.File
	path  string
	limit int64
	size  int64
}

func (l *logFile) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.size > 0 && l.size+int64(len(p)) > l.limit {
		l.roll()
	}
	n, err := l.f.Write(p)
	l.size += int64(n)
	return n, err
}

// roll moves the full file aside and starts a new one. If that fails the old
// file is emptied instead: the limit holds either way.
func (l *logFile) roll() {
	l.f.Close()
	os.Remove(l.path + ".old")
	if os.Rename(l.path, l.path+".old") != nil {
		os.Remove(l.path)
	}
	f, err := os.Create(l.path)
	if err != nil {
		f, _ = os.OpenFile(os.DevNull, os.O_WRONLY, 0) // keep the app running without this log
	}
	l.f, l.size = f, 0
}

func (l *logFile) clear() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f.Truncate(0) == nil {
		l.f.Seek(0, io.SeekStart) // not O_APPEND: without this the next write leaves a hole of zeros
		l.size = 0
	}
}

// Clear empties the log files and the dashboard's in-memory log.
func Clear() {
	mem.mu.Lock()
	mem.lines = nil
	mem.mu.Unlock()
	filesMu.Lock()
	defer filesMu.Unlock()
	for _, f := range files {
		f.clear()
	}
}

// Recent returns up to n of the newest lines, oldest first.
func Recent(n int) []Line {
	mem.mu.Lock()
	defer mem.mu.Unlock()
	if n <= 0 || n > ringSize {
		n = ringSize
	}
	if n > len(mem.lines) {
		n = len(mem.lines)
	}
	return append([]Line(nil), mem.lines[len(mem.lines)-n:]...)
}

// Subscribe streams new lines until cancel is called.
func Subscribe() (<-chan Line, func()) {
	ch := make(chan Line, 256)
	mem.mu.Lock()
	mem.subs[ch] = struct{}{}
	mem.mu.Unlock()
	return ch, func() {
		mem.mu.Lock()
		delete(mem.subs, ch)
		mem.mu.Unlock()
	}
}

// lenientWriter swallows write errors so a missing console (GUI build) can't
// stop the other log destinations behind io.MultiWriter.
type lenientWriter struct{ w io.Writer }

func (l lenientWriter) Write(p []byte) (int, error) {
	l.w.Write(p)
	return len(p), nil
}

// Init: Info goes to console + both files + the ring; Debug goes to the debug
// file + the ring. Files are recreated on every start; dir "" means no files.
func Init(dir string) {
	filesMu.Lock()
	for _, f := range files {
		f.f.Close()
	}
	files = nil
	open := func(name string, limit int64) *logFile {
		path := filepath.Join(dir, name)
		f, err := os.Create(path)
		if err != nil {
			return nil
		}
		lf := &logFile{f: f, path: path, limit: limit}
		files = append(files, lf)
		return lf
	}
	info := []io.Writer{lenientWriter{os.Stdout}, ringWriter{"info", mem}}
	debug := []io.Writer{ringWriter{"debug", mem}}
	if dir != "" {
		if f := open("dpi-info.log", infoLimit); f != nil {
			info = append(info, f)
		}
		if f := open("dpi-debug.log", debugLimit); f != nil {
			info = append(info, f) // the debug file holds everything
			debug = append(debug, f)
		}
	}
	filesMu.Unlock()
	Info = log.New(io.MultiWriter(info...), "INFO  ", log.Ltime)
	Debug = log.New(io.MultiWriter(debug...), "DEBUG ", log.Ltime|log.Lmicroseconds)
}
