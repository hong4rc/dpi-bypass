package app

import (
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestAllowedClient(t *testing.T) {
	for ip, want := range map[string]bool{
		"192.168.1.20": true, "10.1.2.3": true, "172.16.5.5": true, "127.0.0.1": true, "169.254.1.1": true,
		"8.8.8.8": false, "203.0.113.9": false,
	} {
		if got := allowedClient(&net.TCPAddr{IP: net.ParseIP(ip)}); got != want {
			t.Errorf("allowedClient(%s)=%v want %v", ip, got, want)
		}
	}
}

func TestEnableAndDisableLAN(t *testing.T) {
	if len(lanIPs()) == 0 {
		t.Skip("no private LAN address on this machine")
	}
	dir := t.TempDir()
	a := New(dir, "127.0.0.1:0", "test")

	if err := a.EnableLAN(); err != nil {
		t.Fatal(err)
	}
	urls := a.LANURLs()
	if !a.LANEnabled() || len(urls) == 0 || !a.LANWanted() {
		t.Fatalf("not enabled: enabled=%v urls=%v wanted=%v", a.LANEnabled(), urls, a.LANWanted())
	}

	// another device would fetch exactly this URL; the PAC must name the LAN address, not 127.0.0.1
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Get(urls[0])
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	hostPort := strings.TrimSuffix(strings.TrimPrefix(urls[0], "http://"), "/proxy.pac")
	if !strings.Contains(string(body), "PROXY "+hostPort) || strings.Contains(string(body), "PROXY 127.0.0.1") {
		t.Fatalf("PAC does not point at %s:\n%s", hostPort, body)
	}

	a.DisableLAN()
	if a.LANEnabled() || a.LANWanted() {
		t.Fatal("still enabled after DisableLAN")
	}
	if _, err := os.Stat(a.markerPath()); err == nil {
		t.Fatal("marker file not removed")
	}
	if c, err := net.DialTimeout("tcp", hostPort, time.Second); err == nil {
		c.Close()
		t.Fatal("LAN listener still accepting after DisableLAN")
	}
}
