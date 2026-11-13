package proxy

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// dohServer answers every query with the given JSON body and counts the requests.
func dohServer(t *testing.T, status int, body string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

const aAnswer = `{"Status":0,"Answer":[{"type":1,"TTL":300,"data":"203.0.113.7"}]}`

// testResolver cannot find any name through the "system" (a DNS error) but dials
// an IP address for real; dialed records what was dialed.
func testResolver(srv *httptest.Server, dialed *[]string) *Resolver {
	r := NewResolver()
	r.Client = srv.Client()
	r.Endpoints = []string{srv.URL}
	r.Dial = func(_, addr string, _ time.Duration) (net.Conn, error) {
		host, _, _ := net.SplitHostPort(addr)
		*dialed = append(*dialed, addr)
		if net.ParseIP(host) == nil {
			return nil, &net.OpError{Op: "dial", Err: &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}}
		}
		c, _ := net.Pipe()
		return c, nil
	}
	return r
}

func TestDialFallsBackToDoHWhenSystemLookupFails(t *testing.T) {
	srv, hits := dohServer(t, http.StatusOK, aAnswer)
	var dialed []string
	r := testResolver(srv, &dialed)

	began := time.Now()
	if r.UsedSince("blocked.example.test", began) {
		t.Fatal("reported as used before any dial")
	}
	c, err := r.DialTimeout("blocked.example.test:443", time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	c.Close()
	if got := dialed[len(dialed)-1]; got != "203.0.113.7:443" {
		t.Fatalf("dialed %q, want the DoH address", got)
	}
	if !r.UsedSince("blocked.example.test", began) {
		t.Fatal("a connection made with a DoH address is not reported")
	}

	c, err = r.DialTimeout("blocked.example.test:443", time.Second)
	if err != nil {
		t.Fatalf("second dial: %v", err)
	}
	c.Close()
	if hits.Load() != 1 {
		t.Fatalf("DoH asked %d times, want 1 (second answer comes from the cache)", hits.Load())
	}
}

func TestDialKeepsSystemErrorWhenDoHFindsNothing(t *testing.T) {
	srv, _ := dohServer(t, http.StatusOK, `{"Status":3}`)
	var dialed []string
	r := testResolver(srv, &dialed)

	_, err := r.DialTimeout("nothing.example.test:443", time.Second)
	if err == nil || !containsDNSError(err) {
		t.Fatalf("want the original no such host error, got %v", err)
	}
}

func TestDialTriesNextEndpoint(t *testing.T) {
	bad, _ := dohServer(t, http.StatusInternalServerError, "oops")
	good, _ := dohServer(t, http.StatusOK, aAnswer)
	var dialed []string
	r := testResolver(good, &dialed)
	r.Endpoints = []string{bad.URL, good.URL}

	c, err := r.DialTimeout("blocked.example.test:443", time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	c.Close()
}

func TestDoHNotUsedForOtherErrorsOrIPs(t *testing.T) {
	srv, hits := dohServer(t, http.StatusOK, aAnswer)
	r := NewResolver()
	r.Client, r.Endpoints = srv.Client(), []string{srv.URL}
	r.Dial = func(string, string, time.Duration) (net.Conn, error) {
		return nil, &net.OpError{Op: "dial", Err: errRefused}
	}
	if _, err := r.DialTimeout("site.example.test:443", time.Second); err == nil {
		t.Fatal("want the refused error")
	}
	if _, err := r.DialTimeout("192.0.2.1:443", time.Second); err == nil {
		t.Fatal("want the refused error")
	}
	if hits.Load() != 0 {
		t.Fatalf("DoH asked %d times for a non-DNS failure, want 0", hits.Load())
	}
}

func TestDoHCacheIsBounded(t *testing.T) {
	srv, _ := dohServer(t, http.StatusOK, aAnswer)
	var dialed []string
	r := testResolver(srv, &dialed)
	for i := 0; i < dohCacheSize+50; i++ {
		r.lookupDoH(t.Context(), "h"+string(rune('a'+i%26))+time.Duration(i).String()+".example.test")
	}
	if n := len(r.cache); n > dohCacheSize {
		t.Fatalf("cache holds %d entries, limit %d", n, dohCacheSize)
	}
}

type refused struct{}

func (refused) Error() string { return "connection refused" }

var errRefused error = refused{}

func containsDNSError(err error) bool {
	var d *net.DNSError
	return errors.As(err, &d)
}
