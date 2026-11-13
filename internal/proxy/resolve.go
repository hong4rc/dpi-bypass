package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"dpi/internal/logx"
)

// Some networks (routers, ISPs) answer "no such host" for blocked sites, and the
// split hello cannot help when the address is never found. Resolver dials the
// normal way and only when the system lookup fails asks a public resolver over
// HTTPS (DoH), by IP address so no lookup is needed to reach it.
const (
	dohTimeout   = 3 * time.Second
	dohMinTTL    = 30 * time.Second // also how long a failed lookup is remembered
	dohMaxTTL    = 5 * time.Minute
	dohCacheSize = 512 // bounded: the proxy runs for months
	dohMaxDials  = 4
)

type dohEntry struct {
	ips  []net.IP // empty: nothing found, remembered briefly so a dead name does not cost a request each time
	exp  time.Time
	used time.Time // last time a connection was actually made to these addresses
}

type Resolver struct {
	Endpoints []string // DoH JSON endpoints (application/dns-json)
	Client    *http.Client
	Dial      func(network, addr string, timeout time.Duration) (net.Conn, error) // the normal dial; tests swap it

	mu    sync.Mutex
	cache map[string]dohEntry
}

func NewResolver() *Resolver {
	return &Resolver{
		Endpoints: []string{"https://1.1.1.1/dns-query", "https://8.8.8.8/resolve"},
		// no Proxy: the lookup must never go through this proxy or the system one
		Client: &http.Client{Timeout: dohTimeout, Transport: &http.Transport{Proxy: nil}},
		Dial:   net.DialTimeout,
	}
}

// DialTimeout is net.DialTimeout("tcp", target, d) that also works when only the
// system name lookup is broken.
func (r *Resolver) DialTimeout(target string, d time.Duration) (net.Conn, error) {
	conn, err := r.Dial("tcp", target, d)
	var dnsErr *net.DNSError
	if err == nil || !errors.As(err, &dnsErr) {
		return conn, err
	}
	host, port, serr := net.SplitHostPort(target)
	if serr != nil || net.ParseIP(host) != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	ips := r.lookupDoH(ctx, host)
	if len(ips) == 0 {
		return nil, err // report the original lookup failure
	}
	for i, ip := range ips {
		if i == dohMaxDials {
			break
		}
		c, derr := r.Dial("tcp", net.JoinHostPort(ip.String(), port), d)
		if derr == nil {
			r.mu.Lock()
			if e, ok := r.cache[host]; ok {
				e.used = time.Now()
				r.cache[host] = e
			}
			r.mu.Unlock()
			return c, nil
		}
		err = derr
	}
	return nil, err
}

// Forget drops what is remembered about host, so the next lookup asks again.
func (r *Resolver) Forget(host string) {
	r.mu.Lock()
	delete(r.cache, host)
	r.mu.Unlock()
}

// Public asks the DoH servers about host and never answers from the cache or the
// system, so it can be compared with what this network's DNS says.
func (r *Resolver) Public(ctx context.Context, host string) []net.IP {
	for _, ep := range r.Endpoints {
		if ips, _ := r.query(ctx, ep, host); len(ips) > 0 {
			return ips
		}
	}
	return nil
}

// UsedSince reports whether a connection to host was made at or after t using an
// address found by DoH, that is, only because the system lookup had failed.
func (r *Resolver) UsedSince(host string, t time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return !r.cache[host].used.IsZero() && !r.cache[host].used.Before(t)
}

// Lookup resolves host with the system resolver, falling back to DoH when that
// fails. viaDoH says the answer came from the fallback.
func (r *Resolver) Lookup(ctx context.Context, host string) (ips []net.IP, viaDoH bool, err error) {
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	for _, a := range addrs {
		ips = append(ips, a.IP)
	}
	if err != nil || len(ips) == 0 {
		if alt := r.lookupDoH(ctx, host); len(alt) > 0 {
			return alt, true, nil
		}
	}
	return ips, false, err
}

func (r *Resolver) lookupDoH(ctx context.Context, host string) []net.IP {
	r.mu.Lock()
	e, ok := r.cache[host]
	r.mu.Unlock()
	if ok && time.Now().Before(e.exp) {
		return e.ips
	}
	var ips []net.IP
	ttl := dohMinTTL
	for _, ep := range r.Endpoints {
		if ips, ttl = r.query(ctx, ep, host); len(ips) > 0 {
			break
		}
	}
	ttl = min(max(ttl, dohMinTTL), dohMaxTTL)
	r.mu.Lock()
	if r.cache == nil || len(r.cache) >= dohCacheSize {
		r.cache = map[string]dohEntry{}
	}
	r.cache[host] = dohEntry{ips: ips, exp: time.Now().Add(ttl)}
	r.mu.Unlock()
	if len(ips) > 0 {
		logx.Info.Printf("DNS: this network could not find %s, found it through a public lookup instead", host)
	}
	return ips
}

type dohAnswer struct {
	Status int `json:"Status"`
	Answer []struct {
		Type int    `json:"type"`
		TTL  int    `json:"TTL"`
		Data string `json:"data"`
	} `json:"Answer"`
}

// query asks one DoH endpoint for the A records of host, then AAAA if there are none.
func (r *Resolver) query(ctx context.Context, endpoint, host string) ([]net.IP, time.Duration) {
	for _, typ := range []string{"A", "AAAA"} {
		u := endpoint + "?name=" + url.QueryEscape(host) + "&type=" + typ
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, 0
		}
		req.Header.Set("Accept", "application/dns-json")
		resp, err := r.Client.Do(req)
		if err != nil {
			return nil, 0 // endpoint unreachable: the next one may work, AAAA would fail the same way
		}
		var ans dohAnswer
		err = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&ans)
		resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK || ans.Status != 0 {
			continue
		}
		var ips []net.IP
		ttl := dohMaxTTL
		for _, a := range ans.Answer {
			if ip := net.ParseIP(a.Data); ip != nil && (a.Type == 1 || a.Type == 28) {
				ips = append(ips, ip)
				ttl = min(ttl, time.Duration(a.TTL)*time.Second)
			}
		}
		if len(ips) > 0 {
			return ips, ttl
		}
	}
	return nil, 0
}
