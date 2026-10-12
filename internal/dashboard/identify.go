package dashboard

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/skip2/go-qrcode"
)

// phoneItem is one address of this PC that a phone can use: where to get the
// proxy setup (PAC) and where the identify page lives, with a check of each.
type phoneItem struct {
	IP           string `json:"ip"`
	Primary      bool   `json:"primary"` // the address the PC uses for the internet: usually the phone's Wi-Fi
	PAC          string `json:"pac"`
	PACReachable bool   `json:"pacReachable"`
	Identify     string `json:"identify"`
	IdentifyOK   bool   `json:"identifyReachable"`
}

type phoneSetupJSON struct {
	Enabled bool        `json:"enabled"`
	Items   []phoneItem `json:"items"`
}

// phoneSetup lists this PC's addresses for phones and checks that each one really answers.
func (h *handler) phoneSetup(w http.ResponseWriter, r *http.Request) {
	pac := map[string]string{}
	for _, u := range h.a.LANURLs() {
		pac[hostOf(u)] = u
	}
	var items []phoneItem
	for i, u := range h.a.IdentifyURLs() {
		ip := hostOf(u)
		items = append(items, phoneItem{IP: ip, Primary: i == 0, PAC: pac[ip], Identify: u})
	}
	if items == nil {
		items = []phoneItem{}
	}
	var wg sync.WaitGroup
	for i := range items {
		wg.Add(2)
		go func(it *phoneItem) { defer wg.Done(); it.PACReachable = it.PAC != "" && probePAC(it.PAC) }(&items[i])
		go func(it *phoneItem) { defer wg.Done(); it.IdentifyOK = probeIdentify(it.Identify) }(&items[i])
	}
	wg.Wait()
	writeJSON(w, phoneSetupJSON{Enabled: len(items) > 0, Items: items})
}

// hostOf returns the IP of "http(s)://ip:port/path".
func hostOf(u string) string {
	u = u[strings.Index(u, "://")+3:]
	if i := strings.IndexByte(u, '/'); i >= 0 {
		u = u[:i]
	}
	ip, _, _ := net.SplitHostPort(u)
	return ip
}

func probeClient() *http.Client {
	return &http.Client{
		Timeout:       1500 * time.Millisecond,
		Transport:     &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// probePAC fetches the proxy setup file the way a phone would.
func probePAC(u string) bool {
	resp, err := probeClient().Get(u)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode == http.StatusOK && strings.Contains(string(b), "FindProxyForURL")
}

// probeIdentify fetches the identify page as a phone does after tapping "Proceed" on the certificate warning.
func probeIdentify(u string) bool {
	resp, err := probeClient().Get(u)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// qr renders a QR code for one of this PC's phone addresses (the setup address
// or the identify page) and for nothing else: it is not a general QR generator.
func (h *handler) qr(w http.ResponseWriter, r *http.Request) {
	want := r.URL.Query().Get("url")
	for _, u := range append(h.a.LANURLs(), h.a.IdentifyURLs()...) {
		if u != want {
			continue
		}
		svg, err := qrSVG(u)
		if err != nil {
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Cache-Control", "no-store")
		fmt.Fprint(w, svg)
		return
	}
	fail(w, http.StatusNotFound, "not an address of this PC")
}

// qrSVG draws text as a QR code: black modules on a white square (white even in
// dark mode, so phone cameras can read it), with the standard quiet border.
func qrSVG(text string) (string, error) {
	q, err := qrcode.New(text, qrcode.Medium)
	if err != nil {
		return "", err
	}
	bm := q.Bitmap()
	n := len(bm)
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges" role="img" aria-label="QR code"><rect width="%d" height="%d" fill="#fff"/><path fill="#000" d="`, n, n, n, n)
	for y, row := range bm {
		for x := 0; x < n; {
			if !row[x] {
				x++
				continue
			}
			start := x
			for x < n && row[x] {
				x++
			}
			fmt.Fprintf(&b, "M%d %dh%dv1h-%dz", start, y, x-start, x-start) // one rectangle per run of dark modules
		}
	}
	b.WriteString(`"/></svg>`)
	return b.String(), nil
}
