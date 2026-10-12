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

// Entry is one listed domain and the comment written after it, if any.
type Entry struct {
	Host string `json:"host"`
	Note string `json:"note"`
}

type List struct {
	path    string
	mu      sync.RWMutex
	entries []Entry
	mtime   time.Time
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
	var entries []Entry
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if e, ok := parseLine(sc.Text()); ok {
			entries = append(entries, e)
		}
	}
	l.mu.Lock()
	l.entries, l.mtime = entries, st.ModTime()
	l.mu.Unlock()
	return true
}

// parseLine reads "domain # note"; blank and comment-only lines give ok=false.
func parseLine(line string) (Entry, bool) {
	host, note, _ := strings.Cut(line, "#")
	host = strings.ToLower(strings.TrimSpace(host))
	return Entry{Host: host, Note: strings.TrimSpace(note)}, host != ""
}

// Entries returns a copy of the current entries.
func (l *List) Entries() []Entry {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return append([]Entry(nil), l.entries...)
}

// Items returns just the domains.
func (l *List) Items() []string {
	var hosts []string
	for _, e := range l.Entries() {
		hosts = append(hosts, e.Host)
	}
	return hosts
}

// Match reports whether host equals a listed domain or is a subdomain of one.
func (l *List) Match(host string) bool {
	host = strings.ToLower(host)
	l.mu.RLock()
	defer l.mu.RUnlock()
	for _, e := range l.entries {
		if host == e.Host || strings.HasSuffix(host, "."+e.Host) {
			return true
		}
	}
	return false
}

// Add appends host (with a note) to the file and the in-memory list. It reports
// whether it added anything: false means the host (or its parent domain) was
// already listed, so concurrent callers for one host get exactly one true.
func (l *List) Add(host, note string) (bool, error) {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" || strings.ContainsAny(host, " #\t/") {
		return false, fmt.Errorf("invalid domain %q", host)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, e := range l.entries {
		if host == e.Host || strings.HasSuffix(host, "."+e.Host) {
			return false, nil
		}
	}
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return false, err
	}
	_, err = fmt.Fprintf(f, "\n%s # %s\n", host, note)
	f.Close()
	if err != nil {
		return false, err
	}
	l.entries = append(l.entries, Entry{Host: host, Note: note})
	return true, nil
}

// Remove deletes every line for host from the file, keeping comments and other
// domains exactly as they were.
func (l *List) Remove(host string) error {
	host = strings.ToLower(strings.TrimSpace(host))
	data, err := os.ReadFile(l.path)
	if err != nil {
		return err
	}
	var kept []string
	for _, line := range strings.Split(string(data), "\n") {
		if e, ok := parseLine(strings.TrimRight(line, "\r")); ok && e.Host == host {
			continue
		}
		kept = append(kept, line)
	}
	if err := os.WriteFile(l.path, []byte(strings.Join(kept, "\n")), 0o644); err != nil {
		return err
	}
	l.mu.Lock()
	var entries []Entry
	for _, e := range l.entries {
		if e.Host != host {
			entries = append(entries, e)
		}
	}
	l.entries = entries
	l.mu.Unlock()
	return nil
}
