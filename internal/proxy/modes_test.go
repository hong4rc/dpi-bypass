package proxy

import (
	"io"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"dpi/internal/blacklist"
	"dpi/internal/logx"
	"dpi/internal/policy"
)

// A fake upstream that behaves like a network with DPI: depending on whether the
// ClientHello arrived whole or fragmented, it answers, resets, hangs or sends a TLS alert.
type behavior string

const (
	bOK    behavior = "ok"    // answers
	bClose behavior = "close" // closes without a reply, like a DPI reset
	bAlert behavior = "alert" // TLS alert
	bHang  behavior = "hang"  // silence, like a DPI that drops packets
)

type upstream struct {
	ln                    net.Listener
	directSeen, splitSeen atomic.Int32
	rule                  func(frag bool, n int) behavior // n: how many connections of this kind came before
}

func startUpstream(t *testing.T, rule func(frag bool, n int) behavior) *upstream {
	t.Helper()
	u := &upstream{rule: rule}
	u.ln, _ = net.Listen("tcp", "127.0.0.1:0")
	done := make(chan struct{})
	t.Cleanup(func() { close(done); u.ln.Close() })
	go func() {
		for {
			c, err := u.ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				// like a real TLS server: read records until the whole handshake message has arrived
				h := make([]byte, 5)
				if _, err := io.ReadFull(c, h); err != nil {
					return
				}
				body := make([]byte, int(h[3])<<8|int(h[4]))
				io.ReadFull(c, body)
				frag := fragmented(append(h, body...))
				if frag {
					need := 4 + (int(body[1])<<16 | int(body[2])<<8 | int(body[3]))
					for got := len(body); got < need; {
						nh := make([]byte, 5)
						if _, err := io.ReadFull(c, nh); err != nil {
							return
						}
						nb := make([]byte, int(nh[3])<<8|int(nh[4]))
						if _, err := io.ReadFull(c, nb); err != nil {
							return
						}
						got += len(nb)
					}
				}
				ctr := &u.directSeen
				if frag {
					ctr = &u.splitSeen
				}
				switch u.rule(frag, int(ctr.Add(1))-1) {
				case bOK:
					c.Write([]byte("OK"))
				case bAlert:
					c.Write([]byte{0x15, 0x03, 0x03, 0x00, 0x02, 0x02, 0x28})
				case bHang:
					select {
					case <-time.After(3 * time.Second):
					case <-done:
					}
				}
			}()
		}
	}()
	return u
}

func (u *upstream) port() string { _, p, _ := net.SplitHostPort(u.ln.Addr().String()); return p }

// shortTimeouts makes a silent upstream cost 150 ms instead of 6 s.
func shortTimeouts(t *testing.T) {
	t.Helper()
	old := firstReplyTimeout
	firstReplyTimeout = 150 * time.Millisecond
	t.Cleanup(func() { firstReplyTimeout = old })
}

// rig serves the proxy in a chosen mode over real list files.
type rig struct {
	t       *testing.T
	dir     string
	mode    policy.Mode
	pol     *policy.Policy
	p       *Proxy
	addr    string
	opens   <-chan openEvent
	learned chan policy.Decision
}

func newRig(t *testing.T, mode policy.Mode, up ...*upstream) *rig {
	t.Helper()
	r := &rig{t: t, dir: t.TempDir(), mode: mode, learned: make(chan policy.Decision, 16)}
	r.start(up...)
	return r
}

// start (re)creates the policy and proxy over the same files, like an app restart.
func (r *rig) start(up ...*upstream) {
	skip := blacklist.New(filepath.Join(r.dir, "blacklist.txt"), policy.SkipHeader)
	bypass := blacklist.New(filepath.Join(r.dir, "bypass.txt"), policy.BypassHeader)
	r.pol = policy.New(skip, bypass, func() policy.Mode { return r.mode })
	r.p = New("127.0.0.1:0", r.pol)
	r.p.SplitPrivate = true // the fake upstreams live on loopback
	for _, u := range up {
		r.p.TLSPorts[u.port()] = true
	}
	r.p.OnLearn = func(_ string, d policy.Decision) { r.learned <- d }
	r.opens, _ = observe(r.p)
	srv := httptest.NewServer(r.p)
	r.t.Cleanup(srv.Close)
	r.addr = strings.TrimPrefix(srv.URL, "http://")
}

