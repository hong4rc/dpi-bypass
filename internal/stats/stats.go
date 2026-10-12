// Package stats remembers recent proxy connections and which devices used the
// proxy, for the dashboard. Everything is in memory except device names and
// identities (devices.json).
package stats

import (
	"context"
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

// Event is one proxy connection. It is recorded when the tunnel opens (Open is
// true and the byte counts are still 0) and completed when it closes.
type Event struct {
	ID     uint64    `json:"id"`
	Time   time.Time `json:"time"`
	Client string    `json:"client"`
	Host   string    `json:"host"` // host:port
	Mode   string    `json:"mode"` // bypass | direct | auto-skip | failed
	Open   bool      `json:"open"` // still connected
	Up     int64     `json:"up"`
	Down   int64     `json:"down"`
	Millis int64     `json:"ms"`
	Err    string    `json:"err,omitempty"`
}

// Device is a client that fetched the PAC or used the proxy.
type Device struct {
	IP         string     `json:"ip"`
	Name       string     `json:"name"`       // chosen by the user
	Hostname   string     `json:"hostname"`   // from reverse DNS, often empty
	MAC        string     `json:"mac"`        // only known for devices on the PC's own subnet
	Agents     []string   `json:"agents"`     // what the device says it is (from User-Agent), e.g. "Android 14 · SM-S918B"
	RawAgents  []string   `json:"rawAgents"`  // the User-Agent headers as received, for troubleshooting
	Identities []Identity `json:"identities"` // what the device reported on the identify page (real model)
	Self       bool       `json:"self"`
	Active     bool       `json:"active"` // seen in the last 2 minutes
	First      time.Time  `json:"first"`
	Last       time.Time  `json:"last"`
	Conns      int        `json:"conns"`
	PACFetches int        `json:"pacFetches"`
	Up         int64      `json:"up"`
	Down       int64      `json:"down"`
}

// Totals counts every connection since the app started (the event list is capped).
type Totals struct {
	Conns    int   `json:"conns"`
	Open     int   `json:"open"` // connected right now
	Bypass   int   `json:"bypass"`
	Direct   int   `json:"direct"`
	AutoSkip int   `json:"autoSkip"`
	Failed   int   `json:"failed"`
	Up       int64 `json:"up"`
	Down     int64 `json:"down"`
}

type Store struct {
	mu      sync.Mutex
	totals  Totals
	nextID  uint64
	open    map[uint64]string // id -> client, for connections still open
	events  []Event
	devices map[string]*Device
	saved   map[string]*savedDevice // by IP: name and identities, kept in devices.json
	path    string
}

// New loads saved device names and identities from path (devices.json); "" keeps them in memory only.
func New(path string) *Store {
	s := &Store{devices: map[string]*Device{}, saved: map[string]*savedDevice{}, open: map[uint64]string{}, path: path}
	s.load(path)
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

// SeenAgent remembers what a device calls itself in its User-Agent header. It
// returns the short label (may be "") and whether this exact header was new for
// the device, so the caller can log each distinct header once.
func (s *Store) SeenAgent(client, userAgent string) (label string, isNew bool) {
	if userAgent == "" {
		return "", false
	}
	label = Label(userAgent)
	raw := userAgent
	if len(raw) > 300 {
		raw = raw[:300]
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.touch(client)
	if label != "" && !contains(d.Agents, label) && len(d.Agents) < maxAgents {
		d.Agents = append(d.Agents, label)
	}
	if contains(d.RawAgents, raw) || len(d.RawAgents) >= maxAgents {
		return label, false
	}
	d.RawAgents = append(d.RawAgents, raw)
	return label, true
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func (s *Store) SeenPAC(client string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.touch(client).PACFetches++
}

// Open records a connection as soon as its tunnel is established, so
// long-lived connections (streaming, chat, push) count immediately. It returns
// the id to pass to Close.
func (s *Store) Open(e Event) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	e.ID, e.Open = s.nextID, true
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	s.touch(e.Client).Conns++
	s.totals.Conns++
	s.totals.Open++
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
	s.open[e.ID] = e.Client
	s.events = append(s.events, e)
	if len(s.events) > maxEvents {
		s.events = append([]Event(nil), s.events[len(s.events)-maxEvents:]...)
	}
	return e.ID
}

// Close completes a connection with its final size, duration and error.
func (s *Store) Close(id uint64, up, down, millis int64, errText string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	client, ok := s.open[id]
	if !ok {
		return
	}
	delete(s.open, id)
	s.totals.Open--
	s.totals.Up += up
	s.totals.Down += down
	if d := s.devices[client]; d != nil {
		d.Up += up
		d.Down += down
		d.Last = time.Now()
	}
	for i := len(s.events) - 1; i >= 0; i-- { // recent first; the event may have been trimmed away
		if s.events[i].ID == id {
			e := &s.events[i]
			e.Open, e.Up, e.Down, e.Millis, e.Err = false, up, down, millis, errText
			break
		}
	}
}

// AddConn records a connection that is already finished (open + close in one step).
func (s *Store) AddConn(e Event) {
	s.Close(s.Open(e), e.Up, e.Down, e.Millis, e.Err)
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
		c.RawAgents = append([]string{}, d.RawAgents...)
		if sd := s.saved[c.IP]; sd != nil {
			c.Name = sd.Name
			c.Identities = append([]Identity{}, sd.Identities...)
		} else {
			c.Identities = []Identity{}
		}
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

// SetName names a device ("" clears it) and saves it.
func (s *Store) SetName(ip, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sd := s.savedFor(ip)
	sd.Name = strings.TrimSpace(name)
	if sd.Name == "" && len(sd.Identities) == 0 {
		delete(s.saved, ip)
	}
	return s.persist()
}
