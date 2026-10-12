package proxy

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"dpi/internal/blacklist"
)

// fakeHello builds a TLS handshake record whose ClientHello length field is consistent.
func fakeHello(bodyLen int) []byte {
	body := make([]byte, bodyLen)
	body[0] = 0x01 // ClientHello
	hl := bodyLen - 4
	body[1], body[2], body[3] = byte(hl>>16), byte(hl>>8), byte(hl)
	return append([]byte{0x16, 0x03, 0x01, byte(bodyLen >> 8), byte(bodyLen)}, body...)
}

// fragmented reports whether the record holds less than the full handshake message.
func fragmented(hello []byte) bool {
	body := hello[5:]
	return 4+(int(body[1])<<16|int(body[2])<<8|int(body[3])) > len(body)
}

func TestWriteSplitMakesTwoRecordsWithSamePayload(t *testing.T) {
	hello := fakeHello(300)
	a, b := net.Pipe()
	got := make(chan []byte)
	go func() { d, _ := io.ReadAll(b); got <- d }()
	if err := writeSplit(a, hello); err != nil {
		t.Fatal(err)
	}
	a.Close()
	raw := <-got

	var payload []byte
	records := 0
	for len(raw) > 0 {
		n := int(raw[3])<<8 | int(raw[4])
		payload = append(payload, raw[5:5+n]...)
		raw = raw[5+n:]
		records++
	}
	if records != 2 || !bytes.Equal(payload, hello[5:]) {
		t.Fatalf("records=%d, payload equal=%v", records, bytes.Equal(payload, hello[5:]))
	}
}

func TestReadHelloKeepsNonTLSIntact(t *testing.T) {
	a, b := net.Pipe()
	go func() { a.Write([]byte("GET /")); a.Close() }()
	got, err := readHello(b)
	if err != nil || string(got) != "GET /" {
		t.Fatalf("got %q err %v", got, err)
	}
}