func (r *rig) file(name string) string {
	data, _ := os.ReadFile(filepath.Join(r.dir, name))
	return string(data)
}

// visit connects through the proxy to host:port and returns the server's answer
// ("" when the connection failed) and how the proxy reported handling it.
func (r *rig) visit(host string, u *upstream) (answer, mode string) {
	r.t.Helper()
	c, err := net.Dial("tcp", r.addr)
	if err != nil {
		r.t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(10 * time.Second))
	target := net.JoinHostPort(host, u.port())
	io.WriteString(c, "CONNECT "+target+" HTTP/1.1\r\nHost: "+target+"\r\n\r\n")
	status := make([]byte, 0, 64)
	one := make([]byte, 1)
	for !strings.HasSuffix(string(status), "\r\n\r\n") {
		if _, err := c.Read(one); err != nil {
			return "", "no-tunnel"
		}
		status = append(status, one[0])
	}
	if !strings.Contains(string(status), " 200 ") {
		return "", "refused"
	}
	c.Write(fakeHello(200))
	buf := make([]byte, 2)
	n, _ := io.ReadFull(c, buf)
	answer = string(buf[:n])
	select {
	case o := <-r.opens:
		mode = o.mode
	case <-time.After(2 * time.Second):
		mode = "no-event"
	}
	if answer == "" { // show what the proxy logged: the reason is there
		for _, l := range logx.Recent(6) {
			r.t.Logf("proxy log: %s", l.Text)
		}
	}
	return answer, mode
}

func (r *rig) wantLearned(d policy.Decision) {
	r.t.Helper()
	select {
	case got := <-r.learned:
		if got != d {
			r.t.Fatalf("learned %v, want %v", got, d)
		}
	case <-time.After(2 * time.Second):
		r.t.Fatalf("nothing was learned, want %v", d)
	}
}

func (r *rig) wantNothingLearned() {
	r.t.Helper()
	select {
	case d := <-r.learned:
		r.t.Fatalf("learned %v, want nothing", d)
	case <-time.After(100 * time.Millisecond):
	}
}

const host = "127.0.0.1"

// Rules.
var (
	allOK          = func(bool, int) behavior { return bOK }
	allClose       = func(bool, int) behavior { return bClose }
	dpiResetsWhole = func(frag bool, _ int) behavior { // whole hello: reset; fragmented: fine (the Steam case)
		if frag {
			return bOK
		}
		return bClose
	}
	dpiHangsWhole = func(frag bool, _ int) behavior {
		if frag {
			return bOK
		}
		return bHang
	}
	brokenByFragments = func(frag bool, _ int) behavior { // the opposite: fragments break the server
		if frag {
			return bClose
		}
		return bOK
	}
)

// ===================== Learn =====================

// Visiting a site that works must never put it on any list ("UNKNOWN is not stored").
func TestLearnConnectsNormallyAndWritesNothingForSitesThatWork(t *testing.T) {
	up := startUpstream(t, allOK)
	r := newRig(t, policy.Learn, up)
	for i := 0; i < 6; i++ {
		if ans, mode := r.visit(host, up); ans != "OK" || mode != ModeDirect {
			t.Fatalf("visit %d: answer %q mode %q, want OK direct", i, ans, mode)
		}
	}
	if up.splitSeen.Load() != 0 {
		t.Fatalf("Learn must not fragment a site that works normally (%d fragmented hellos)", up.splitSeen.Load())
	}
	if r.pol.Skip.Len() != 0 || r.pol.Bypass.Len() != 0 || strings.Contains(r.file("bypass.txt"), host) {
		t.Fatal("a site that simply worked must not be added to any list")
	}
	r.wantNothingLearned()
}

