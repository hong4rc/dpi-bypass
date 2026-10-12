// Package settings keeps the app's saved choices in settings.json next to the
// exe: plain JSON, easy to read and edit, written atomically.
package settings

import (
	"encoding/json"
	"os"
	"sync"
)

// Settings are the choices that survive a restart.
type Settings struct {
	LANSharing bool `json:"lanSharing"` // share the bypass with other devices on the LAN
}

type Store struct {
	path string
	mu   sync.Mutex
	s    Settings
}

// Load reads path; a missing or unreadable file just means the defaults.
func Load(path string) *Store {
	st := &Store{path: path}
	if b, err := os.ReadFile(path); err == nil {
		json.Unmarshal(b, &st.s)
	}
	return st
}

func (st *Store) Get() Settings {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.s
}

// Update applies fn and saves. The change is kept in memory even if saving fails.
func (st *Store) Update(fn func(*Settings)) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	fn(&st.s)
	b, err := json.MarshalIndent(st.s, "", "  ")
	if err != nil {
		return err
	}
	tmp := st.path + ".tmp" // write then rename, so a crash can't leave half a file
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, st.path)
}
