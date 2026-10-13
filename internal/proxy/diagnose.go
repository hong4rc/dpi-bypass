package proxy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"net"
	"strings"
	"time"
)

// DialFunc opens a TCP connection; tests swap it so nothing touches the network.
type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// DiagEnv is what Diagnose uses to reach the network. The zero value is the real
// network with normal certificate checking.
type DiagEnv struct {
	Lookup  func(ctx context.Context, host string) ([]net.IP, error)
	Dial    DialFunc
	RootCAs *x509.CertPool // nil: the system roots
}

// Attempt is one TLS handshake with a site, with or without the bypass.
type Attempt struct {
	Name        string `json:"name"`   // "direct" or "split"
	Result      string `json:"result"` // ok, timeout, reset, tls alert: X, certificate error, other
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
	ResolveMS    int64     `json:"resolveMs"`
	ResolveError string    `json:"resolveError,omitempty"`
	Attempts     []Attempt `json:"attempts"`
	Verdict      string    `json:"verdict"`
	Level        string    `json:"level"`             // ok, action (a list change is suggested), warn, bad: how the page colours the verdict
	Suggest      string    `json:"suggest,omitempty"` // "NEEDS_BYPASS", "NO_BYPASS" or empty
}

const diagHandshakeTimeout = 8 * time.Second

// Diagnose resolves host and tries a real TLS handshake on port 443 twice, on
// fresh connections to the same address: once untouched, once with the
// ClientHello split like the bypass does. It only reads: nothing is learned or saved.
func Diagnose(ctx context.Context, host string, env DiagEnv) Report {
	if env.Lookup == nil {
		env.Lookup = func(ctx context.Context, h string) ([]net.IP, error) {
			addrs, err := net.DefaultResolver.LookupIPAddr(ctx, h)
			var ips []net.IP
			for _, a := range addrs {
				ips = append(ips, a.IP)
			}
			return ips, err
		}
	}
	if env.Dial == nil {
		env.Dial = (&net.Dialer{Timeout: dialTimeout}).DialContext
	}
	rep := Report{Host: host, IPs: []string{}, Attempts: []Attempt{}}

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
	// prefer IPv4: the same address is used for both attempts so they are comparable
	pick := ips[0]
	for _, ip := range ips {
		if ip.To4() != nil {
			pick = ip
			break
		}
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

	addr := net.JoinHostPort(pick.String(), "443")
	direct := tryHandshake(ctx, env, host, addr, false)
	split := tryHandshake(ctx, env, host, addr, true)
	rep.Attempts = []Attempt{direct, split}
	// A list suggestion rests on a difference between the two attempts, so the
	// failing one is repeated once: a hiccup must not look like a block.
	confirmed := func(i int) bool {
		again := tryHandshake(ctx, env, host, addr, i == 1)
		rep.Attempts = append(rep.Attempts, again)
		return again.Result != "ok"
	}
	switch {
	case ctx.Err() != nil:
		rep.Verdict, rep.Level = "The test ran out of time. Try again.", "warn"
	case direct.Result == "ok":
		switch {
		case split.Result == "ok":
			rep.Verdict, rep.Level = "Works without the bypass.", "ok"
		case !confirmed(1) || ctx.Err() != nil:
			rep.Verdict, rep.Level = "Mixed result: the bypass failed once but worked on the repeat. Try again.", "warn"
		default:
			rep.Verdict, rep.Level = "The bypass breaks this site.", "action"
			rep.Suggest = "NO_BYPASS"
		}
	case split.Result == "ok":
		switch {
		case !confirmed(0) || ctx.Err() != nil:
			rep.Verdict, rep.Level = "Mixed result: a normal connection failed once but worked on the repeat. Try again.", "warn"
		default:
			rep.Verdict, rep.Level = "This site is blocked; the bypass fixes it.", "action"
			rep.Suggest = "NEEDS_BYPASS"
		}
	default:
		rep.Verdict, rep.Level = "Not a block the bypass can fix (site down, IP or DNS blocked).", "bad"
	}
	return rep
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
