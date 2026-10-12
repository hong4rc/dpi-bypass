package dashboard

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"dpi/internal/app"
	"dpi/internal/logx"
	"dpi/internal/stats"
)

func statsEvent(client, host, mode string) stats.Event {
	return stats.Event{Time: time.Now(), Client: client, Host: host, Mode: mode}
}

func newHandler(t *testing.T) (http.Handler, *app.App) {
	t.Helper()
	a := app.New(t.TempDir(), "127.0.0.1:18080", "test")
	return New(a), a
}

type req struct {
	method, path, body string
	remote, host       string
	headers            map[string]string
}

func do(h http.Handler, q req) *httptest.ResponseRecorder {
	r := httptest.NewRequest(q.method, q.path, strings.NewReader(q.body))
	r.RemoteAddr = "127.0.0.1:50000"
	if q.remote != "" {
		r.RemoteAddr = q.remote
	}
	r.Host = "127.0.0.1:18080"
	if q.host != "" {
		r.Host = q.host
	}
	for k, v := range q.headers {
		r.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func get(h http.Handler, path string) *httptest.ResponseRecorder {
	return do(h, req{method: "GET", path: path})
}

func post(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	return do(h, req{method: method, path: path, body: body, headers: map[string]string{"X-Requested-With": "dpi"}})
}

func TestOnlyThisPCCanOpenTheDashboard(t *testing.T) {
	h, _ := newHandler(t)
	for _, path := range []string{"/", "/api/state", "/api/devices", "/api/logs", "/api/skip"} {
		if rec := do(h, req{method: "GET", path: path, remote: "192.168.1.77:4000"}); rec.Code != http.StatusForbidden {
			t.Errorf("LAN client got %d for %s, want 403", rec.Code, path)
		}
		if rec := do(h, req{method: "GET", path: path, remote: "8.8.8.8:4000"}); rec.Code != http.StatusForbidden {
			t.Errorf("public client got %d for %s, want 403", rec.Code, path)
		}
	}
	if rec := get(h, "/api/state"); rec.Code != http.StatusOK {
		t.Fatalf("this PC got %d", rec.Code)
	}
	if rec := do(h, req{method: "GET", path: "/api/state", remote: "[::1]:4000", host: "localhost:18080"}); rec.Code != http.StatusOK {
		t.Fatalf("IPv6 loopback + localhost got %d", rec.Code)
	}
}

func TestDNSRebindingHostIsRejected(t *testing.T) {
	h, _ := newHandler(t)
	for _, host := range []string{"evil.example.com", "evil.example.com:18080", "192.168.1.10:18080"} {
		if rec := do(h, req{method: "GET", path: "/api/state", host: host}); rec.Code != http.StatusForbidden {
			t.Errorf("Host %q got %d, want 403", host, rec.Code)
		}
	}
}

func TestChangesNeedTheCustomHeaderAndSameOrigin(t *testing.T) {
	h, a := newHandler(t)
	body := `{"host":"csrf.example.org"}`

	if rec := do(h, req{method: "POST", path: "/api/skip", body: body}); rec.Code != http.StatusForbidden {
		t.Errorf("POST without header got %d, want 403", rec.Code)
	}
	cross := req{method: "POST", path: "/api/skip", body: body, headers: map[string]string{"X-Requested-With": "dpi", "Origin": "http://evil.example.com"}}
	if rec := do(h, cross); rec.Code != http.StatusForbidden {
		t.Errorf("cross-origin POST got %d, want 403", rec.Code)
	}
	if a.BL.Match("csrf.example.org") {
		t.Fatal("a blocked request still changed the skip list")
	}
	same := req{method: "POST", path: "/api/skip", body: body, headers: map[string]string{"X-Requested-With": "dpi", "Origin": "http://127.0.0.1:18080"}}
	if rec := do(h, same); rec.Code != http.StatusOK {
		t.Errorf("same-origin POST got %d: %s", rec.Code, rec.Body)
	}
}

func TestIndexAndState(t *testing.T) {
	h, _ := newHandler(t)
	rec := get(h, "/")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "<title>DPI bypass</title>") || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("index: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if rec := get(h, "/nope"); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown path got %d", rec.Code)
	}

	var s struct {
		Version string
		On      bool
		LAN     struct {
			Enabled bool
			URLs    []string
		}
		Addr string
	}
	if err := json.Unmarshal(get(h, "/api/state").Body.Bytes(), &s); err != nil {
		t.Fatal(err)
	}
	if s.Version != "test" || s.On || s.LAN.Enabled || s.LAN.URLs == nil || s.Addr != "127.0.0.1:18080" {
		t.Fatalf("state: %+v", s)
	}
}