// The core of Learn: a failure that the bypass fixes, found through real connections.
func TestLearnDiscoversNeedsBypassFromRealFailures(t *testing.T) {
	up := startUpstream(t, dpiResetsWhole)
	r := newRig(t, policy.Learn, up)

	if ans, mode := r.visit(host, up); ans != "OK" || mode != ModeAutoBypass {
		t.Fatalf("first visit: answer %q mode %q, want OK served through the bypass retry", ans, mode)
	}
	if r.pol.Decide(host) != policy.Unknown || strings.Contains(r.file("bypass.txt"), host) {
		t.Fatal("one failure + working retry must not be written yet: it could be a hiccup")
	}
	r.wantNothingLearned()

	if ans, _ := r.visit(host, up); ans != "OK" {
		t.Fatalf("second visit: %q", ans)
	}
	r.wantLearned(policy.NeedsBypass)
	if r.pol.Decide(host) != policy.NeedsBypass || !strings.Contains(r.file("bypass.txt"), host+" # learned ") {
		t.Fatalf("not persisted with its note: %q", r.file("bypass.txt"))
	}

	direct := up.directSeen.Load()
	if ans, mode := r.visit(host, up); ans != "OK" || mode != ModeBypass {
		t.Fatalf("third visit: answer %q mode %q, want OK via the bypass straight away", ans, mode)
	}
	if up.directSeen.Load() != direct {
		t.Fatal("once learned, the failing normal attempt must be skipped")
	}
	r.wantNothingLearned() // announced exactly once
}

// A DPI that silently drops packets makes the normal attempt time out: that qualifies too.
func TestLearnTreatsATimeoutOfTheNormalAttemptAsAFailureWorthRetrying(t *testing.T) {
	shortTimeouts(t)
	up := startUpstream(t, dpiHangsWhole)
	r := newRig(t, policy.Learn, up)
	if ans, mode := r.visit(host, up); ans != "OK" || mode != ModeAutoBypass {
		t.Fatalf("answer %q mode %q, want OK after the timeout retry", ans, mode)
	}
	r.visit(host, up)
	r.wantLearned(policy.NeedsBypass)
}

func TestLearnLearnsNothingWhenTheRetryFailsToo(t *testing.T) {
	up := startUpstream(t, allClose)
	r := newRig(t, policy.Learn, up)
	for i := 0; i < 4; i++ {
		if ans, mode := r.visit(host, up); ans != "" || mode != ModeFailed {
			t.Fatalf("visit %d: answer %q mode %q, want a clean failure", i, ans, mode)
		}
	}
	if r.pol.Skip.Len() != 0 || r.pol.Bypass.Len() != 0 {
		t.Fatalf("a site that fails either way proves nothing: skip=%v bypass=%v", r.pol.Skip.Items(), r.pol.Bypass.Items())
	}
	r.wantNothingLearned()
}

// A network hiccup breaks the normal attempt once; the next normal attempt works.
func TestLearnATransientFailureDoesNotBecomeAPermanentDecision(t *testing.T) {
	up := startUpstream(t, func(frag bool, n int) behavior {
		if !frag && (n == 0 || n == 2) { // the whole hello fails on the 1st and 3rd tries, works otherwise
			return bClose
		}
		return bOK
	})
	r := newRig(t, policy.Learn, up)
	r.visit(host, up) // fails normally, bypass works: evidence 1
	r.visit(host, up) // works normally: disproves it
	r.visit(host, up) // fails normally again: only evidence 1 again, not 2
	if r.pol.Decide(host) != policy.Unknown || r.pol.Bypass.Len() != 0 {
		t.Fatal("two isolated hiccups with a success in between must not become NEEDS_BYPASS")
	}
	r.wantNothingLearned()
}