func TestPACSendsBlacklistDirect(t *testing.T) {
	bl := blacklist.New(filepath.Join(t.TempDir(), "bl.txt"))
	rec := httptest.NewRecorder()
	New("127.0.0.1:18080", bl).ServeHTTP(rec, httptest.NewRequest("GET", "/proxy.pac", nil))
	body := rec.Body.String()
	for _, want := range []string{`"riotgames.com"`, "PROXY 127.0.0.1:18080", `return "DIRECT"`, "isPlainHostName", `isInNet(h,"192.168.0.0"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("PAC missing %q:\n%s", want, body)
		}
	}
}

// proxyServer starts the proxy; tlsPort is added to the ports it treats as TLS.
func proxyServer(t *testing.T, tlsPort string) (*Proxy, *blacklist.List, string) {
	t.Helper()
	bl := blacklist.New(filepath.Join(t.TempDir(), "bl.txt"))
	p := New("127.0.0.1:0", bl)
	p.TLSPorts[tlsPort] = true
	p.SplitPrivate = true // the fake servers live on loopback
	srv := httptest.NewServer(p)
	t.Cleanup(srv.Close)
	return p, bl, strings.TrimPrefix(srv.URL, "http://")
}

// connectThroughProxy opens a CONNECT tunnel and returns the raw conn.
func connectThroughProxy(t *testing.T, proxyAddr, target string) net.Conn {
	t.Helper()
	c, err := net.Dial("tcp", proxyAddr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	c.SetDeadline(time.Now().Add(15 * time.Second))
	io.WriteString(c, "CONNECT "+target+" HTTP/1.1\r\nHost: "+target+"\r\n\r\n")
	br := bufio.NewReader(c)
	line, _ := br.ReadString('\n')
	if !strings.Contains(line, "200") {
		t.Fatalf("CONNECT failed: %q", line)
	}
	br.ReadString('\n') // blank line
	return c
}

// A server that resets fragmented hellos must still work: the proxy retries
// unsplit and remembers the host.
func TestAutoSkipWhenSplitBreaksServer(t *testing.T) {
	testAutoSkip(t, nil)
}

// Embedded servers (routers) often answer a fragmented hello with a TLS alert
// instead of closing: that must trigger the same retry.
func TestAutoSkipWhenSplitGetsTLSAlert(t *testing.T) {
	testAutoSkip(t, []byte{0x15, 0x03, 0x03, 0x00, 0x02, 0x02, 0x28})
}

// fragReply is what the fake server sends for a fragmented hello (nil = just close).
func testAutoSkip(t *testing.T, fragReply []byte) {
	var fragmentedSeen, plainSeen atomic.Int32
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				h := make([]byte, 5)
				if _, err := io.ReadFull(c, h); err != nil {
					return
				}
				body := make([]byte, int(h[3])<<8|int(h[4]))
				io.ReadFull(c, body)
				if fragmented(append(h, body...)) {
					fragmentedSeen.Add(1)
					c.Write(fragReply) // nil: close without a reply, like a server that rejects fragments
					return
				}
				plainSeen.Add(1)
				c.Write([]byte("OK"))
			}()
		}
	}()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	p, bl, proxyAddr := proxyServer(t, port)
	skipped := make(chan string, 1)
	p.OnSkip = func(h string) { skipped <- h }

	exchange := func() string {
		c := connectThroughProxy(t, proxyAddr, ln.Addr().String())
		c.Write(fakeHello(200))
		buf := make([]byte, 2)
		io.ReadFull(c, buf)
		return string(buf)
	}

	if got := exchange(); got != "OK" {
		t.Fatalf("first connection: got %q, want OK via unsplit retry", got)
	}
	if h := <-skipped; h != "127.0.0.1" || !bl.Match("127.0.0.1") {
		t.Fatalf("host not learned: %q", h)
	}
	if got := exchange(); got != "OK" {
		t.Fatalf("second connection: got %q", got)
	}
	if fragmentedSeen.Load() != 1 {
		t.Fatalf("learned host was split again (fragmented hellos seen: %d)", fragmentedSeen.Load())
	}
}

// A normal TLS server accepts the fragmented hello end to end.
func TestSplitHelloCompletesRealTLSHandshake(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "hi") }))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	_, _, proxyAddr := proxyServer(t, u.Port())

	tr := srv.Client().Transport.(*http.Transport).Clone()
	tr.Proxy = http.ProxyURL(&url.URL{Scheme: "http", Host: proxyAddr})
	resp, err := (&http.Client{Transport: tr, Timeout: 10 * time.Second}).Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if b, _ := io.ReadAll(resp.Body); string(b) != "hi" {
		t.Fatalf("body %q", b)
	}
}

// LAN servers (e.g. the router's admin page) are never split by default.
func TestPrivateAddressesAreNotSplit(t *testing.T) {
	var fragmentedSeen atomic.Int32
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
			fragmentedSeen.Add(1)
		}
		c.Write([]byte("OK"))
	}()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	p := New("127.0.0.1:0", blacklist.New(filepath.Join(t.TempDir(), "bl.txt")))
	p.TLSPorts[port] = true // SplitPrivate stays at its default (off)
	srv := httptest.NewServer(p)
	t.Cleanup(srv.Close)

	c := connectThroughProxy(t, strings.TrimPrefix(srv.URL, "http://"), ln.Addr().String())
	c.Write(fakeHello(200))
	buf := make([]byte, 2)
	io.ReadFull(c, buf)
	if string(buf) != "OK" || fragmentedSeen.Load() != 0 {
		t.Fatalf("got %q, fragmented hellos seen: %d (want OK, 0)", buf, fragmentedSeen.Load())
	}
}

func TestPACAdvertisesTheAddressTheClientUsed(t *testing.T) {
	bl := blacklist.New(filepath.Join(t.TempDir(), "bl.txt"))
	p := New("127.0.0.1:18080", bl)
	for host, want := range map[string]string{
		"192.168.1.50:18080": "PROXY 192.168.1.50:18080",
		"127.0.0.1:18080":    "PROXY 127.0.0.1:18080",
		"evil.example:18080": "PROXY 127.0.0.1:18080", // a name in Host is not trusted
	} {
		req := httptest.NewRequest("GET", "/proxy.pac", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		p.ServeHTTP(rec, req)
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("Host %q: PAC lacks %q:\n%s", host, want, rec.Body.String())
		}
	}
}

func TestClientIsLocalAndIsSelf(t *testing.T) {
	for remote, want := range map[string]bool{"127.0.0.1:5000": true, "[::1]:5000": true, "192.168.1.9:5000": false, "bad": false} {
		if got := clientIsLocal(remote); got != want {
			t.Errorf("clientIsLocal(%q)=%v want %v", remote, got, want)
		}
	}
	if !isSelf(&net.TCPAddr{IP: net.ParseIP("127.0.0.1")}) {
		t.Error("loopback must count as this PC")
	}
	if isSelf(&net.TCPAddr{IP: net.ParseIP("8.8.8.8")}) {
		t.Error("8.8.8.8 is not this PC")
	}
}
