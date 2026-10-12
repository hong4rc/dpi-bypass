// Package identify lets a phone tell the PC what it is. The model and OS
// version are only exposed to JavaScript on an HTTPS page (Client Hints), never
// in headers the proxy can see, so the proxy serves a tiny HTTPS page with a
// self-signed certificate: scan a QR code, tap through the browser's warning
// once, and the page reports the device.
package identify

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	_ "embed"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dpi/internal/stats"
)

//go:embed page.html
var pageHTML []byte

const maxBody = 16 << 10

// ReportFunc receives a device's report; client is the connection's RemoteAddr.
type ReportFunc func(client string, id stats.Identity) error

// ProbeHost is a made-up name the page requests to find out whether the phone
// sends its HTTPS traffic through the proxy. The proxy recognises the name and
// reports who asked: only a phone that uses the proxy can get a request for it there.
const ProbeHost = "probe.dpi-bypass.invalid"

// StatusFunc tells whether client is using the proxy, and the proxy setup address
// to give it. host is the address the client used to reach this page.
type StatusFunc func(client, host string) (usingProxy bool, pac string)

// New serves GET /identify (the page), POST /identify/report and GET /identify/status.
// status may be nil: the page then can not tell whether the phone uses the proxy.
func New(report ReportFunc, status StatusFunc) http.Handler {
	page := []byte(strings.ReplaceAll(string(pageHTML), "{{PROBE_HOST}}", ProbeHost))
	mux := http.NewServeMux()
	mux.HandleFunc("GET /identify", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(page)
	})
	mux.HandleFunc("POST /identify/report", func(w http.ResponseWriter, r *http.Request) { handleReport(w, r, report) })
	mux.HandleFunc("GET /identify/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		out := map[string]any{"checked": status != nil}
		if status != nil {
			host, _, err := net.SplitHostPort(r.Host)
			if err != nil {
				host = r.Host
			}
			using, pac := status(r.RemoteAddr, host)
			out["usingProxy"], out["pac"] = using, pac
		}
		json.NewEncoder(w).Encode(out)
	})
	return mux
}

// body is what the page sends: the userAgentData high-entropy values plus a few basics.
type body struct {
	UserAgent       string  `json:"userAgent"`
	Language        string  `json:"language"`
	Screen          string  `json:"screen"`
	Mobile          bool    `json:"mobile"`
	Model           string  `json:"model"`
	Platform        string  `json:"platform"`
	PlatformVersion string  `json:"platformVersion"`
	Architecture    string  `json:"architecture"`
	Bitness         string  `json:"bitness"`
	Brands          []brand `json:"brands"`
	FullVersionList []brand `json:"fullVersionList"`
}

type brand struct {
	Brand   string `json:"brand"`
	Version string `json:"version"`
}

func handleReport(w http.ResponseWriter, r *http.Request, report ReportFunc) {
	if o := r.Header.Get("Origin"); o != "" {
		if u, err := url.Parse(o); err != nil || u.Host != r.Host {
			http.Error(w, "cross-site request blocked", http.StatusForbidden)
			return
		}
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	var b body
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		http.Error(w, "bad report", http.StatusBadRequest)
		return
	}
	id := stats.Identity{
		Model:           clean(b.Model, 80),
		Platform:        clean(b.Platform, 40),
		PlatformVersion: clean(b.PlatformVersion, 40),
		Architecture:    clean(b.Architecture, 20),
		Bitness:         clean(b.Bitness, 8),
		Mobile:          b.Mobile,
		Browser:         browserName(b),
		UserAgent:       clean(b.UserAgent, 300),
		Screen:          clean(b.Screen, 40),
		Language:        clean(b.Language, 20),
	}
	if err := report(r.RemoteAddr, id); err != nil {
		http.Error(w, "could not save", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "model": id.Model, "label": id.Describe()})
}

// clean trims, drops control characters and limits the length: the values come from the network.
func clean(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	if len(s) > max {
		s = s[:max]
	}
	return s
}

// browserName picks "Google Chrome 154" from the brand lists, skipping the
// "Chromium" engine and the deliberately fake "Not A(Brand" entry.
func browserName(b body) string {
	list := b.FullVersionList
	if len(list) == 0 {
		list = b.Brands
	}
	pick := brand{}
	for _, x := range list {
		name := strings.ToLower(x.Brand)
		if strings.Contains(name, "not") && strings.Contains(name, "brand") {
			continue
		}
		if pick.Brand == "" || (strings.EqualFold(pick.Brand, "Chromium") && !strings.EqualFold(x.Brand, "Chromium")) {
			pick = x
		}
	}
	if pick.Brand == "" {
		return ""
	}
	major, _, _ := strings.Cut(pick.Version, ".")
	return clean(pick.Brand+" "+major, 60)
}

// SelfSigned makes a certificate valid for the given addresses, in memory only.
func SelfSigned(ips []net.IP) (tls.Certificate, error) {
	return selfSigned(ips, time.Now().Add(365*24*time.Hour))
}

func selfSigned(ips []net.IP, notAfter time.Time) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 100))
	if err != nil {
		return tls.Certificate{}, err
	}
	tpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "dpi-bypass identify page"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  ips,
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
}

// CertFile is the name of the saved certificate (next to the exe).
const CertFile = "identify-cert.pem"

// renewBefore: a saved certificate with less than this left is replaced.
const renewBefore = 30 * 24 * time.Hour

// Persistent returns the certificate saved in dir, creating it when it is
// missing, unreadable, about to expire, or not valid for one of ips. Keeping
// the same certificate means the browser's "proceed anyway" choice for it keeps
// working after the app restarts, so the warning does not return every time.
func Persistent(dir string, ips []net.IP) (tls.Certificate, error) {
	path := filepath.Join(dir, CertFile)
	if cert, ok := loadUsable(path, ips); ok {
		return cert, nil
	}
	cert, err := SelfSigned(ips)
	if err != nil {
		return cert, err
	}
	key, err := x509.MarshalECPrivateKey(cert.PrivateKey.(*ecdsa.PrivateKey))
	if err != nil {
		return cert, err
	}
	data := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]})
	data = append(data, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: key})...)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return cert, err // still usable this run; it just will not be remembered
	}
	return cert, nil
}

func loadUsable(path string, ips []net.IP) (tls.Certificate, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return tls.Certificate{}, false
	}
	cert, err := tls.X509KeyPair(data, data)
	if err != nil {
		return tls.Certificate{}, false
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil || time.Until(leaf.NotAfter) < renewBefore {
		return tls.Certificate{}, false
	}
	for _, ip := range ips {
		if leaf.VerifyHostname(ip.String()) != nil {
			return tls.Certificate{}, false
		}
	}
	return cert, true
}

// TLSListener wraps ln so connections speak TLS 1.2+ with cert.
func TLSListener(ln net.Listener, cert tls.Certificate) net.Listener {
	return tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
}
