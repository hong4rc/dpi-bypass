package app

import (
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"dpi/internal/identify"
)

// The whole chain, as a phone would drive it: ask the identify page whether the
// phone uses the proxy, send the probe through the proxy, ask again.
func TestTheIdentifyPageLearnsWhetherThePhoneUsesTheProxy(t *testing.T) {
	a := newLANApp(t, t.TempDir())
	if err := a.EnableLAN(); err != nil {
		t.Fatal(err)
	}
	defer a.DisableLAN()

	pacURL := a.LANURLs()[0]
	proxyAddr := strings.TrimSuffix(strings.TrimPrefix(pacURL, "http://"), "/proxy.pac")
	identifyURL := a.IdentifyURLs()[0]
	statusURL := strings.TrimSuffix(identifyURL, "/identify") + "/identify/status"

	phone := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	status := func() (using bool, pac string) {
		t.Helper()
		resp, err := phone.Get(statusURL)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out struct {
			Checked    bool
			UsingProxy bool
			Pac        string
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || !out.Checked {
			t.Fatalf("status: %+v err=%v", out, err)
		}
		return out.UsingProxy, out.Pac
	}

	// 1. no probe has come through the proxy yet: not (known to be) using it, and the setup address is offered
	using, pac := status()
	if using {
		t.Fatal("a phone whose probe never reached the proxy must not be reported as using it")
	}
	if pac != pacURL {
		t.Fatalf("the setup address must be the one for the address the phone reached us on: %q, want %q", pac, pacURL)
	}

	// 2. the phone's browser sends the page's probe request through the proxy (a CONNECT)
	c, err := net.Dial("tcp", proxyAddr)
	if err != nil {
		t.Fatal(err)
	}
	c.SetDeadline(time.Now().Add(5 * time.Second))
	io.WriteString(c, "CONNECT "+identify.ProbeHost+":443 HTTP/1.1\r\nHost: "+identify.ProbeHost+":443\r\n\r\n")
	buf := make([]byte, 64)
	n, _ := c.Read(buf)
	c.Close()
	if !strings.Contains(string(buf[:n]), "403") {
		t.Fatalf("the proxy answered the probe with %q", buf[:n])
	}

	// 3. now it is reported as using the proxy
	if using, _ := status(); !using {
		t.Fatal("after its probe reached the proxy the phone must be reported as using it")
	}

	// the probe is not a connection: it must not appear in the activity list or the counters
	if tot := a.Stats.Totals(); tot.Conns != 0 {
		t.Fatalf("the probe was counted as a connection: %+v", tot)
	}
}

func TestThePageIsToldTheSetupAddressOnlyForAddressesTheAppShares(t *testing.T) {
	a := newLANApp(t, t.TempDir())
	if err := a.EnableLAN(); err != nil {
		t.Fatal(err)
	}
	defer a.DisableLAN()
	if got := a.pacFor("203.0.113.9"); got != "" {
		t.Fatalf("an address the app does not share on must get no setup address, got %q", got)
	}
	if got := a.pacFor("127.0.0.1"); got == "" || !strings.HasSuffix(got, "/proxy.pac") {
		t.Fatalf("a shared address must get its setup address, got %q", got)
	}
}
