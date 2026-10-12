package proxy

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"dpi/internal/policy"
)

// A client that opens a tunnel and then says nothing must be dropped: left alone it
// would hold two sockets and a goroutine for as long as the app runs.
func TestAClientThatNeverSendsItsHelloIsDroppedNotHeldForever(t *testing.T) {
	old := helloTimeout
	helloTimeout = 200 * time.Millisecond
	t.Cleanup(func() { helloTimeout = old })

	p := New("127.0.0.1:0", newPolicy(t, policy.Always))
	front := httptest.NewServer(p)
	t.Cleanup(front.Close)
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(up.Close)
	target := strings.TrimPrefix(up.URL, "https://")
	_, port, _ := net.SplitHostPort(target)
	p.TLSPorts[port] = true

	c, err := net.Dial("tcp", strings.TrimPrefix(front.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	io.WriteString(c, "CONNECT "+target+" HTTP/1.1\r\nHost: "+target+"\r\n\r\n")
	br := bufio.NewReader(c)
	if line, _ := br.ReadString('\n'); !strings.Contains(line, "200") {
		t.Fatalf("tunnel not opened: %q", line)
	}
	br.ReadString('\n') // the blank line

	start := time.Now()
	_, err = br.ReadByte() // the client sends nothing: the proxy must hang up
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatal("the proxy kept the silent tunnel open")
	}
	if err == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("expected the proxy to close the connection soon, err=%v after %s", err, time.Since(start))
	}
}
