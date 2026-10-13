package app

import (
	"crypto/x509/pkix"
	"strings"
	"testing"

	"dpi/internal/proxy"
)

func TestReportTextIsPlainAndKeepsRawErrorsForTheEnd(t *testing.T) {
	r := DiagnoseResult{
		Report: proxy.Report{
			Host: "example.com", IPs: []string{"203.0.113.9"}, ResolveMS: 3,
			Attempts: []proxy.Attempt{
				{Name: "direct", Result: "reset", Error: "read tcp 10.0.0.2:1->203.0.113.9:443: wsarecv: forcibly closed", DialMS: 70, HandshakeMS: 12},
				{Name: "split", Result: "ok", TLSVersion: "TLS 1.3", Subject: "example.com", Issuer: "Example CA", DialMS: 71, HandshakeMS: 150},
				{Name: "direct", Result: "reset", Error: "again", DialMS: 70, HandshakeMS: 12},
			},
			Verdict: "This site is blocked; the bypass fixes it.", Level: "action", Suggest: "NEEDS_BYPASS",
		},
		Listed: "UNKNOWN", Mode: "learn", Plan: "connects normally first, uses the bypass if that fails",
	}
	text := r.text()
	for _, want := range []string{
		"Normal connection: the connection was cut",
		"With the bypass: worked",
		"Normal connection (repeated to confirm)",
		"Secure connection: TLS 1.3, certificate for example.com, issued by Example CA",
		"Next step: add it to the Needs bypass list.",
		"On the lists: not listed",
		"Bypass on this PC: off",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	head, raw, found := strings.Cut(text, "Technical errors")
	if !found || strings.Contains(head, "wsarecv") || !strings.Contains(raw, "wsarecv") {
		t.Fatalf("raw errors must only appear in the last section:\n%s", text)
	}
}

func TestNextStep(t *testing.T) {
	for name, c := range map[string]struct {
		r    DiagnoseResult
		want string
	}{
		"works":          {DiagnoseResult{Report: proxy.Report{Level: "ok"}}, "Nothing to do."},
		"already listed": {DiagnoseResult{Report: proxy.Report{Level: "action", Suggest: "NEEDS_BYPASS"}, Listed: "NEEDS_BYPASS"}, "already on the Needs bypass list"},
		"breaks":         {DiagnoseResult{Report: proxy.Report{Level: "action", Suggest: "NO_BYPASS"}}, "add it to the Never bypass list"},
		"mixed":          {DiagnoseResult{Report: proxy.Report{Level: "warn"}}, "Run the test again."},
		"down":           {DiagnoseResult{Report: proxy.Report{Level: "bad"}}, "cannot fix this"},
	} {
		if got := c.r.nextStep(); !strings.Contains(got, c.want) {
			t.Errorf("%s: %q lacks %q", name, got, c.want)
		}
	}
}

func TestCertNameIsShortAndReadable(t *testing.T) {
	for want, n := range map[string]pkix.Name{
		"*.example.com": {CommonName: "*.example.com", Organization: []string{"Example, Inc."}},
		"DigiCert Inc":  {CommonName: "DigiCert Global G2 TLS RSA SHA256 2020 CA1", Organization: []string{"DigiCert Inc"}},
		"Lone CN":       {CommonName: "Lone CN"},
	} {
		if got := proxy.CertName(n); got != want {
			t.Errorf("CertName(%+v)=%q want %q", n, got, want)
		}
	}
}
