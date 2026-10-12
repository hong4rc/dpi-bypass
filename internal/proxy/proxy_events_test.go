package proxy

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dpi/internal/blacklist"
)

type connEvent struct {
	client, host, mode string
	up, down           int64
	err                error
}

func TestOnConnReportsABypassedConnection(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "hi") }))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	p, _, proxyAddr := proxyServer(t, u.Port())
	events := make(chan connEvent, 4)
	p.OnConn = func(client, host, mode string, up, down int64, _ time.Duration, err error) {
		events <- connEvent{client, host, mode, up, down, err}
	}

	tr := srv.Client().Transport.(*http.Transport).Clone()
	tr.Proxy = http.ProxyURL(&url.URL{Scheme: "http", Host: proxyAddr})
	resp, err := (&http.Client{Transport: tr, Timeout: 10 * time.Second}).Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()
	tr.CloseIdleConnections() // ends the tunnel so the proxy reports it

	select {
	case e := <-events:
		if e.mode != ModeBypass || e.host != u.Host || e.up == 0 || e.down == 0 || e.err != nil || !strings.HasPrefix(e.client, "127.0.0.1:") {
			t.Fatalf("unexpected event: %+v", e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no OnConn event")
	}
}

func TestOnConnReportsAFailedDial(t *testing.T) {
	p, _, proxyAddr := proxyServer(t, "1")
	events := make(chan connEvent, 1)
	p.OnConn = func(client, host, mode string, up, down int64, _ time.Duration, err error) {
		events <- connEvent{client, host, mode, up, down, err}
	}

	c, err := net.Dial("tcp", proxyAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(10 * time.Second))
	io.WriteString(c, "CONNECT 127.0.0.1:1 HTTP/1.1\r\nHost: 127.0.0.1:1\r\n\r\n")
	if line, _ := bufio.NewReader(c).ReadString('\n'); !strings.Contains(line, "502") {
		t.Fatalf("want 502, got %q", line)
	}
	select {
	case e := <-events:
		if e.mode != ModeFailed || e.err == nil || e.host != "127.0.0.1:1" {
			t.Fatalf("unexpected event: %+v", e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no OnConn event for failed dial")
	}
}

func TestOnClientSeesTheUserAgentOfPACFetchesAndConnects(t *testing.T) {
	p, _, proxyAddr := proxyServer(t, "1")
	type seen struct{ client, ua string }
	got := make(chan seen, 4)
	p.OnClient = func(client, ua string) { got <- seen{client, ua} }

	// PAC fetch like Android's system proxy service does it
	req, _ := http.NewRequest("GET", "http://"+proxyAddr+"/proxy.pac", nil)
	req.Header.Set("User-Agent", "Dalvik/2.1.0 (Linux; U; Android 14; SM-S918B Build/UP1A)")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if s := <-got; !strings.Contains(s.ua, "SM-S918B") || !strings.HasPrefix(s.client, "127.0.0.1:") {
		t.Fatalf("PAC fetch: %+v", s)
	}

	// CONNECT from a browser (the dial fails, but the agent is noted before that)
	c, err := net.Dial("tcp", proxyAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(10 * time.Second))
	io.WriteString(c, "CONNECT 127.0.0.1:1 HTTP/1.1\r\nHost: 127.0.0.1:1\r\nUser-Agent: Mozilla/5.0 (iPhone; CPU iPhone OS 17_2 like Mac OS X)\r\n\r\n")
	bufio.NewReader(c).ReadString('\n')
	if s := <-got; !strings.Contains(s.ua, "iPhone") {
		t.Fatalf("CONNECT: %+v", s)
	}

	// no User-Agent, no callback
	r2, _ := http.NewRequest("GET", "http://"+proxyAddr+"/proxy.pac", nil)
	r2.Header.Set("User-Agent", "")
	if resp, err := http.DefaultClient.Do(r2); err == nil {
		resp.Body.Close()
	}
	select {
	case s := <-got:
		t.Fatalf("unexpected callback without a User-Agent: %+v", s)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestOnPACAndFallbackRouting(t *testing.T) {
	bl := blacklist.New(filepath.Join(t.TempDir(), "bl.txt"))
	p := New("127.0.0.1:18080", bl)
	var pacClients []string
	p.OnPAC = func(client string) { pacClients = append(pacClients, client) }

	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		req.RemoteAddr = "192.168.1.77:4000"
		rec := httptest.NewRecorder()
		p.ServeHTTP(rec, req)
		return rec
	}

	if rec := get("/"); rec.Code != http.StatusNotFound {
		t.Fatalf("no fallback: / should be 404, got %d", rec.Code)
	}

	p.Fallback = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "dashboard") })
	if rec := get("/"); rec.Body.String() != "dashboard" {
		t.Fatalf("/ should reach the fallback, got %q", rec.Body.String())
	}
	if rec := get("/api/state"); rec.Body.String() != "dashboard" {
		t.Fatalf("/api/state should reach the fallback, got %q", rec.Body.String())
	}

	rec := get("/proxy.pac")
	if !strings.Contains(rec.Body.String(), "FindProxyForURL") || rec.Header().Get("Content-Type") != "application/x-ns-proxy-autoconfig" {
		t.Fatalf("PAC wrong: %q (%s)", rec.Body.String(), rec.Header().Get("Content-Type"))
	}
	if len(pacClients) != 1 || pacClients[0] != "192.168.1.77:4000" {
		t.Fatalf("OnPAC clients: %v", pacClients)
	}
}
