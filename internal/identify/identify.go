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
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"dpi/internal/stats"
)

//go:embed page.html
var pageHTML []byte

const maxBody = 16 << 10

// ReportFunc receives a device's report; client is the connection's RemoteAddr.
type ReportFunc func(client string, id stats.Identity) error

// New serves GET /identify (the page) and POST /identify/report.
func New(report ReportFunc) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /identify", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(pageHTML)
	})
	mux.HandleFunc("POST /identify/report", func(w http.ResponseWriter, r *http.Request) { handleReport(w, r, report) })
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

// SelfSigned makes a throwaway certificate valid for the given addresses. It is
// created in memory on every start, so no private key is ever written to disk.
func SelfSigned(ips []net.IP) (tls.Certificate, error) {
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
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
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

// TLSListener wraps ln so connections speak TLS 1.2+ with cert.
func TLSListener(ln net.Listener, cert tls.Certificate) net.Listener {
	return tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
}
