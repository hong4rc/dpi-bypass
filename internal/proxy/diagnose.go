package proxy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"net"
	"sort"
	"strings"
	"sync"
	"time"
)

// DialFunc opens a TCP connection; tests swap it so nothing touches the network.
type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// DiagEnv is what Diagnose uses to reach the network. The zero value is the real
// network with normal certificate checking.
type DiagEnv struct {
	Lookup func(ctx context.Context, host string) ([]net.IP, error)
	// Public asks a public resolver, never a cache, so the network's own answer can be
	// compared with it. Nil skips the comparison.
	Public  func(ctx context.Context, host string) []net.IP
	Dial    DialFunc
	RootCAs *x509.CertPool // nil: the system roots
}

// Attempt is one TLS handshake with a site, with or without the bypass.
type Attempt struct {
	Name        string `json:"name"`             // "direct" or "split"
	IP          string `json:"ip,omitempty"`     // the address that was tried
	Source      string `json:"source,omitempty"` // "public" when the address came from public DNS, empty for this network's DNS
	Result      string `json:"result"`           // ok, timeout, reset, tls alert: X, certificate error, other
	Error       string `json:"error,omitempty"`
	DialMS      int64  `json:"dialMs"`
	HandshakeMS int64  `json:"handshakeMs"`
	TLSVersion  string `json:"tlsVersion,omitempty"`
	Subject     string `json:"subject,omitempty"`
	Issuer      string `json:"issuer,omitempty"`
}

// Report is the outcome of Diagnose. It never changes any site list.
type Report struct {
	Host         string    `json:"host"`
	IPs          []string  `json:"ips"`
	PublicIPs    []string  `json:"publicIps,omitempty"` // what a public DNS says, asked fresh
	ResolveMS    int64     `json:"resolveMs"`
	ResolveError string    `json:"resolveError,omitempty"`
	DNSFallback  bool      `json:"dnsFallback,omitempty"` // this network's DNS could not find the name; a public resolver did (not the bypass)
	Attempts     []Attempt `json:"attempts"`
	Verdict      string    `json:"verdict"`
	Level        string    `json:"level"`             // ok, action (a list change is suggested), warn, bad: how the page colours the verdict
	Suggest      string    `json:"suggest,omitempty"` // "NEEDS_BYPASS", "NO_BYPASS" or empty
}

const (
	diagHandshakeTimeout = 8 * time.Second
	diagAddrsPerSource   = 2 // addresses tried from this network's DNS, and again from public DNS
)

// candidate is one address to test and where it came from.
type candidate struct {
	ip     net.IP
	public bool
}

// ipResult is what testing one address showed.
type ipResult struct {
	attempts       []Attempt
	verdict, level string
	suggest        string
	public         bool
}

