package app

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dpi/internal/proxy"
	"dpi/internal/stats"
)

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestPlainError(t *testing.T) {
	for name, c := range map[string]struct {
		err  error
		want string
	}{
		"deadline":      {context.DeadlineExceeded, "No reply in time"},
		"net timeout":   {&net.OpError{Op: "read", Err: timeoutErr{}}, "No reply in time"},
		"reset":         {errors.New("read tcp: connection reset by peer"), "The connection was cut"},
		"bad gateway":   {errors.New("502 Bad Gateway: "), "The proxy could not reach Steam"},
		"windows reset": {errors.New("wsarecv: An existing connection was forcibly closed by the remote host."), "The connection was cut"},
		"eof":           {io.EOF, "The connection was cut"},
		"dns":           {errors.New("dial tcp: lookup nope.example: no such host"), "The name could not be found"},
		"refused":       {errors.New("dial tcp 127.0.0.1:18080: connectex: No connection could be made because the target machine actively refused it."), "The proxy did not answer"},
		"other":         {errors.New("something odd"), "Steam could not be reached"},
	} {
		if got := plainError(c.err); !strings.HasPrefix(got, c.want) {
			t.Errorf("%s: %q does not start with %q", name, got, c.want)
		}
	}
}

// answers every request like a proxy that reaches the site
func stubProxy(t *testing.T, status int) string {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
	t.Cleanup(s.Close)
	return s.Listener.Addr().String()
}

func TestCheckSaysWhenTheBypassIsOff(t *testing.T) {
	a := New(t.TempDir(), stubProxy(t, 200), "test")
	a.steamURL = "http://steam.example.test/"
	offline(a)

	got := a.Check() // a.on is false: nothing here touches the system proxy
	if !got.OK || !strings.Contains(got.Message, "bypass is off for this PC") || got.Detail == "" {
		t.Fatalf("off: %+v", got)
	}

	a.on = true
	got = a.Check()
	if !got.OK || got.Message != "Steam works through the proxy." {
		t.Fatalf("on: %+v", got)
	}
}

func TestCheckMessageSeparatesBypassFromDNS(t *testing.T) {
	a := New(t.TempDir(), "127.0.0.1:1", "test")
	a.on = true
	for name, c := range map[string]struct {
		mode   string
		viaDNS bool
		want   []string
		not    []string
	}{
		"split worked":    {"auto-bypass", false, []string{"needed the bypass"}, []string{"DNS"}},
		"not blocked":     {"direct", false, []string{"not because of the bypass"}, []string{"needed the bypass", "DNS"}},
		"dns help only":   {"direct", true, []string{"not because of the bypass", "DNS could not find Steam", "not the bypass"}, []string{"needed the bypass"}},
		"both were used":  {"bypass", true, []string{"needed the bypass", "DNS could not find Steam"}, nil},
		"nothing on file": {"", false, []string{"through the proxy"}, []string{"needed the bypass"}},
	} {
		got := a.okMessage(c.mode, c.mode != "", c.viaDNS)
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: %q lacks %q", name, got, w)
			}
		}
		for _, w := range c.not {
			if strings.Contains(got, w) {
				t.Errorf("%s: %q must not contain %q", name, got, w)
			}
		}
	}
}

func TestDiagnoseTextSaysWhenDNSWasWorkedAround(t *testing.T) {
	r := DiagnoseResult{Report: proxy.Report{Host: "example.test", IPs: []string{"203.0.113.7"}, Level: "ok", Verdict: "Works without the bypass.", DNSFallback: true}}
	got := r.text()
	for _, want := range []string{"public DNS instead. That is not the bypass", "The bypass is not needed"} {
		if !strings.Contains(got, want) {
			t.Errorf("text lacks %q:\n%s", want, got)
		}
	}
	r.DNSFallback = false
	if strings.Contains(r.text(), "public DNS") {
		t.Error("DNS note shown although the system lookup worked")
	}
}

func TestCheckFailureIsOneSentenceWithTheRawErrorKept(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	dead := ln.Addr().String()
	ln.Close()
	a := New(t.TempDir(), dead, "test")
	a.steamURL = "http://steam.example.test/"
	offline(a)

	got := a.Check()
	if got.OK || got.Message != "The proxy did not answer." || !strings.Contains(got.Detail, dead) {
		t.Fatalf("%+v", got)
	}
}

func TestCheckSurfacesWhyTheProxyCouldNotReachTheSite(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "dial tcp: lookup steam.example.test: no such host", http.StatusBadGateway) // what the proxy answers when its dial fails
	}))
	defer s.Close()
	a := New(t.TempDir(), s.Listener.Addr().String(), "test")
	a.steamURL = "https://steam.example.test/"
	offline(a)

	got := a.Check()
	if got.OK || !strings.HasPrefix(got.Message, "The name could not be found") || !strings.Contains(got.Detail, "no such host") {
		t.Fatalf("%+v", got)
	}
}

// offline makes the direct comparison fail at once, so no test touches the real network.
func offline(a *App) {
	a.DiagEnv.Lookup = func(context.Context, string) ([]net.IP, error) { return nil, errors.New("no such host") }
}

func TestCheckDetailExplainsWhatHappened(t *testing.T) {
	a := New(t.TempDir(), stubProxy(t, 200), "test")
	a.steamURL = "http://steam.example.test/"
	offline(a)

	d := a.Check().Detail
	for _, want := range []string{"What was done", "Asked the proxy", "HTTP 200", "On the lists: not listed", "Bypass on this PC: off", "DPI mode: learn", "The same site checked directly", "Name lookup failed"} {
		if !strings.Contains(d, want) {
			t.Errorf("detail lacks %q:\n%s", want, d)
		}
	}
}

func TestCheckDetailNamesHowTheProxyConnected(t *testing.T) {
	a := New(t.TempDir(), stubProxy(t, 200), "test")
	a.steamURL = "http://steam.example.test/"
	offline(a)
	id := a.Stats.Open(stats.Event{Client: "127.0.0.1", Host: "steam.example.test:443", Mode: "auto-bypass"})
	a.Stats.Close(id, 1, 1, 5, "")

	if d := a.Check().Detail; !strings.Contains(d, "retried with the bypass") {
		t.Fatalf("mode not described:\n%s", d)
	}
}