func TestSkipListAPI(t *testing.T) {
	h, a := newHandler(t)

	rec := post(h, "POST", "/api/skip", `{"host":"  HTTPS://Game.Example.org:443/login?x=1 "}`)
	if rec.Code != 200 || !a.BL.Match("game.example.org") {
		t.Fatalf("add: %d %s", rec.Code, rec.Body)
	}
	var entries []struct{ Host, Note string }
	json.Unmarshal(get(h, "/api/skip").Body.Bytes(), &entries)
	found := false
	for _, e := range entries {
		if e.Host == "game.example.org" && e.Note == "added in dashboard" {
			found = true
		}
	}
	if !found {
		t.Fatalf("entry missing or note wrong: %+v", entries)
	}

	if rec := post(h, "DELETE", "/api/skip?host=game.example.org", ""); rec.Code != 200 || a.BL.Match("game.example.org") {
		t.Fatalf("remove: %d %s", rec.Code, rec.Body)
	}
	if !a.BL.Match("riotgames.com") {
		t.Fatal("removing one domain removed another")
	}
	if rec := post(h, "POST", "/api/skip", `{"host":"   "}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty host got %d", rec.Code)
	}
	if rec := post(h, "POST", "/api/skip", `not json`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json got %d", rec.Code)
	}
}

func TestDevicesAndNaming(t *testing.T) {
	h, a := newHandler(t)
	a.Stats.SeenPAC("192.168.1.50")

	if rec := post(h, "POST", "/api/devices/name", `{"ip":"192.168.1.50","name":"My Android"}`); rec.Code != 200 {
		t.Fatalf("name: %d %s", rec.Code, rec.Body)
	}
	var devs []struct{ IP, Name string }
	json.Unmarshal(get(h, "/api/devices").Body.Bytes(), &devs)
	if len(devs) != 1 || devs[0].Name != "My Android" {
		t.Fatalf("devices: %+v", devs)
	}
	if rec := post(h, "POST", "/api/devices/name", `{"ip":"not-an-ip","name":"x"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad ip got %d", rec.Code)
	}
	if rec := post(h, "POST", "/api/devices/name", `{"ip":"192.168.1.50","name":"`+strings.Repeat("x", 61)+`"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("long name got %d", rec.Code)
	}
}

func TestConnectionsAndLogsEndpoints(t *testing.T) {
	h, a := newHandler(t)
	a.Stats.AddConn(statsEvent("192.168.1.50", "a.example:443", "bypass"))
	a.Stats.AddConn(statsEvent("192.168.1.50", "b.example:443", "failed"))
	logx.Info.Println("dashboard-test-marker")

	var ev []struct{ Host, Mode string }
	json.Unmarshal(get(h, "/api/connections?limit=1").Body.Bytes(), &ev)
	if len(ev) != 1 || ev[0].Host != "b.example:443" || ev[0].Mode != "failed" {
		t.Fatalf("connections (newest first, limited): %+v", ev)
	}

	var lines []struct{ Level, Text string }
	json.Unmarshal(get(h, "/api/logs").Body.Bytes(), &lines)
	ok := false
	for _, l := range lines {
		if l.Level == "info" && strings.Contains(l.Text, "dashboard-test-marker") {
			ok = true
		}
	}
	if !ok {
		t.Fatalf("log line missing from /api/logs (%d lines)", len(lines))
	}
}

func TestLogStreamDeliversNewLines(t *testing.T) {
	h, _ := newHandler(t)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/api/logs/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("content type %q", resp.Header.Get("Content-Type"))
	}

	go func() {
		for i := 0; i < 20; i++ { // the subscription starts a moment after the headers
			logx.Info.Println("live-stream-marker")
			time.Sleep(50 * time.Millisecond)
		}
	}()
	got := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if strings.HasPrefix(sc.Text(), "data: ") && strings.Contains(sc.Text(), "live-stream-marker") {
				got <- sc.Text()
				return
			}
		}
	}()
	select {
	case line := <-got:
		var l struct {
			Seq         int64
			Level, Text string
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &l); err != nil || l.Level != "info" || l.Seq == 0 {
			t.Fatalf("bad event %q: %v", line, err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no log line streamed")
	}
}

func TestClearLogsEmptiesTheLogButKeepsLogging(t *testing.T) {
	h, _ := newHandler(t)
	logx.Info.Println("old-line-before-clear")
	if rec := do(h, req{method: "POST", path: "/api/logs/clear"}); rec.Code != http.StatusForbidden {
		t.Fatalf("clear without the header got %d, want 403", rec.Code)
	}
	if rec := post(h, "POST", "/api/logs/clear", ""); rec.Code != 200 {
		t.Fatalf("clear: %d", rec.Code)
	}
	var lines []struct{ Text string }
	json.Unmarshal(get(h, "/api/logs").Body.Bytes(), &lines)
	for _, l := range lines {
		if strings.Contains(l.Text, "old-line-before-clear") {
			t.Fatalf("old line survived the clear: %+v", lines)
		}
	}
	if len(lines) != 1 || !strings.Contains(lines[0].Text, "log cleared") {
		t.Fatalf("expected only the 'log cleared' note, got %+v", lines)
	}
}

// The real wiring from main: one port serves the dashboard, the PAC file and CONNECT.
func TestDashboardAndProxyAddressShareOnePort(t *testing.T) {
	a := app.New(t.TempDir(), "127.0.0.1:18080", "test")
	a.Proxy.Fallback = New(a)
	srv := httptest.NewServer(a.Proxy)
	t.Cleanup(srv.Close)

	body := func(path string) (int, string, string) {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, resp.Header.Get("Content-Type"), string(b)
	}
	if code, ct, b := body("/"); code != 200 || !strings.HasPrefix(ct, "text/html") || !strings.Contains(b, "<title>DPI bypass</title>") {
		t.Fatalf("dashboard on the proxy port: %d %s", code, ct)
	}
	if code, ct, b := body("/proxy.pac"); code != 200 || ct != "application/x-ns-proxy-autoconfig" || !strings.Contains(b, "FindProxyForURL") {
		t.Fatalf("PAC on the proxy port: %d %s", code, ct)
	}
	if code, _, _ := body("/api/state"); code != 200 {
		t.Fatalf("API on the proxy port: %d", code)
	}
	// the PAC fetch counted as a device and its agent was noted
	req, _ := http.NewRequest("GET", srv.URL+"/proxy.pac", nil)
	req.Header.Set("User-Agent", "Dalvik/2.1.0 (Linux; U; Android 14; SM-S918B Build/UP1A)")
	if resp, err := http.DefaultClient.Do(req); err == nil {
		resp.Body.Close()
	}
	devs := a.Stats.Devices()
	if len(devs) == 0 || len(devs[0].Agents) == 0 || devs[0].Agents[0] != "Android 14 · SM-S918B" {
		t.Fatalf("device not described from the PAC fetch: %+v", devs)
	}
}

// Quit and start-with-Windows are tray-only: the dashboard must not expose them.
func TestDashboardHasNoQuitOrAutostartEndpoints(t *testing.T) {
	h, a := newHandler(t)
	for _, path := range []string{"/api/quit", "/api/autostart"} {
		if rec := post(h, "POST", path, `{"on":true}`); rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s should not exist, got %d", path, rec.Code)
		}
	}
	select {
	case <-a.Done():
		t.Fatal("the app quit although no quit endpoint exists")
	default:
	}
	if strings.Contains(get(h, "/").Body.String(), "autoToggle") || strings.Contains(get(h, "/").Body.String(), "quitBtn") {
		t.Error("the page still has quit / autostart controls")
	}
}

func TestCleanHost(t *testing.T) {
	for in, want := range map[string]string{
		"Example.COM":                  "example.com",
		" https://a.b.c:8443/x?y=1#z ": "a.b.c",
		"host:443":                     "host",
		"[::1]:443":                    "::1",
		"192.168.1.1":                  "192.168.1.1",
		"example.com.":                 "example.com",
		"":                             "",
	} {
		if got := CleanHost(in); got != want {
			t.Errorf("CleanHost(%q)=%q want %q", in, got, want)
		}
	}
}