// Diagnose resolves host and tries a real TLS handshake on port 443 twice per
// address, on fresh connections: once untouched, once with the ClientHello split
// like the bypass does. Several addresses are tried, those this network's DNS
// gives and those a public DNS gives (asked fresh), because a block can depend on
// the address. It only reads: nothing is learned or saved.
func Diagnose(ctx context.Context, host string, env DiagEnv) Report {
	rep := Report{Host: host, IPs: []string{}, Attempts: []Attempt{}}
	if env.Lookup == nil {
		env.Lookup = func(ctx context.Context, h string) ([]net.IP, error) {
			defaultResolver.Forget(h) // a test must not answer from an earlier one
			ips, via, err := defaultResolver.Lookup(ctx, h)
			rep.DNSFallback = via
			return ips, err
		}
		if env.Public == nil {
			env.Public = defaultResolver.Public
		}
	}
	if env.Dial == nil {
		env.Dial = (&net.Dialer{Timeout: dialTimeout}).DialContext
	}

	t0 := time.Now()
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	ips, err := env.Lookup(rctx, host)
	cancel()
	rep.ResolveMS = time.Since(t0).Milliseconds()
	if err != nil || len(ips) == 0 {
		if err != nil {
			rep.ResolveError = err.Error()
		}
		rep.Verdict, rep.Level = "The name could not be found (DNS problem or no internet).", "bad"
		return rep
	}
	private := false
	for _, ip := range ips {
		rep.IPs = append(rep.IPs, ip.String())
		private = private || isPrivate(ip.String())
	}
	if private {
		rep.Verdict, rep.Level = "This name points to your own network, so it never uses the bypass.", "ok"
		return rep
	}

	var pub []net.IP
	if env.Public != nil && !rep.DNSFallback { // after a fallback the addresses above already are the public ones
		pctx, cancel := context.WithTimeout(ctx, 4*time.Second)
		pub = env.Public(pctx, host)
		cancel()
		for _, ip := range pub {
			rep.PublicIPs = append(rep.PublicIPs, ip.String())
		}
	}

	cands := candidates(ips, pub, rep.DNSFallback)
	results := make([]ipResult, len(cands))
	var wg sync.WaitGroup
	for i, c := range cands { // addresses in parallel so the time is that of one; the two handshakes of an address stay in turn
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = judge(ctx, env, host, c)
		}()
	}
	wg.Wait()

	best := results[0]
	for _, r := range results {
		rep.Attempts = append(rep.Attempts, r.attempts...)
		if r.rank() > best.rank() {
			best = r
		}
	}
	rep.Verdict, rep.Level, rep.Suggest = best.verdict, best.level, best.suggest

	// the network's own addresses all fail while a public DNS gives ones that work: only the DNS can be at fault
	netAddrs, netBad, pubOK := 0, 0, false
	for _, r := range results {
		switch {
		case !r.public:
			netAddrs++
			if r.level == "bad" {
				netBad++
			}
		case r.level == "ok":
			pubOK = true
		}
	}
	if netAddrs > 0 && netBad == netAddrs && pubOK {
		rep.Verdict, rep.Level, rep.Suggest = "This network's DNS gives addresses that do not work, but a public DNS gives ones that do. Change your DNS; the bypass cannot fix this.", "bad", ""
	}
	return rep
}

// candidates picks the addresses to test: up to diagAddrsPerSource from this
// network's DNS, then up to as many from public DNS that the network did not
// already give. IPv4 first, since that is what most connections use.
func candidates(network, public []net.IP, networkIsPublic bool) []candidate {
	seen := map[string]bool{}
	var out []candidate
	take := func(list []net.IP, isPublic bool) {
		list = append([]net.IP(nil), list...)
		sort.SliceStable(list, func(i, j int) bool { return list[i].To4() != nil && list[j].To4() == nil })
		n := 0
		for _, ip := range list {
			if n == diagAddrsPerSource {
				return
			}
			if seen[ip.String()] || isPrivate(ip.String()) {
				continue
			}
			seen[ip.String()] = true
			out = append(out, candidate{ip, isPublic})
			n++
		}
	}
	take(network, networkIsPublic)
	take(public, true)
	return out
}

// judge tests one address with and without the bypass.
func judge(ctx context.Context, env DiagEnv, host string, c candidate) ipResult {
	addr := net.JoinHostPort(c.ip.String(), "443")
	direct := tryHandshake(ctx, env, host, addr, false)
	split := tryHandshake(ctx, env, host, addr, true)
	res := ipResult{attempts: []Attempt{direct, split}, public: c.public}
	// A list suggestion rests on a difference between the two attempts, so the
	// failing one is repeated once: a hiccup must not look like a block.
	confirmed := func(i int) bool {
		again := tryHandshake(ctx, env, host, addr, i == 1)
		res.attempts = append(res.attempts, again)
		return again.Result != "ok"
	}
	switch {
	case ctx.Err() != nil:
		res.verdict, res.level = "The test ran out of time. Try again.", "warn"
	case direct.Result == "ok":
		switch {
		case split.Result == "ok":
			res.verdict, res.level = "Works without the bypass.", "ok"
		case !confirmed(1) || ctx.Err() != nil:
			res.verdict, res.level = "Mixed result: the bypass failed once but worked on the repeat. Try again.", "warn"
		default:
			res.verdict, res.level = "The bypass breaks this site.", "action"
			res.suggest = "NO_BYPASS"
		}
	case split.Result == "ok":
		switch {
		case !confirmed(0) || ctx.Err() != nil:
			res.verdict, res.level = "Mixed result: a normal connection failed once but worked on the repeat. Try again.", "warn"
		default:
			res.verdict, res.level = "This site is blocked; the bypass fixes it.", "action"
			res.suggest = "NEEDS_BYPASS"
		}
	default:
		res.verdict, res.level = "Not a block the bypass can fix (site down, IP or DNS blocked).", "bad"
	}
	if c.public {
		for i := range res.attempts {
			res.attempts[i].Source = "public"
		}
	}
	return res
}

