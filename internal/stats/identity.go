package stats

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const maxIdentities = 5 // phones behind one router (NAT) share an address

// Identity is what a device told us about itself on the identify page: the
// browser's Client Hints (real model, OS version, ...), which never appear in
// the headers the proxy can see.
type Identity struct {
	Model           string    `json:"model"`
	Platform        string    `json:"platform"`
	PlatformVersion string    `json:"platformVersion"`
	Architecture    string    `json:"architecture,omitempty"`
	Bitness         string    `json:"bitness,omitempty"`
	Mobile          bool      `json:"mobile"`
	Browser         string    `json:"browser,omitempty"` // e.g. "Google Chrome 154"
	UserAgent       string    `json:"userAgent,omitempty"`
	Screen          string    `json:"screen,omitempty"`
	Language        string    `json:"language,omitempty"`
	At              time.Time `json:"at"`
	Label           string    `json:"label"` // short description, filled in by SetIdentity
}

// savedDevice is everything about a device that survives a restart.
type savedDevice struct {
	Name       string     `json:"name,omitempty"`
	Identities []Identity `json:"identities,omitempty"`
}

// load reads path (devices.json). If it doesn't exist yet but the old
// device-names.json does, the names are moved over.
func (s *Store) load(path string) {
	if path == "" {
		return
	}
	if b, err := os.ReadFile(path); err == nil {
		json.Unmarshal(b, &s.saved)
		for ip, d := range s.saved {
			if d == nil {
				delete(s.saved, ip)
			}
		}
		return
	}
	legacy := filepath.Join(filepath.Dir(path), "device-names.json")
	b, err := os.ReadFile(legacy)
	if err != nil {
		return
	}
	names := map[string]string{}
	if json.Unmarshal(b, &names) != nil {
		return
	}
	for ip, name := range names {
		s.saved[ip] = &savedDevice{Name: name}
	}
	if s.persist() == nil {
		os.Remove(legacy)
	}
}

// persist writes devices.json atomically (write, then rename). Caller holds s.mu or is single-threaded.
func (s *Store) persist() error {
	if s.path == "" {
		return nil
	}
	b, err := json.MarshalIndent(s.saved, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *Store) savedFor(ip string) *savedDevice {
	d := s.saved[ip]
	if d == nil {
		d = &savedDevice{}
		s.saved[ip] = d
	}
	return d
}

// SetIdentity stores what a device reported about itself (keyed by its address)
// and remembers it across restarts. The same model reporting again just
// refreshes its entry.
func (s *Store) SetIdentity(client string, id Identity) error {
	id.At = time.Now()
	id.Label = id.Describe()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.touch(client)
	d := s.savedFor(client)
	replaced := false
	for i, old := range d.Identities {
		if old.Model == id.Model && old.Platform == id.Platform {
			d.Identities[i], replaced = id, true
			break
		}
	}
	if !replaced {
		d.Identities = append(d.Identities, id)
		if len(d.Identities) > maxIdentities {
			d.Identities = d.Identities[len(d.Identities)-maxIdentities:]
		}
	}
	return s.persist()
}

// Describe turns an identity into "SHARK PAR-A0 · Android 12" (or the best the
// browser gave: iPhone and Safari report no model).
func (id Identity) Describe() string {
	platform := strings.TrimSpace(id.Platform)
	if v := trimVersion(id.PlatformVersion); v != "" && (platform == "Android" || platform == "iOS") {
		platform += " " + v
	}
	switch model := strings.TrimSpace(id.Model); {
	case model != "" && platform != "":
		return model + " · " + platform
	case model != "":
		return model
	}
	if l := Label(id.UserAgent); l != "" {
		return l
	}
	return platform
}

// trimVersion drops trailing ".0" parts: "12.0.0" -> "12", "17.2.0" -> "17.2".
func trimVersion(v string) string {
	v = strings.TrimSpace(v)
	for strings.HasSuffix(v, ".0") {
		v = strings.TrimSuffix(v, ".0")
	}
	return v
}
