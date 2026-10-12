// Package blacklist is a domain list backed by a text file (hosts that skip
// the DPI bypass), safe for concurrent use.
package blacklist

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

const defaultContent = `# Domains that skip the DPI bypass (connect directly). One per line.
# A domain also covers its subdomains. Changes are picked up automatically.
# dpi-bypass also adds hosts here on its own when splitting breaks them.
riotgames.com
riotcdn.net
leagueoflegends.com
pvp.net
`

type List struct {
	path  string
	mu    sync.RWMutex
	items []string
	mtime time.Time
}

// New loads path, creating it with defaults when missing.
func New(path string) *List {
	if _, err := os.Stat(path); err != nil {
		os.WriteFile(path, []byte(defaultContent), 0o644)
	}
	l := &List{path: path}
	l.Reload()
	return l
}

func (l *List) Path() string { return l.path }

// Reload re-reads the file if it changed; reports whether it did.
func (l *List) Reload() bool {
	st, err := os.Stat(l.path)
	if err != nil {
		return false
	}
	l.mu.RLock()
	same := st.ModTime().Equal(l.mtime)
	l.mu.RUnlock()
	if same {
		return false
	}
	f, err := os.Open(l.path)
	if err != nil {
		return false
	}
	defer f.Close()
	var items []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line, _, _ := strings.Cut(sc.Text(), "#")
		if d := strings.ToLower(strings.TrimSpace(line)); d != "" {
			items = append(items, d)
		}
	}
	l.mu.Lock()
	l.items, l.mtime = items, st.ModTime()
	l.mu.Unlock()
	return true
}

// Items returns a copy of the current domains.
func (l *List) Items() []string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return append([]string(nil), l.items...)
}

// Match reports whether host equals a listed domain or is a subdomain of one.
func (l *List) Match(host string) bool {
	host = strings.ToLower(host)
	l.mu.RLock()
	defer l.mu.RUnlock()
	for _, d := range l.items {
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

// Add appends host to the file and the in-memory list.
func (l *List) Add(host string) error {
	host = strings.ToLower(host)
	if l.Match(host) {
		return nil
	}
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(f, "\n%s # auto-skip %s\n", host, time.Now().Format("2006-01-02"))
	f.Close()
	if err != nil {
		return err
	}
	l.mu.Lock()
	l.items = append(l.items, host)
	l.mu.Unlock()
	return nil
}
