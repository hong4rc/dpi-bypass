// Package policy decides, for each site, whether a connection uses the DPI
// bypass (splitting the TLS ClientHello), and learns from how connections go.
//
// What is known about a site is one of three explicit states:
//
//	NO_BYPASS     evidence (or the user) says it must connect directly
//	NEEDS_BYPASS  evidence (or the user) says it only works with the bypass
//	UNKNOWN       nothing is known; follow the selected mode
//
// The two known states are persisted in two plain-text lists (see the Header
// constants); UNKNOWN is simply "in neither list", so visiting a site never
// writes anything by itself. The selected mode only decides what happens for
// UNKNOWN sites and never changes a stored decision, so switching modes back and
// forth loses nothing.
package policy

import (
	"sync"
	"time"

	"dpi/internal/blacklist"
)

// Mode is how UNKNOWN sites are handled.
type Mode string

const (
	// Always bypasses by default; a site is only connected directly once it is
	// known (learned or set by the user) to work better that way.
	Always Mode = "always"
	// Learn connects directly first and only retries with the bypass after a
	// failure; a site is only bypassed once that retry has proven necessary.
	Learn Mode = "learn"
)

// ParseMode accepts "always" or "learn".
func ParseMode(s string) (Mode, bool) {
	switch Mode(s) {
	case Always, Learn:
		return Mode(s), true
	}
	return Always, false
}

// Decision is what is known about a site.
type Decision int

const (
	Unknown Decision = iota
	NeedsBypass
	NoBypass
)

func (d Decision) String() string {
	switch d {
	case NeedsBypass:
		return "NEEDS_BYPASS"
	case NoBypass:
		return "NO_BYPASS"
	}
	return "UNKNOWN"
}

// ParseDecision reads the names used by String and by the dashboard API.
func ParseDecision(s string) (Decision, bool) {
	switch s {
	case "NEEDS_BYPASS":
		return NeedsBypass, true
	case "NO_BYPASS":
		return NoBypass, true
	case "UNKNOWN":
		return Unknown, true
	}
	return Unknown, false
}

// Method is one way of sending the ClientHello.
type Method int

const (
	None   Method = iota // no attempt
	Direct               // untouched
	Split                // fragmented (the bypass)
)

// Plan is how to connect to a site: what to try first, what to fall back to if
// that gets no usable reply, and which decision a successful fallback is
// evidence for (Unknown: it proves nothing and nothing is learned).
type Plan struct {
	First, Fallback Method
	Learn           Decision
}

// Header texts of the two lists.
const (
	SkipHeader = `# Sites that never use the DPI bypass (NO_BYPASS): they connect directly. One per line.
# A domain also covers its subdomains. Changes are picked up automatically.
# Added by you, or by the app after it saw the bypass break a site that works without it.
`
	BypassHeader = `# Sites that need the DPI bypass (NEEDS_BYPASS). One per line.
# A domain also covers its subdomains. Changes are picked up automatically.
# Added by you, or by the app after a normal connection failed and the bypass fixed it.
`
)

// Defaults for the evidence rule: one failure followed by a successful retry is
// not enough to write a permanent decision (a network hiccup looks the same),
// two separate ones in a short time are.
const (
	DefaultThreshold = 2
	DefaultWindow    = 10 * time.Minute
)

// Policy answers "how should I connect to this site?" and records what was learned.
type Policy struct {
	Skip   *blacklist.List // NO_BYPASS
	Bypass *blacklist.List // NEEDS_BYPASS

	Threshold int           // observations needed before a decision is persisted
	Window    time.Duration // observations older than this are forgotten

	mode func() Mode
	now  func() time.Time

	mu      sync.Mutex
	pending map[string]*evidence
}

type evidence struct {
	count int
	since time.Time
}

