// Package stats remembers recent proxy connections and which devices used the
// proxy, for the dashboard. Everything is in memory except device names.
package stats

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	maxEvents    = 500
	activeWindow = 2 * time.Minute
)

// Event is one finished (or failed) proxy connection.
type Event struct {
	Time   time.Time `json:"time"`
	Client string    `json:"client"`
	Host   string    `json:"host"` // host:port
	Mode   string    `json:"mode"` // bypass | direct | auto-skip | failed
	Up     int64     `json:"up"`
	Down   int64     `json:"down"`
	Millis int64     `json:"ms"`
	Err    string    `json:"err,omitempty"`
}

// Device is a client that fetched the PAC or used the proxy.
type Device struct {
	IP         string    `json:"ip"`
	Name       string    `json:"name"`     // chosen by the user
	Hostname   string    `json:"hostname"` // from reverse DNS, often empty
	MAC        string    `json:"mac"`      // only known for devices on the PC's own subnet
	Agents     []string  `json:"agents"`   // what the device says it is (from User-Agent), e.g. "Android 14 · SM-S918B"
	Self       bool      `json:"self"`
	Active     bool      `json:"active"` // seen in the last 2 minutes
	First      time.Time `json:"first"`
	Last       time.Time `json:"last"`
	Conns      int       `json:"conns"`
	PACFetches int       `json:"pacFetches"`
	Up         int64     `json:"up"`
	Down       int64     `json:"down"`
}

// Totals counts every connection since the app started (the event list is capped).
type Totals struct {
	Conns    int   `json:"conns"`
	Bypass   int   `json:"bypass"`
	Direct   int   `json:"direct"`
	AutoSkip int   `json:"autoSkip"`
	Failed   int   `json:"failed"`
	Up       int64 `json:"up"`
	Down     int64 `json:"down"`
}

type Store struct {
	mu        sync.Mutex
	totals    Totals
	events    []Event
	devices   map[string]*Device
	names     map[string]string
	namesPath string
}

// New loads saved device names from namesPath ("" keeps names in memory only).
func New(namesPath string) *Store {
	s := &Store{devices: map[string]*Device{}, names: map[string]string{}, namesPath: namesPath}
	if namesPath != "" {
		if b, err := os.ReadFile(namesPath); err == nil {
			json.Unmarshal(b, &s.names)
		}
	}
	return s
}

// ClientIP turns a RemoteAddr into the key used for devices; loopback is one device.
func ClientIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return "127.0.0.1"
	}
	return host
}

// touch returns the device for ip, creating it (and looking up its names in the background).
func (s *Store) touch(ip string) *Device {
	d := s.devices[ip]
	if d == nil {
		now := time.Now()
		d = &Device{IP: ip, Self: ip == "127.0.0.1", First: now}
		s.devices[ip] = d
		go s.enrich(ip)
	}
	d.Last = time.Now()
	return d
}

// maxAgents bounds how many different descriptions one address keeps; several
// phones behind one router (NAT) share an address.
const maxAgents = 5

// SeenAgent remembers what a device calls itself in its User-Agent header.
func (s *Store) SeenAgent(client, userAgent string) {
	label := Label(userAgent)
	if label == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.touch(client)
	for _, a := range d.Agents {
		if a == label {
			return
		}
	}
	if len(d.Agents) < maxAgents {
		d.Agents = append(d.Agents, label)
	}
}

func (s *Store) SeenPAC(client string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.touch(client).PACFetches++
}

func (s *Store) AddConn(e Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.touch(e.Client)
	d.Conns++
	d.Up += e.Up
	d.Down += e.Down
	s.totals.Conns++
	s.totals.Up += e.Up
	s.totals.Down += e.Down
	switch e.Mode {
	case "bypass":
		s.totals.Bypass++
	case "direct":
		s.totals.Direct++
	case "auto-skip":
		s.totals.AutoSkip++
	case "failed":
		s.totals.Failed++
	}
	s.events = append(s.events, e)
	if len(s.events) > maxEvents {
		s.events = append([]Event(nil), s.events[len(s.events)-maxEvents:]...)
	}
}

// enrich fills in the reverse-DNS name and MAC without blocking the proxy.
func (s *Store) enrich(ip string) {
	var hostname, mac string
	if ip == "127.0.0.1" {
		hostname, _ = os.Hostname()
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
		defer cancel()
		if names, err := net.DefaultResolver.LookupAddr(ctx, ip); err == nil && len(names) > 0 {
			hostname = strings.TrimSuffix(names[0], ".")
		}
		mac = macFor(ip)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if d := s.devices[ip]; d != nil {
		d.Hostname, d.MAC = hostname, mac
	}
}

// Devices returns a snapshot: this PC first, then the most recently seen.
func (s *Store) Devices() []Device {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Device, 0, len(s.devices))
	for _, d := range s.devices {
		c := *d
		c.Agents = append([]string{}, d.Agents...) // never nil in JSON, and not shared with the live record
		c.Name = s.names[c.IP]
		c.Active = time.Since(c.Last) < activeWindow
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Self != out[j].Self {
			return out[i].Self
		}
		return out[i].Last.After(out[j].Last)
	})
	return out
}

// Recent returns up to n events, newest first.
func (s *Store) Recent(n int) []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n <= 0 || n > len(s.events) {
		n = len(s.events)
	}
	out := make([]Event, 0, n)
	for i := len(s.events) - 1; i >= len(s.events)-n; i-- {
		out = append(out, s.events[i])
	}
	return out
}

// Totals returns the counters since start.
func (s *Store) Totals() Totals {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.totals
}

// SetName names a device ("" clears it) and saves all names.
func (s *Store) SetName(ip, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	name = strings.TrimSpace(name)
	if name == "" {
		delete(s.names, ip)
	} else {
		s.names[ip] = name
	}
	if s.namesPath == "" {
		return nil
	}
	b, err := json.MarshalIndent(s.names, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.namesPath, b, 0o644)
}