// rank orders results for the verdict: a finding about the bypass beats a mixed
// result, which beats "works", which beats one dead address among working ones.
func (r ipResult) rank() int {
	switch r.level {
	case "action":
		return 4
	case "warn":
		return 3
	case "ok":
		return 2
	}
	return 1
}

// splitFirstWrite sends the first write (the ClientHello) split in two, like the bypass.
type splitFirstWrite struct {
	net.Conn
	done bool
}

func (c *splitFirstWrite) Write(b []byte) (int, error) {
	if c.done {
		return c.Conn.Write(b)
	}
	c.done = true
	if err := writeSplit(c.Conn, b); err != nil {
		return 0, err
	}
	return len(b), nil
}

func tryHandshake(ctx context.Context, env DiagEnv, host, addr string, split bool) Attempt {
	at := Attempt{Name: "direct", Result: "other"}
	at.IP, _, _ = net.SplitHostPort(addr)
	if split {
		at.Name = "split"
	}
	t0 := time.Now()
	raw, err := env.Dial(ctx, "tcp", addr)
	at.DialMS = time.Since(t0).Milliseconds()
	if err != nil {
		at.Result, at.Error = classify(err), err.Error()
		return at
	}
	defer raw.Close()
	conn := net.Conn(raw)
	if split {
		conn = &splitFirstWrite{Conn: raw}
	}
	raw.SetDeadline(time.Now().Add(diagHandshakeTimeout))
	tc := tls.Client(conn, &tls.Config{ServerName: host, RootCAs: env.RootCAs})
	t1 := time.Now()
	err = tc.HandshakeContext(ctx)
	at.HandshakeMS = time.Since(t1).Milliseconds()
	if err != nil {
		at.Result, at.Error = classify(err), err.Error()
		return at
	}
	st := tc.ConnectionState()
	at.Result = "ok"
	at.TLSVersion = tls.VersionName(st.Version)
	if len(st.PeerCertificates) > 0 {
		c := st.PeerCertificates[0]
		at.Subject, at.Issuer = CertName(c.Subject), CertName(c.Issuer)
	}
	return at
}

// classify names a network or TLS error the way a person would look for it.
func classify(err error) string {
	var (
		ne     net.Error
		unk    x509.UnknownAuthorityError
		host   x509.HostnameError
		inval  x509.CertificateInvalidError
		verify *tls.CertificateVerificationError
	)
	msg := err.Error()
	switch {
	case errors.As(err, &verify), errors.As(err, &unk), errors.As(err, &host), errors.As(err, &inval):
		return "certificate error"
	case strings.Contains(msg, "remote error: tls:"):
		return "tls alert:" + msg[strings.Index(msg, "remote error: tls:")+len("remote error: tls:"):]
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		return "timeout"
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF),
		strings.Contains(msg, "reset by peer"), strings.Contains(msg, "forcibly closed"), strings.Contains(msg, "connection aborted"), strings.Contains(msg, "aborted by the software"):
		return "reset"
	}
	return "other"
}

// CertName is the short name of a certificate subject or issuer: its common
// name, or the organisation when there is none ("CN=*.example.com,O=Example\, Inc.,..." is unreadable).
func CertName(n pkix.Name) string {
	switch {
	case n.CommonName != "" && len(n.Organization) > 0 && !strings.Contains(n.CommonName, " ") && strings.Contains(n.CommonName, "."):
		return n.CommonName // a host name: the organisation adds nothing
	case len(n.Organization) > 0:
		return n.Organization[0]
	}
	return n.CommonName
}