// New builds a policy over the two lists; mode is read on every decision, so
// changing it takes effect immediately.
func New(skip, bypass *blacklist.List, mode func() Mode) *Policy {
	return &Policy{
		Skip: skip, Bypass: bypass, mode: mode, now: time.Now,
		Threshold: DefaultThreshold, Window: DefaultWindow,
		pending: map[string]*evidence{},
	}
}

func (p *Policy) Mode() Mode {
	if m, ok := ParseMode(string(p.mode())); ok {
		return m
	}
	return Always
}

// Decide returns what is known about host. NO_BYPASS wins over NEEDS_BYPASS.
func (p *Policy) Decide(host string) Decision {
	switch {
	case p.Skip.Match(host):
		return NoBypass
	case p.Bypass.Match(host):
		return NeedsBypass
	}
	return Unknown
}

// PlanFor returns how to connect to host in the current mode.
func (p *Policy) PlanFor(host string) Plan {
	switch p.Decide(host) {
	case NoBypass:
		return Plan{First: Direct, Fallback: None, Learn: Unknown}
	case NeedsBypass:
		// a failing bypass falls back to a direct connection for this one connection,
		// but a single contradicting observation never rewrites a stored decision
		return Plan{First: Split, Fallback: Direct, Learn: Unknown}
	}
	if p.Mode() == Learn {
		return Plan{First: Direct, Fallback: Split, Learn: NeedsBypass}
	}
	return Plan{First: Split, Fallback: Direct, Learn: NoBypass}
}

func key(host string, d Decision) string { return host + "|" + d.String() }

// Observe records one piece of evidence that host is d (NEEDS_BYPASS or
// NO_BYPASS): a connection failed with one method and worked with the other.
// Only when the same evidence has been seen Threshold times within Window is it
// persisted. It reports whether this call wrote the decision.
func (p *Policy) Observe(host string, d Decision) (persisted bool, err error) {
	if d == Unknown {
		return false, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.Decide(host) != Unknown {
		return false, nil // already decided (learned meanwhile, or set by the user)
	}
	k, now := key(host, d), p.now()
	ev := p.pending[k]
	if ev == nil || now.Sub(ev.since) > p.Window {
		ev = &evidence{since: now}
		p.pending[k] = ev
	}
	ev.count++
	if ev.count < p.Threshold {
		return false, nil
	}
	delete(p.pending, key(host, NeedsBypass))
	delete(p.pending, key(host, NoBypass))
	date := now.Format("2006-01-02")
	if d == NeedsBypass {
		return p.Bypass.Add(host, "learned "+date)
	}
	return p.Skip.Add(host, "auto-skip "+date)
}

// Succeeded tells the policy that the first attempt of plan worked, which
// contradicts the pending evidence for plan.Learn: the earlier failure was transient.
func (p *Policy) Succeeded(host string, plan Plan) {
	if plan.Learn == Unknown {
		return
	}
	p.mu.Lock()
	delete(p.pending, key(host, plan.Learn))
	p.mu.Unlock()
}

// SetState is an explicit choice by the user for exactly this host: it moves the
// host to the list of d (Unknown removes it from both). A host never sits in
// both lists, so the lists can not contradict each other.
func (p *Policy) SetState(host string, d Decision, note string) error {
	if err := p.Skip.Remove(host); err != nil {
		return err
	}
	if err := p.Bypass.Remove(host); err != nil {
		return err
	}
	var err error
	switch d {
	case NoBypass:
		_, err = p.Skip.Add(host, note)
	case NeedsBypass:
		_, err = p.Bypass.Add(host, note)
	}
	return err
}

// Entries lists the persisted entries of one state.
func (p *Policy) Entries(d Decision) []blacklist.Entry {
	switch d {
	case NoBypass:
		return p.Skip.Entries()
	case NeedsBypass:
		return p.Bypass.Entries()
	}
	return nil
}

// Reload re-reads both lists if their files changed; reports whether either did.
func (p *Policy) Reload() bool {
	a, b := p.Skip.Reload(), p.Bypass.Reload()
	return a || b
}