func TestLearnUsesTheBypassFirstForAKnownNeedsBypassSite(t *testing.T) {
	up := startUpstream(t, dpiResetsWhole)
	r := newRig(t, policy.Learn, up)
	r.pol.SetState(host, policy.NeedsBypass, "set by the user")
	if ans, mode := r.visit(host, up); ans != "OK" || mode != ModeBypass {
		t.Fatalf("answer %q mode %q", ans, mode)
	}
	if up.directSeen.Load() != 0 {
		t.Fatal("a known NEEDS_BYPASS site must not try the failing normal connection first")
	}
}

func TestLearnNeverTriesTheBypassForANoBypassSite(t *testing.T) {
	up := startUpstream(t, dpiResetsWhole) // the normal attempt fails and the bypass would work...
	r := newRig(t, policy.Learn, up)
	r.pol.SetState(host, policy.NoBypass, "set by the user")
	if ans, mode := r.visit(host, up); ans != "" || mode == ModeBypass {
		t.Fatalf("answer %q mode %q: NO_BYPASS must connect directly and fail rather than bypass", ans, mode)
	}
	if up.splitSeen.Load() != 0 {
		t.Fatal("NO_BYPASS must never be fragmented, in Learn mode either")
	}
}

// ===================== Always =====================

func TestAlwaysBypassesUnknownSitesAndWritesNothingWhenTheyWork(t *testing.T) {
	up := startUpstream(t, allOK)
	r := newRig(t, policy.Always, up)
	for i := 0; i < 6; i++ {
		if ans, mode := r.visit(host, up); ans != "OK" || mode != ModeBypass {
			t.Fatalf("visit %d: answer %q mode %q, want OK bypassed", i, ans, mode)
		}
	}
	if up.directSeen.Load() != 0 {
		t.Fatal("Always must start with the bypass")
	}
	if r.pol.Skip.Len() != 0 || r.pol.Bypass.Len() != 0 {
		t.Fatal("visiting sites must not write UNKNOWN (or anything) to the lists")
	}
	r.wantNothingLearned()
}

func TestAlwaysLearnsNoBypassWhenTheBypassBreaksASiteTwice(t *testing.T) {
	up := startUpstream(t, brokenByFragments)
	r := newRig(t, policy.Always, up)
	if ans, mode := r.visit(host, up); ans != "OK" || mode != ModeAutoSkip {
		t.Fatalf("answer %q mode %q, want OK via the direct retry", ans, mode)
	}
	if r.pol.Decide(host) != policy.Unknown {
		t.Fatal("not on the first observation")
	}
	r.visit(host, up)
	r.wantLearned(policy.NoBypass)
	if r.pol.Decide(host) != policy.NoBypass || !strings.Contains(r.file("blacklist.txt"), host+" # auto-skip ") {
		t.Fatalf("not persisted: %q", r.file("blacklist.txt"))
	}
	frag := up.splitSeen.Load()
	if ans, mode := r.visit(host, up); ans != "OK" || mode != ModeDirect {
		t.Fatalf("third visit: answer %q mode %q", ans, mode)
	}
	if up.splitSeen.Load() != frag {
		t.Fatal("a NO_BYPASS site must not be fragmented again")
	}
}

func TestAlwaysNeverBypassesANoBypassSite(t *testing.T) {
	up := startUpstream(t, allOK)
	r := newRig(t, policy.Always, up)
	r.pol.SetState(host, policy.NoBypass, "set by the user")
	if ans, mode := r.visit(host, up); ans != "OK" || mode != ModeDirect {
		t.Fatalf("answer %q mode %q", ans, mode)
	}
	if up.splitSeen.Load() != 0 {
		t.Fatal("NO_BYPASS takes precedence over the Always default")
	}
}

