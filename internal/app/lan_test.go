package app

import (
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newLANApp is an App whose "LAN addresses" are loopback, so tests never listen on
// the machine's real LAN address: Windows Firewall asks for permission for every
// new test program that does, and each test run is a new program. Set
// DPI_TEST_REAL_LAN=1 to run these tests against the real LAN address instead.
func newLANApp(t *testing.T, dir string) *App {
	t.Helper()
	a := New(dir, "127.0.0.1:0", "test")
	if os.Getenv("DPI_TEST_REAL_LAN") == "" {
		a.LANAddrs = func() []string { return []string{"127.0.0.1"} }
	}
	return a
}

func TestVirtualAdaptersAreSkipped(t *testing.T) {
	for name, want := range map[string]bool{
		"vEthernet (WSL (Hyper-V firewall))": true, "vEthernet (Default Switch)": true, "VMware Network Adapter VMnet8": true,
		"VirtualBox Host-Only Network": true, "docker0": true, "veth1a2b3c": true, "virbr0": true, "br-1234abcd": true,
		"Ethernet": false, "Wi-Fi": false, "eth0": false, "wlan0": false, "en0": false, "Local Area Connection* 10": false,
	} {
		if got := isVirtual(name); got != want {
			t.Errorf("isVirtual(%q)=%v want %v", name, got, want)
		}
	}
}

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
	dir := t.TempDir()
	a := newLANApp(t, dir)

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
	if !strings.Contains(string(body), "PROXY "+hostPort) || (os.Getenv("DPI_TEST_REAL_LAN") != "" && strings.Contains(string(body), "PROXY 127.0.0.1")) {
		t.Fatalf("PAC does not point at %s:\n%s", hostPort, body)
	}

	a.DisableLAN()
	if a.LANEnabled() || a.LANWanted() {
		t.Fatal("still enabled after DisableLAN")
	}
	if c, err := net.DialTimeout("tcp", hostPort, time.Second); err == nil {
		c.Close()
		t.Fatal("LAN listener still accepting after DisableLAN")
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "settings.json")); !strings.Contains(string(data), `"lanSharing": false`) {
		t.Fatalf("settings.json should say sharing is off: %q", data)
	}
}

func TestLANSettingSurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	a := newLANApp(t, dir)
	if a.LANWanted() {
		t.Fatal("sharing must start off")
	}
	if err := a.EnableLAN(); err != nil {
		t.Fatal(err)
	}
	defer a.DisableLAN()

	if !New(dir, "127.0.0.1:0", "test").LANWanted() {
		t.Fatal("a new run must see that sharing was left on")
	}
}

func TestIdentifyPageComesAndGoesWithSharing(t *testing.T) {
	a := newLANApp(t, t.TempDir())
	if len(a.IdentifyURLs()) != 0 {
		t.Fatal("no identify page before sharing is on")
	}
	if err := a.EnableLAN(); err != nil {
		t.Fatal(err)
	}
	urls := a.IdentifyURLs()
	if len(urls) == 0 || !strings.HasPrefix(urls[0], "https://") || !strings.HasSuffix(urls[0], "/identify") {
		t.Fatalf("identify urls: %v", urls)
	}

	phone := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} // like tapping "Proceed"
	resp, err := phone.Get(urls[0])
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(page), "getHighEntropyValues") {
		t.Fatal("not the identify page")
	}

	report := `{"model":"SHARK PAR-A0","platform":"Android","platformVersion":"12.0.0","mobile":true,"userAgent":"Mozilla/5.0 (Linux; Android 10; K) Chrome/154"}`
	resp, err = phone.Post(strings.TrimSuffix(urls[0], "/identify")+"/identify/report", "application/json", strings.NewReader(report))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("report got %d", resp.StatusCode)
	}
	var labelled bool
	for _, d := range a.Stats.Devices() {
		for _, id := range d.Identities {
			labelled = labelled || id.Label == "SHARK PAR-A0 · Android 12"
		}
	}
	if !labelled {
		t.Fatalf("the device was not identified: %+v", a.Stats.Devices())
	}
	if data, _ := os.ReadFile(filepath.Join(a.Dir, "devices.json")); !strings.Contains(string(data), "SHARK PAR-A0") {
		t.Fatalf("identity not saved to devices.json: %q", data)
	}

	a.DisableLAN()
	if len(a.IdentifyURLs()) != 0 {
		t.Fatal("identify page must stop with sharing")
	}
	if _, err := phone.Get(urls[0]); err == nil {
		t.Fatal("identify listener still accepting after DisableLAN")
	}
}

func TestLANChangesSignalTheTray(t *testing.T) {
	a := newLANApp(t, t.TempDir())
	pending := func() int { return len(a.changed) }

	if err := a.EnableLAN(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-a.Changed():
	default:
		t.Fatal("EnableLAN did not signal")
	}

	a.DisableLAN()
	select {
	case <-a.Changed():
	default:
		t.Fatal("DisableLAN did not signal")
	}

	// two changes nobody read collapse into one pending signal
	if err := a.EnableLAN(); err != nil {
		t.Fatal(err)
	}
	a.DisableLAN()
	if pending() != 1 {
		t.Fatalf("want 1 pending signal, got %d", pending())
	}
}

func TestAddressChangeSignalsTheTray(t *testing.T) {
	a := newLANApp(t, t.TempDir())
	if err := a.EnableLAN(); err != nil {
		t.Fatal(err)
	}
	defer a.DisableLAN()
	<-a.Changed()

	a.LANAddrs = func() []string { return []string{"127.0.0.1", "127.0.0.2"} }
	a.refreshLAN()
	select {
	case <-a.Changed():
	default:
		t.Fatal("refreshLAN moving to a new address did not signal")
	}
}

func TestFailedAddressChangeStillSignalsTheTray(t *testing.T) {
	a := newLANApp(t, t.TempDir())
	if err := a.EnableLAN(); err != nil {
		t.Fatal(err)
	}
	<-a.Changed()

	a.LANAddrs = func() []string { return []string{"192.0.2.1"} } // not this PC's address: cannot bind
	a.refreshLAN()
	if a.LANEnabled() {
		t.Fatal("sharing should have stopped when the new address cannot be used")
	}
	select {
	case <-a.Changed():
	default:
		t.Fatal("sharing stopped but the tray was not told")
	}
}
