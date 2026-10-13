package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func serve(t *testing.T, h http.HandlerFunc) string {
	t.Helper()
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	return s.Listener.Addr().String()
}

func TestIsRunning(t *testing.T) {
	ok := serve(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"version":"v1","on":true}`)) })
	notFound := serve(t, http.NotFound)
	notJSON := serve(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("<html>hi</html>")) })
	otherJSON := serve(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"hello":1}`)) })

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := ln.Addr().String()
	ln.Close()

	for name, c := range map[string]struct {
		addr string
		want bool
	}{"dpi-bypass": {ok, true}, "404": {notFound, false}, "not json": {notJSON, false}, "json without version": {otherJSON, false}, "closed port": {closed, false}} {
		if got := isRunning(c.addr); got != c.want {
			t.Errorf("%s: isRunning=%v want %v", name, got, c.want)
		}
	}
}
