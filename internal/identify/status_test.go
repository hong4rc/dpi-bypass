package identify

import (
	"encoding/json"
	"strings"
	"testing"

	"dpi/internal/stats"
)

func noReport(string, stats.Identity) error { return nil }

type statusReply struct {
	Checked    bool
	UsingProxy bool
	Pac        string
}

func getStatus(t *testing.T, status StatusFunc) (statusReply, string) {
	t.Helper()
	srv, c := startWith(t, noReport, status)
	code, body := send(t, c, "GET", srv.URL+"/identify/status", "", nil)
	if code != 200 {
		t.Fatalf("status endpoint got %d", code)
	}
	var out statusReply
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	return out, body
}

func TestStatusSaysWhetherThePhoneUsesTheProxyAndGivesTheSetupAddress(t *testing.T) {
	var gotClient, gotHost string
	out, _ := getStatus(t, func(client, host string) (bool, string) {
		gotClient, gotHost = client, host
		return true, "http://" + host + ":18080/proxy.pac"
	})
	if !out.Checked || !out.UsingProxy || out.Pac == "" || !strings.HasSuffix(out.Pac, ":18080/proxy.pac") {
		t.Fatalf("status: %+v", out)
	}
	if !strings.HasPrefix(gotClient, "127.0.0.1:") {
		t.Errorf("the check must be about the connecting client, got %q", gotClient)
	}
	if gotHost != "127.0.0.1" {
		t.Errorf("the host must be the address the client used (no port), got %q", gotHost)
	}

	out, _ = getStatus(t, func(string, string) (bool, string) { return false, "http://pc:18080/proxy.pac" })
	if !out.Checked || out.UsingProxy || out.Pac == "" {
		t.Fatalf("a phone that did not probe: %+v", out)
	}
}

// Without a status function the page can not detect anything, and says so.
func TestStatusWithoutADetectorSaysItCouldNotCheck(t *testing.T) {
	out, body := getStatus(t, nil)
	if out.Checked || out.UsingProxy || out.Pac != "" {
		t.Fatalf("status: %+v (%s)", out, body)
	}
}

func TestThePageProbesTheMadeUpNameAndOffersTheAddressWhenItIsNotInUse(t *testing.T) {
	srv, c := startWith(t, noReport, nil)
	_, page := send(t, c, "GET", srv.URL+"/identify", "", nil)

	if strings.Contains(page, "{{") {
		t.Fatalf("an unfilled placeholder is left in the page")
	}
	if !strings.Contains(page, "https://"+ProbeHost+"/") {
		t.Fatal("the page must request the probe name")
	}
	for _, want := range []string{"/identify/status", "Copy proxy address", "is using the proxy", "does not seem to use the proxy", "Could not check"} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	// the probe name must be one that can never be a real site
	if !strings.HasSuffix(ProbeHost, ".invalid") {
		t.Errorf("probe host %q must use the reserved .invalid TLD", ProbeHost)
	}
}