// A learned NEEDS_BYPASS decision is not rewritten by a single contradicting failure.
func TestAlwaysKeepsAKnownNeedsBypassDecisionWhenTheBypassFailsOnce(t *testing.T) {
	up := startUpstream(t, brokenByFragments)
	r := newRig(t, policy.Always, up)
	r.pol.SetState(host, policy.NeedsBypass, "learned earlier")
	for i := 0; i < 3; i++ {
		if ans, mode := r.visit(host, up); ans != "OK" || mode == ModeFailed {
			t.Fatalf("visit %d: answer %q mode %q: the connection should still be served by the direct fallback", i, ans, mode)
		}
	}
	if r.pol.Decide(host) != policy.NeedsBypass || r.pol.Skip.Len() != 0 {
		t.Fatalf("a stored decision was rewritten by failures: skip=%v bypass=%v", r.pol.Skip.Items(), r.pol.Bypass.Items())
	}
	r.wantNothingLearned()
}

func TestAlwaysATransientFailureDoesNotBecomeAPermanentDecision(t *testing.T) {
	up := startUpstream(t, func(frag bool, n int) behavior {
		if frag && (n == 0 || n == 2) {
			return bClose
		}
		return bOK
	})
	r := newRig(t, policy.Always, up)
	r.visit(host, up)
	r.visit(host, up)
	r.visit(host, up)
	if r.pol.Decide(host) != policy.Unknown || r.pol.Skip.Len() != 0 {
		t.Fatal("isolated failures with a success in between must not become NO_BYPASS")
	}
	r.wantNothingLearned()
}

// A TLS alert to a whole hello is a real answer (the server refuses): it is not a DPI failure to learn from.
func TestLearnAnAlertToTheNormalAttemptIsNotAFailureToRetry(t *testing.T) {
	up := startUpstream(t, func(bool, int) behavior { return bAlert })
	r := newRig(t, policy.Learn, up)
	r.visit(host, up)
	if up.splitSeen.Load() != 0 {
		t.Fatalf("a server that answers with an alert must not trigger a bypass retry (%d fragmented hellos)", up.splitSeen.Load())
	}
	if r.pol.Bypass.Len() != 0 {
		t.Fatal("nothing may be learned from a server that simply refused")
	}
}

// ===================== both modes share the data =====================

// Learn -> Always -> Learn, and Always -> Learn, with two different sites, through real connections.
func TestLearnedDecisionsSurviveSwitchingModesAndRestarts(t *testing.T) {
	needs := startUpstream(t, dpiResetsWhole)    // site A: only works fragmented
	never := startUpstream(t, brokenByFragments) // site B: only works whole
	r := newRig(t, policy.Learn, needs, never)
	const a, b = "127.0.0.1", "localhost"

	// Learn: discover A
	r.visit(a, needs)
	r.visit(a, needs)
	r.wantLearned(policy.NeedsBypass)

	// Always: A keeps using the bypass; discover B
	r.mode = policy.Always
	direct := needs.directSeen.Load()
	if ans, mode := r.visit(a, needs); ans != "OK" || mode != ModeBypass || needs.directSeen.Load() != direct {
		t.Fatalf("Always lost A: %q %q", ans, mode)
	}
	r.visit(b, never)
	r.visit(b, never)
	r.wantLearned(policy.NoBypass)

	// back to Learn: both are still known
	r.mode = policy.Learn
	directA, splitB := needs.directSeen.Load(), never.splitSeen.Load()
	if ans, mode := r.visit(a, needs); ans != "OK" || mode != ModeBypass {
		t.Fatalf("Learn forgot A: %q %q", ans, mode)
	}
	if ans, mode := r.visit(b, never); ans != "OK" || mode != ModeDirect {
		t.Fatalf("Learn forgot B: %q %q", ans, mode)
	}
	if needs.directSeen.Load() != directA || never.splitSeen.Load() != splitB {
		t.Fatal("Learn went back to trying the failing method for sites it already knows")
	}

	// restart in each mode: same decisions
	for _, m := range []policy.Mode{policy.Always, policy.Learn} {
		r.mode = m
		r.start(needs, never)
		if r.pol.Decide(a) != policy.NeedsBypass || r.pol.Decide(b) != policy.NoBypass {
			t.Fatalf("%s: decisions lost across a restart", m)
		}
	}
}
