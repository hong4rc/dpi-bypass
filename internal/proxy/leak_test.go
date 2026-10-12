package proxy

import (
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"dpi/internal/policy"
)

// An app that runs for a year serves millions of tunnels. Whatever a tunnel holds
// (goroutines, sockets) must be released when it ends, or the app slowly dies.
func TestFinishedTunnelsLeaveNothingBehind(t *testing.T) {
	p := New("127.0.0.1:0", newPolicy(t, policy.Learn))
	front := httptest.NewServer(p)
	defer front.Close()
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, strings.Repeat("x", 4096))
	}))
	defer up.Close()
	p.TLSPorts[strings.TrimPrefix(up.URL[strings.LastIndex(up.URL, ":"):], ":")] = true

	proxyURL, _ := url.Parse(front.URL)
	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			Proxy:             http.ProxyURL(proxyURL),
			TLSClientConfig:   &tls.Config{InsecureSkipVerify: true},
			DisableKeepAlives: true,
		},
	}
	run := func(n int) {
		var wg sync.WaitGroup
		sem := make(chan struct{}, 20)
		for i := 0; i < n; i++ {
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				resp, err := client.Get(up.URL)
				if err != nil {
					t.Error(err)
					return
				}
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}()
		}
		wg.Wait()
	}

	run(20) // warm up: server and client pools exist before the baseline is taken
	client.CloseIdleConnections()
	settle := func() int {
		deadline := time.Now().Add(3 * time.Second)
		n := runtime.NumGoroutine()
		for time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
			if m := runtime.NumGoroutine(); m < n {
				n = m
			} else if m == n {
				break
			}
		}
		return n
	}
	base := settle()

	run(500)
	client.CloseIdleConnections()
	after := settle()
	if after > base+5 {
		t.Fatalf("goroutines grew from %d to %d after 500 finished tunnels: something is not released", base, after)
	}
}
