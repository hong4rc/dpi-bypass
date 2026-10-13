package proxy

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"
)

const diagHost = "blocked.example.test"

// diagServer is a TLS server for diagHost that closes the connection without a
// reply for a whole ClientHello (resetDirect, like a DPI block) or a fragmented
// one (resetSplit, a server that cannot reassemble).
type diagServer struct {
	addr  string
	roots *x509.CertPool
}

func startDiagServer(t *testing.T, resetDirect, resetSplit bool) *diagServer {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: diagHost}, DNSNames: []string{diagHost},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	cfg := &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				c.SetDeadline(time.Now().Add(5 * time.Second))
				h := make([]byte, 5)
				if _, err := io.ReadFull(c, h); err != nil {
					return
				}
				body := make([]byte, int(h[3])<<8|int(h[4]))
				if _, err := io.ReadFull(c, body); err != nil {
					return
				}
				hello := append(h, body...)
				if (fragmented(hello) && resetSplit) || (!fragmented(hello) && resetDirect) {
					return // closes without a reply
				}
				srv := tls.Server(&replayConn{Conn: c, r: io.MultiReader(bytes.NewReader(hello), c)}, cfg)
				if srv.Handshake() == nil {
					srv.Close()
				}
			}()
		}
	}()
	return &diagServer{addr: ln.Addr().String(), roots: pool}
}

// replayConn hands the bytes already read back to the TLS server.
type replayConn struct {
	net.Conn
	r io.Reader
}

func (c *replayConn) Read(p []byte) (int, error) { return c.r.Read(p) }

func (s *diagServer) env() DiagEnv {
	return DiagEnv{
		Lookup:  func(context.Context, string) ([]net.IP, error) { return []net.IP{net.ParseIP("203.0.113.7")}, nil },
		Dial:    func(ctx context.Context, n, _ string) (net.Conn, error) { return (&net.Dialer{}).DialContext(ctx, n, s.addr) },
		RootCAs: s.roots,
	}
}

func TestDiagnoseSiteThatWorksWithoutTheBypass(t *testing.T) {
	s := startDiagServer(t, false, false)
	rep := Diagnose(context.Background(), diagHost, s.env())
	if len(rep.Attempts) != 2 || rep.Attempts[0].Result != "ok" || rep.Attempts[1].Result != "ok" {
		t.Fatalf("attempts: %+v", rep.Attempts)
	}
	if rep.Verdict != "Works without the bypass." || rep.Suggest != "" {
		t.Fatalf("verdict %q suggest %q", rep.Verdict, rep.Suggest)
	}
	if a := rep.Attempts[0]; a.TLSVersion == "" || !strings.Contains(a.Subject, diagHost) {
		t.Fatalf("missing TLS detail: %+v", a)
	}
}

func TestDiagnoseBlockedSiteThatTheBypassFixes(t *testing.T) {
	s := startDiagServer(t, true, false) // a normal hello is reset, a split one passes
	rep := Diagnose(context.Background(), diagHost, s.env())
	if rep.Attempts[0].Result != "reset" || rep.Attempts[1].Result != "ok" {
		t.Fatalf("attempts: %+v", rep.Attempts)
	}
	if rep.Verdict != "This site is blocked; the bypass fixes it." || rep.Suggest != "NEEDS_BYPASS" {
		t.Fatalf("verdict %q suggest %q", rep.Verdict, rep.Suggest)
	}
}

func TestDiagnoseSiteThatTheBypassBreaks(t *testing.T) {
	s := startDiagServer(t, false, true)
	rep := Diagnose(context.Background(), diagHost, s.env())
	if rep.Attempts[0].Result != "ok" || rep.Attempts[1].Result != "reset" {
		t.Fatalf("attempts: %+v", rep.Attempts)
	}
	if rep.Verdict != "The bypass breaks this site." || rep.Suggest != "NO_BYPASS" {
		t.Fatalf("verdict %q suggest %q", rep.Verdict, rep.Suggest)
	}
}

func TestDiagnoseWhenNothingAnswers(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	dead := ln.Addr().String()
	ln.Close()
	env := DiagEnv{
		Lookup: func(context.Context, string) ([]net.IP, error) { return []net.IP{net.ParseIP("203.0.113.7")}, nil },
		Dial:   func(ctx context.Context, n, _ string) (net.Conn, error) { return (&net.Dialer{}).DialContext(ctx, n, dead) },
	}
	rep := Diagnose(context.Background(), diagHost, env)
	if rep.Attempts[0].Result == "ok" || rep.Attempts[1].Result == "ok" || rep.Suggest != "" ||
		!strings.HasPrefix(rep.Verdict, "Not a block the bypass can fix") {
		t.Fatalf("report: %+v", rep)
	}
}

func TestDiagnoseUntrustedCertificateIsNamedAsSuch(t *testing.T) {
	s := startDiagServer(t, false, false)
	env := s.env()
	env.RootCAs = x509.NewCertPool() // the server's certificate is not trusted
	rep := Diagnose(context.Background(), diagHost, env)
	if rep.Attempts[0].Result != "certificate error" {
		t.Fatalf("attempts: %+v", rep.Attempts)
	}
}

func TestDiagnoseLANAddressesAreNeverTried(t *testing.T) {
	dialed := false
	env := DiagEnv{
		Lookup: func(context.Context, string) ([]net.IP, error) { return []net.IP{net.ParseIP("192.168.1.5")}, nil },
		Dial:   func(context.Context, string, string) (net.Conn, error) { dialed = true; return nil, io.EOF },
	}
	rep := Diagnose(context.Background(), "nas.example.test", env)
	if dialed || len(rep.Attempts) != 0 || !strings.HasPrefix(rep.Verdict, "This name points to your own network") {
		t.Fatalf("dialed=%v report=%+v", dialed, rep)
	}
}

func TestDiagnoseUnknownNameStopsAtDNS(t *testing.T) {
	env := DiagEnv{Lookup: func(context.Context, string) ([]net.IP, error) { return nil, &net.DNSError{Err: "no such host", Name: "x", IsNotFound: true} }}
	rep := Diagnose(context.Background(), "nothing.example.test", env)
	if len(rep.Attempts) != 0 || rep.ResolveError == "" || !strings.Contains(rep.Verdict, "could not be found") {
		t.Fatalf("report: %+v", rep)
	}
}
