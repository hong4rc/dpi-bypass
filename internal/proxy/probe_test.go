package proxy

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"dpi/internal/policy"
)

const probeName = "probe.test-only.invalid"

func connectLine(t *testing.T, proxyAddr, target string) (status string) {
	t.Helper()
	c, err := net.Dial("tcp", proxyAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(10 * time.Second))
	io.WriteString(c, "CONNECT "+target+" HTTP/1.1\r\nHost: "+target+"\r\n\r\n")
	line, _ := bufio.NewReader(c).ReadString('\n')
	return strings.TrimSpace(line)
}

func probeProxy(t *testing.T) (*Proxy, string, *atomic.Int32, chan string) {
	t.Helper()
	p := New("127.0.0.1:0", newPolicy(t, policy.Always))
	p.ProbeHost = probeName
	probes := make(chan string, 4)
	p.OnProbe = func(client string) { probes <- client }
	var opens atomic.Int32
	p.OnOpen = func(string, string, string) uint64 { opens.Add(1); return 0 }
	srv := httptest.NewServer(p)
	t.Cleanup(srv.Close)
	return p, strings.TrimPrefix(srv.URL, "http://"), &opens, probes
}

// Only a client that sends its HTTPS requests to the proxy can ever get a CONNECT for the made-up name here.
func TestAConnectToTheProbeNameIsReportedAndNeverForwarded(t *testing.T) {
	_, addr, opens, probes := probeProxy(t)

	status := connectLine(t, addr, probeName+":443")
	if !strings.Contains(status, "403") {
		t.Fatalf("the probe must be answered with a refusal (not a dial failure 502, not 200): %q", status)
	}
	select {
	case client := <-probes:
		if !strings.HasPrefix(client, "127.0.0.1:") {
			t.Fatalf("the probe must name the asking client, got %q", client)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OnProbe was not called")
	}
	if opens.Load() != 0 {
		t.Fatal("a probe is not a connection: it must not show up in the connection list or be dialled")
	}
}

func TestOnlyTheExactProbeNameIsSpecial(t *testing.T) {
	p, addr, _, probes := probeProxy(t)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(srv.Close)
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "https://"))
	p.TLSPorts[port] = true

	// a real destination, and names that merely look similar, are ordinary CONNECTs
	for _, target := range []string{
		strings.TrimPrefix(srv.URL, "https://"),
		"x." + probeName + ":443",
		"not-" + probeName + ":443",
	} {
		if status := connectLine(t, addr, target); strings.Contains(status, "403") {
			t.Errorf("%s must not be treated as the probe: %q", target, status)
		}
	}
	select {
	case c := <-probes:
		t.Fatalf("a non-probe CONNECT was reported as a probe from %s", c)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestWithoutAProbeHostNothingIsSpecial(t *testing.T) {
	p := New("127.0.0.1:0", newPolicy(t, policy.Always)) // ProbeHost left empty
	srv := httptest.NewServer(p)
	t.Cleanup(srv.Close)
	status := connectLine(t, strings.TrimPrefix(srv.URL, "http://"), "127.0.0.1:1") // nothing listens there
	if !strings.Contains(status, "502") {
		t.Fatalf("an ordinary CONNECT that fails must still be a 502, got %q", status)
	}
}
