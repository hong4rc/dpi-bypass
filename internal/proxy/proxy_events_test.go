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

type openEvent struct {
	id                 uint64
	client, host, mode string
}

type closeEvent struct {
	id       uint64
	up, down int64
	err      error
}

// observe wires OnOpen/OnClose to channels, handing out sequential ids.
func observe(p *Proxy) (<-chan openEvent, <-chan closeEvent) {
	opens, closes := make(chan openEvent, 8), make(chan closeEvent, 8)
	var next uint64
	p.OnOpen = func(client, host, mode string) uint64 {
		next++
		opens <- openEvent{next, client, host, mode}
		return next
	}
	p.OnClose = func(id uint64, up, down int64, _ time.Duration, err error) {
		closes <- closeEvent{id, up, down, err}
	}
	return opens, closes
}

// A tunnel is reported as soon as it is established, while it is still open,
// and again with its final size when it ends.
func TestConnectionIsReportedWhileOpenAndAgainWhenClosed(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "hi") }))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	p, _, proxyAddr := proxyServer(t, u.Port())
	opens, closes := observe(p)

	tr := srv.Client().Transport.(*http.Transport).Clone()
	tr.Proxy = http.ProxyURL(&url.URL{Scheme: "http", Host: proxyAddr})
	resp, err := (&http.Client{Transport: tr, Timeout: 10 * time.Second}).Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()

	// keep-alive: the tunnel is still open, and must already be counted
	var o openEvent
	select {
	case o = <-opens:
	case <-time.After(5 * time.Second):
		t.Fatal("no OnOpen while the connection is open")
	}
	if o.mode != ModeBypass || o.host != u.Host || !strings.HasPrefix(o.client, "127.0.0.1:") {
		t.Fatalf("unexpected open: %+v", o)
	}
	select {
	case c := <-closes:
		t.Fatalf("OnClose fired while the tunnel is still open: %+v", c)
	case <-time.After(150 * time.Millisecond):
	}

	tr.CloseIdleConnections() // now the tunnel ends
	select {
	case c := <-closes:
		if c.id != o.id || c.up == 0 || c.down == 0 || c.err != nil {
			t.Fatalf("unexpected close: %+v (open id %d)", c, o.id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no OnClose after the tunnel ended")
	}
}

func TestAFailedDialIsReportedAsOpenThenClosedWithTheError(t *testing.T) {
	p, _, proxyAddr := proxyServer(t, "1")
	opens, closes := observe(p)

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
	case o := <-opens:
		if o.mode != ModeFailed || o.host != "127.0.0.1:1" {
			t.Fatalf("unexpected open: %+v", o)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no OnOpen for the failed dial")
	}
	select {
	case cl := <-closes:
		if cl.err == nil {
			t.Fatalf("failed connection must carry its error: %+v", cl)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no OnClose for the failed dial")
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

// host.docker.internal, *.lan, router names, ...: names that resolve to this
// network must reach their server untouched, even when a client sends them to
// the proxy instead of connecting directly.
func TestNamesThatResolveToALocalAddressAreNeverSplit(t *testing.T) {
	var fragmentedSeen int
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	t.Cleanup(func() { ln.Close() })
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		h := make([]byte, 5)
		io.ReadFull(c, h)
		body := make([]byte, int(h[3])<<8|int(h[4]))
		io.ReadFull(c, body)
		if fragmented(append(h, body...)) {
			fragmentedSeen++
		}
		c.Write([]byte("OK"))
	}()
	_, port, _ := net.SplitHostPort(ln.Addr().String())

	bl := blacklist.New(filepath.Join(t.TempDir(), "bl.txt"))
	p := New("127.0.0.1:0", bl)
	p.TLSPorts[port] = true // SplitPrivate stays off, as in the real app
	srv := httptest.NewServer(p)
	t.Cleanup(srv.Close)

	// "localhost" is a name, not an IP literal: only the resolved address says it is local
	c := connectThroughProxy(t, strings.TrimPrefix(srv.URL, "http://"), "localhost:"+port)
	c.Write(fakeHello(200))
	buf := make([]byte, 2)
	io.ReadFull(c, buf)
	if string(buf) != "OK" || fragmentedSeen != 0 {
		t.Fatalf("got %q, fragmented hellos seen: %d (want OK, 0)", buf, fragmentedSeen)
	}
}
