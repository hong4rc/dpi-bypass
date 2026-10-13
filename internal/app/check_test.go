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

	got := a.Check() // a.on is false: nothing here touches the system proxy
	if !got.OK || !strings.Contains(got.Message, "bypass is off for this PC") || got.Detail == "" {
		t.Fatalf("off: %+v", got)
	}

	a.on = true
	got = a.Check()
	if !got.OK || got.Message != "Steam works through the bypass." {
		t.Fatalf("on: %+v", got)
	}
}

func TestCheckFailureIsOneSentenceWithTheRawErrorKept(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	dead := ln.Addr().String()
	ln.Close()
	a := New(t.TempDir(), dead, "test")
	a.steamURL = "http://steam.example.test/"

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

	got := a.Check()
	if got.OK || !strings.HasPrefix(got.Message, "The name could not be found") || !strings.Contains(got.Detail, "no such host") {
		t.Fatalf("%+v", got)
	}
}
