package app

import (
	"strings"
	"testing"

	"dpi/internal/settings"
)

// The PC's address can change while the app runs for months (the router hands out a
// new one, Wi-Fi reconnects, the app started before the network was up). Sharing must
// follow, but only while the user wants sharing.
func TestSharingFollowsTheAddressesOfThePC(t *testing.T) {
	a := newLANApp(t, t.TempDir())
	addrs := []string{} // the network is not up yet
	a.LANAddrs = func() []string { return addrs }
	a.Settings.Update(func(s *settings.Settings) { s.LANSharing = true }) // sharing was left on last time

	if err := a.EnableLAN(); err == nil {
		t.Fatal("with no address there is nothing to share on")
	}
	a.refreshLAN()
	if a.LANEnabled() {
		t.Fatal("still no address: nothing can be shared")
	}

	addrs = []string{"127.0.0.1"} // the network comes up
	a.refreshLAN()
	if !a.LANEnabled() || !strings.Contains(strings.Join(a.LANURLs(), " "), "127.0.0.1:") {
		t.Fatalf("sharing did not start when the network came up: %v", a.LANURLs())
	}
	if !strings.Contains(strings.Join(a.IdentifyURLs(), " "), "127.0.0.1:") {
		t.Fatalf("the identify page did not follow: %v", a.IdentifyURLs())
	}
	before := a.LANURLs()
	a.refreshLAN()
	if strings.Join(a.LANURLs(), " ") != strings.Join(before, " ") {
		t.Fatal("nothing changed, so the listeners must be left alone")
	}

	addrs = []string{"127.0.0.2"} // the router gave the PC another address
	a.refreshLAN()
	urls := strings.Join(a.LANURLs(), " ")
	if !strings.Contains(urls, "127.0.0.2:") || strings.Contains(urls, "127.0.0.1:") {
		t.Fatalf("sharing did not move to the new address: %v", a.LANURLs())
	}
	if !strings.Contains(strings.Join(a.IdentifyURLs(), " "), "127.0.0.2:") {
		t.Fatalf("the identify page did not move: %v", a.IdentifyURLs())
	}
	if !a.LANWanted() {
		t.Fatal("following an address change must not change the saved choice")
	}

	a.DisableLAN() // the user turns sharing off: it must stay off
	addrs = []string{"127.0.0.1"}
	a.refreshLAN()
	if a.LANEnabled() {
		t.Fatal("sharing the user turned off must never come back by itself")
	}
}
