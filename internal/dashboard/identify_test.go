package dashboard

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/skip2/go-qrcode"

	"dpi/internal/app"
)

func sharing(t *testing.T) (http.Handler, *app.App) {
	t.Helper()
	h, a := newHandler(t)
	if os.Getenv("DPI_TEST_REAL_LAN") == "" { // loopback: a test that listens on the real LAN address makes Windows Firewall ask permission on every run
		a.LANAddrs = func() []string { return []string{"127.0.0.1"} }
	}
	if err := a.EnableLAN(); err != nil {
		t.Skipf("no LAN address to share on: %v", err)
	}
	t.Cleanup(a.DisableLAN)
	return h, a
}

type setupReply struct {
	Enabled bool
	Items   []struct {
		IP                string
		Primary           bool
		PAC               string
		PACReachable      bool
		Identify          string
		IdentifyReachable bool
	}
}

func TestPhoneSetupListsEveryAddressAndVerifiesThemAll(t *testing.T) {
	h, a := sharing(t)
	var out setupReply
	if err := json.Unmarshal(get(h, "/api/phone-setup").Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Enabled || len(out.Items) != len(a.IdentifyURLs()) || len(out.Items) == 0 {
		t.Fatalf("items: %+v", out)
	}
	for i, it := range out.Items {
		if !strings.HasPrefix(it.PAC, "http://"+it.IP+":") || !strings.HasSuffix(it.PAC, "/proxy.pac") {
			t.Errorf("PAC address and ip disagree: %+v", it)
		}
		if !strings.HasPrefix(it.Identify, "https://"+it.IP+":") || !strings.HasSuffix(it.Identify, "/identify") {
			t.Errorf("identify address and ip disagree: %+v", it)
		}
		if !it.PACReachable {
			t.Errorf("the PC could not fetch its own proxy setup at %s", it.PAC)
		}
		if !it.IdentifyReachable {
			t.Errorf("the PC could not fetch its own identify page at %s", it.Identify)
		}
		if it.Primary != (i == 0) {
			t.Errorf("only the first (default-route) address is primary: %+v", it)
		}
	}
}

func TestPhoneSetupIsEmptyWhileSharingIsOff(t *testing.T) {
	h, _ := newHandler(t)
	var out setupReply
	json.Unmarshal(get(h, "/api/phone-setup").Body.Bytes(), &out)
	if out.Enabled || out.Items == nil || len(out.Items) != 0 {
		t.Fatalf("want enabled=false and an empty (non-null) list, got %+v", out)
	}
}

func TestAnUnreachableAddressIsReportedAsSuch(t *testing.T) {
	if probePAC("http://127.0.0.1:1/proxy.pac") || probeIdentify("https://127.0.0.1:1/identify") {
		t.Fatal("a dead address must not be reported as reachable")
	}
}

func TestQROnlyForAddressesOfThisPC(t *testing.T) {
	h, a := sharing(t)
	for _, good := range []string{a.IdentifyURLs()[0], a.LANURLs()[0]} { // identify page and proxy setup
		rec := get(h, "/api/qr.svg?url="+url.QueryEscape(good))
		if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/svg+xml" || !strings.HasPrefix(rec.Body.String(), "<svg") {
			t.Fatalf("qr for %s: %d %s", good, rec.Code, rec.Header().Get("Content-Type"))
		}
	}
	for _, bad := range []string{"https://evil.example.com/identify", "http://192.168.1.99:18080/proxy.pac", "", a.IdentifyURLs()[0] + "x"} {
		if rec := get(h, "/api/qr.svg?url="+url.QueryEscape(bad)); rec.Code != http.StatusNotFound {
			t.Errorf("QR for %q should be refused, got %d", bad, rec.Code)
		}
	}
	// the dashboard guard still applies
	for _, path := range []string{"/api/phone-setup", "/api/qr.svg?url=x"} {
		if rec := do(h, req{method: "GET", path: path, remote: "192.168.1.77:4000"}); rec.Code != http.StatusForbidden {
			t.Errorf("a LAN client must not reach %s, got %d", path, rec.Code)
		}
	}
}

// The SVG must encode exactly the modules of the QR code: parse it back and compare.
func TestQRSVGMatchesTheQRCodeModuleForModule(t *testing.T) {
	text := "https://192.168.1.10:18443/identify"
	svg, err := qrSVG(text)
	if err != nil {
		t.Fatal(err)
	}
	q, _ := qrcode.New(text, qrcode.Medium)
	want := q.Bitmap()
	n := len(want)
	if !strings.Contains(svg, `viewBox="0 0 `+strconv.Itoa(n)+" "+strconv.Itoa(n)+`"`) {
		t.Fatalf("viewBox does not match a %dx%d code", n, n)
	}

	got := make([][]bool, n)
	for i := range got {
		got[i] = make([]bool, n)
	}
	runs := regexp.MustCompile(`M(\d+) (\d+)h(\d+)v1h-(\d+)z`).FindAllStringSubmatch(svg, -1)
	for _, m := range runs {
		x, _ := strconv.Atoi(m[1])
		y, _ := strconv.Atoi(m[2])
		w, _ := strconv.Atoi(m[3])
		for i := 0; i < w; i++ {
			got[y][x+i] = true
		}
	}
	for y := range want {
		for x := range want[y] {
			if got[y][x] != want[y][x] {
				t.Fatalf("module (%d,%d) differs: svg=%v qr=%v", x, y, got[y][x], want[y][x])
			}
		}
	}
	if !strings.Contains(svg, `fill="#fff"`) || !strings.Contains(svg, `fill="#000"`) {
		t.Fatal("QR must be black on white whatever the page theme")
	}
}

func TestHostOfExtractsTheIP(t *testing.T) {
	for in, want := range map[string]string{
		"https://192.168.1.10:18443/identify": "192.168.1.10",
		"http://192.168.1.10:18080/proxy.pac": "192.168.1.10",
		"http://[fe80::1]:18080/proxy.pac":    "fe80::1",
	} {
		if got := hostOf(in); got != want {
			t.Errorf("hostOf(%q)=%q want %q", in, got, want)
		}
	}
}
