package identify

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"dpi/internal/stats"
)

type got struct {
	client string
	id     stats.Identity
}

// start serves the identify handler over real TLS with a fresh self-signed
// certificate, and returns a client that trusts exactly that certificate.
func start(t *testing.T, report ReportFunc) (*httptest.Server, *http.Client) {
	t.Helper()
	cert, err := SelfSigned([]net.IP{net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(New(report))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	srv.StartTLS()
	t.Cleanup(srv.Close)

	leaf, _ := x509.ParseCertificate(cert.Certificate[0])
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return srv, &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}
}

func send(t *testing.T, c *http.Client, method, url, body string, hdr map[string]string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// What deviceinfo.me showed for a real Shark PAR-A0 running Chrome 154.
const sharkReport = `{
  "userAgent": "Mozilla/5.0 (Linux; Android 10; K) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/154.0.0.0 Mobile Safari/537.36",
  "language": "en-GB", "screen": "1080x2400 @2.75", "mobile": true,
  "model": "SHARK PAR-A0", "platform": "Android", "platformVersion": "12.0.0", "architecture": "", "bitness": "",
  "brands": [{"brand":"Chromium","version":"154"},{"brand":"Google Chrome","version":"154"},{"brand":"Not A(Brand","version":"99"}],
  "fullVersionList": [{"brand":"Chromium","version":"154.0.8037.127"},{"brand":"Google Chrome","version":"154.0.8037.127"},{"brand":"Not A(Brand","version":"99.0.0.0"}]
}`

func TestPageIsServedAndAsksForTheHighEntropyValues(t *testing.T) {
	srv, c := start(t, func(string, stats.Identity) error { return nil })
	code, page := send(t, c, "GET", srv.URL+"/identify", "", nil)
	if code != 200 || !strings.Contains(page, "getHighEntropyValues") || !strings.Contains(page, "'model'") {
		t.Fatalf("page: %d, asks for model: %v", code, strings.Contains(page, "'model'"))
	}
	for _, path := range []string{"/", "/api/state", "/identify/other"} {
		if code, _ := send(t, c, "GET", srv.URL+path, "", nil); code != 404 {
			t.Errorf("%s should not exist on the identify port, got %d", path, code)
		}
	}
}

func TestAReportBecomesAnIdentityForThatClient(t *testing.T) {
	reports := make(chan got, 1)
	srv, c := start(t, func(client string, id stats.Identity) error { reports <- got{client, id}; return nil })

	code, out := send(t, c, "POST", srv.URL+"/identify/report", sharkReport, nil)
	if code != 200 || !strings.Contains(out, `"ok":true`) || !strings.Contains(out, "SHARK PAR-A0 · Android 12") {
		t.Fatalf("response: %d %s", code, out)
	}
	r := <-reports
	if !strings.HasPrefix(r.client, "127.0.0.1:") {
		t.Fatalf("the report must be keyed by the connection's address, got %q", r.client)
	}
	id := r.id
	if id.Model != "SHARK PAR-A0" || id.Platform != "Android" || id.PlatformVersion != "12.0.0" || !id.Mobile {
		t.Fatalf("identity: %+v", id)
	}
	if id.Browser != "Google Chrome 154" {
		t.Fatalf("browser should skip Chromium and the fake brand, got %q", id.Browser)
	}
	if id.Screen != "1080x2400 @2.75" || id.Language != "en-GB" {
		t.Fatalf("extras: %+v", id)
	}
}

func TestReportsAreValidatedAndCleaned(t *testing.T) {
	var last stats.Identity
	srv, c := start(t, func(_ string, id stats.Identity) error { last = id; return nil })
	url := srv.URL + "/identify/report"

	if code, _ := send(t, c, "POST", url, "not json", nil); code != http.StatusBadRequest {
		t.Errorf("bad json got %d", code)
	}
	if code, _ := send(t, c, "POST", url, `{"model":"`+strings.Repeat("x", 40<<10)+`"}`, nil); code != http.StatusBadRequest {
		t.Errorf("oversized body got %d", code)
	}
	if code, _ := send(t, c, "GET", url, "", nil); code != http.StatusMethodNotAllowed {
		t.Errorf("GET on the report endpoint got %d", code)
	}
	if code, _ := send(t, c, "POST", url, sharkReport, map[string]string{"Origin": "https://evil.example.com"}); code != http.StatusForbidden {
		t.Errorf("cross-site report got %d", code)
	}

	// control characters are dropped and long values cut: they come from the network
	evil := `{"model":"  Pix\u0000el\n 7  ","platform":"` + strings.Repeat("A", 500) + `"}`
	if code, _ := send(t, c, "POST", url, evil, nil); code != 200 {
		t.Fatalf("report with odd values got %d", code)
	}
	if last.Model != "Pixel 7" || len(last.Platform) != 40 {
		t.Fatalf("values not cleaned: model %q, platform length %d", last.Model, len(last.Platform))
	}
}

func TestACertificateIsValidForTheLANAddressesAndNeverWrittenToDisk(t *testing.T) {
	ips := []net.IP{net.ParseIP("192.168.1.10"), net.ParseIP("127.0.0.1")}
	cert, err := SelfSigned(ips)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(cert.Certificate[0])
	for _, ip := range ips {
		if err := leaf.VerifyHostname(ip.String()); err != nil {
			t.Errorf("certificate not valid for %s: %v", ip, err)
		}
	}
	if err := leaf.VerifyHostname("192.168.1.99"); err == nil {
		t.Error("certificate must not be valid for other addresses")
	}
	if left := time.Until(leaf.NotAfter); left < 300*24*time.Hour || left > 400*24*time.Hour {
		t.Errorf("validity should be about a year, got %s", left)
	}
	other, _ := SelfSigned(ips)
	if string(other.Certificate[0]) == string(cert.Certificate[0]) {
		t.Error("each start must generate a new key")
	}
}

func TestTLSListenerRejectsOldProtocols(t *testing.T) {
	cert, _ := SelfSigned([]net.IP{net.ParseIP("127.0.0.1")})
	raw, _ := net.Listen("tcp", "127.0.0.1:0")
	ln := TLSListener(raw, cert)
	go http.Serve(ln, New(func(string, stats.Identity) error { return nil }))
	t.Cleanup(func() { ln.Close() })

	old := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MaxVersion: tls.VersionTLS11}}}
	if resp, err := old.Get("https://" + raw.Addr().String() + "/identify"); err == nil {
		resp.Body.Close()
		t.Fatal("TLS 1.1 must be refused")
	}
	now := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	resp, err := now.Get("https://" + raw.Addr().String() + "/identify")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
}
