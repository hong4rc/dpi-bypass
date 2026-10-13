package app

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"dpi/internal/stats"
)

// checkTrace is what one test request through the proxy went through, for the details.
type checkTrace struct {
	Status      int           // HTTP status of the final response (0 if none)
	Proto       string        // HTTP/1.1, HTTP/2.0
	ConnectCode string        // the proxy's answer to "open a tunnel": "200 OK", "502 Bad Gateway"...
	TunnelAt    time.Duration // until the tunnel to the site was ready
	TLS         time.Duration // TLS handshake through the tunnel
	Total       time.Duration
	TLSVersion  string
	Subject     string
	Issuer      string
}

// checkDetail is the step-by-step account shown under "details" after the Steam
// test: what was asked of the proxy and how the proxy reached the site, then
// the same report "Test a site" gives (normal against bypass, lists, settings).
func (a *App) checkDetail(tr checkTrace, err error, began time.Time) string {
	u, _ := url.Parse(a.checkTarget())
	host := ""
	if u != nil {
		host = u.Hostname()
	}
	ms := func(d time.Duration) string { return fmt.Sprintf("%d ms", d.Milliseconds()) }

	var b strings.Builder
	b.WriteString("What was done\n")
	fmt.Fprintf(&b, "1. Asked the proxy (%s) to open a tunnel to %s:443.\n", a.Addr, host)
	if tr.ConnectCode != "" {
		fmt.Fprintf(&b, "   The proxy answered: %s, after %s.\n", tr.ConnectCode, ms(tr.TunnelAt))
	}
	if ev, ok := a.tunnelSince(host, began); ok {
		fmt.Fprintf(&b, "2. How the proxy connected to the site: %s.\n", describeMode(ev.Mode))
	} else {
		b.WriteString("2. How the proxy connected to the site: not recorded.\n")
	}
	if tr.TLSVersion != "" {
		fmt.Fprintf(&b, "3. Secure connection: %s in %s. Certificate for %s, issued by %s.\n", tr.TLSVersion, ms(tr.TLS), tr.Subject, tr.Issuer)
	}
	if tr.Status != 0 {
		fmt.Fprintf(&b, "4. Asked for the home page: HTTP %d (%s), %s in total.\n", tr.Status, tr.Proto, ms(tr.Total))
	}
	if err != nil {
		fmt.Fprintf(&b, "Stopped with: %v\n", err)
	}

	// the same site without the proxy: normal against bypass, plus what the app knows about it
	if host != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		b.WriteString("\nThe same site checked directly, without the proxy\n")
		b.WriteString(a.Diagnose(ctx, host).Text)
	}
	return strings.TrimRight(b.String(), "\n")
}

// tunnelSince finds the newest tunnel to host that the proxy opened since t.
func (a *App) tunnelSince(host string, t time.Time) (stats.Event, bool) {
	for _, e := range a.Stats.Recent(50) {
		if e.Time.Before(t.Add(-time.Second)) {
			break
		}
		if strings.HasPrefix(e.Host, host+":") {
			return e, true
		}
	}
	return stats.Event{}, false
}

// describeMode says in plain words what a connection mode from the proxy means.
func describeMode(m string) string {
	switch m {
	case "bypass":
		return "with the bypass (the first packet was split in two)"
	case "direct":
		return "directly, untouched (no bypass needed)"
	case "auto-bypass":
		return "the normal attempt failed, so it retried with the bypass and that worked"
	case "auto-skip":
		return "the bypass failed, so it retried directly and that worked"
	case "failed":
		return "failed"
	}
	return m
}
