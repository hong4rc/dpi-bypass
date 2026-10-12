// Package proxy is a CONNECT proxy that fragments the TLS ClientHello so
// SNI-based DPI can't read the site name. It also serves the PAC file that
// points the system at it.
package proxy

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"dpi/internal/blacklist"
	"dpi/internal/logx"
)

const (
	dialTimeout       = 10 * time.Second
	firstReplyTimeout = 6 * time.Second
)

type Proxy struct {
	addr     string
	bl       *blacklist.List
	TLSPorts map[string]bool   // ports whose first bytes are a TLS ClientHello
	OnSkip   func(host string) // called when a host was auto-added to the blacklist

	// SplitPrivate also fragments hellos sent to LAN/loopback IPs. Off by default:
	// routers and other embedded servers often can't reassemble fragments.
	SplitPrivate bool
}

func New(addr string, bl *blacklist.List) *Proxy {
	return &Proxy{addr: addr, bl: bl, TLSPorts: map[string]bool{"443": true}}
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		p.connect(w, r)
		return
	}
	p.servePAC(w, r)
}

// servePAC: only https:// to public hosts goes to this proxy; plain http, LAN
// addresses, local names and the blacklist go DIRECT.
func (p *Proxy) servePAC(w http.ResponseWriter, r *http.Request) {
	logx.Debug.Printf("PAC requested by %s (Host %s)", r.RemoteAddr, r.Host)
	proxyAddr := p.addr
	if h, _, err := net.SplitHostPort(r.Host); err == nil && net.ParseIP(h) != nil {
		proxyAddr = r.Host // a LAN device must be told our LAN address, not 127.0.0.1
	}
	var quoted []string
	for _, d := range p.bl.Items() {
		quoted = append(quoted, fmt.Sprintf("%q", d))
	}
	fmt.Fprintf(w, `var skip=[%s];
function FindProxyForURL(u,h){
  if(u.indexOf("https:")!=0)return "DIRECT";
  if(isPlainHostName(h)||dnsDomainIs(h,".local"))return "DIRECT";
  if(/^\d+\.\d+\.\d+\.\d+$/.test(h)&&(isInNet(h,"10.0.0.0","255.0.0.0")||isInNet(h,"172.16.0.0","255.240.0.0")||isInNet(h,"192.168.0.0","255.255.0.0")||isInNet(h,"127.0.0.0","255.0.0.0")||isInNet(h,"169.254.0.0","255.255.0.0")))return "DIRECT";
  for(var i=0;i<skip.length;i++)if(h==skip[i]||dnsDomainIs(h,"."+skip[i]))return "DIRECT";
  return "PROXY %s; DIRECT";
}`, strings.Join(quoted, ","), proxyAddr)
}

func (p *Proxy) connect(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	host, port, err := net.SplitHostPort(r.Host)
	if err != nil {
		http.Error(w, "bad host", http.StatusBadRequest)
		return
	}
	logx.Debug.Printf("CONNECT %s from %s", r.Host, r.RemoteAddr)
	up, err := net.DialTimeout("tcp", r.Host, dialTimeout)
	if err != nil {
		logx.Info.Printf("FAIL dial %s: %v", r.Host, err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if !clientIsLocal(r.RemoteAddr) && isSelf(up.RemoteAddr()) {
		up.Close()
		logx.Info.Printf("BLOCKED %s: LAN client %s tried to reach this PC through the proxy", r.Host, r.RemoteAddr)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	cl, _, err := w.(http.Hijacker).Hijack()
	if err != nil {
		up.Close()
		return
	}
	defer cl.Close()
	cl.Write([]byte("HTTP/1.1 200 OK\r\n\r\n"))

	var down int64
	if p.TLSPorts[port] {
		var reply []byte
		if up, reply, err = p.handshake(cl, up, r.Host, host); err != nil {
			logx.Info.Printf("FAIL %s: %v", r.Host, err)
			return
		}
		if len(reply) > 0 {
			n, _ := cl.Write(reply)
			down = int64(n)
		}
		logx.Debug.Printf("handshake ok %s (%s)", r.Host, time.Since(start).Round(time.Millisecond))
	}
	defer func() { up.Close() }()

	upBytes := make(chan int64, 1)
	go func() { n, _ := io.Copy(up, cl); upBytes <- n }()
	n, err := io.Copy(cl, up)
	cl.Close()
	up.Close()
	logx.Debug.Printf("closed %s up=%d down=%d dur=%s err=%v", r.Host, <-upBytes, down+n, time.Since(start).Round(time.Millisecond), err)
}

// handshake sends the client's ClientHello upstream, split unless the host is
// blacklisted. If the split hello gets no reply it retries once unsplit on a
// fresh connection and, when that works, blacklists the host. It returns the
// connection to use and the server's first bytes.
func (p *Proxy) handshake(cl, up net.Conn, target, host string) (net.Conn, []byte, error) {
	hello, err := readHello(cl)
	if err != nil {
		up.Close()
		return nil, nil, err
	}
	if hello[0] != 0x16 || p.bl.Match(host) || (!p.SplitPrivate && isPrivate(host)) { // not TLS, or told to skip: forward untouched
		if _, err := up.Write(hello); err != nil {
			up.Close()
			return nil, nil, err
		}
		return up, nil, nil
	}
	if err := writeSplit(up, hello); err != nil {
		up.Close()
		return nil, nil, err
	}
	reply := readFirst(up)
	if reply != nil && !isAlert(reply) {
		return up, reply, nil
	}
	up.Close()

	// No reply, or a TLS alert: the server may not reassemble fragmented hellos.
	logx.Debug.Printf("split hello to %s got no usable reply (alert=%v), retrying unsplit", target, reply != nil)
	up2, err := net.DialTimeout("tcp", target, dialTimeout)
	if err != nil {
		return nil, nil, err
	}
	if _, err := up2.Write(hello); err != nil {
		up2.Close()
		return nil, nil, err
	}
	reply = readFirst(up2)
	if reply == nil {
		up2.Close()
		return nil, nil, fmt.Errorf("NO REPLY with or without split (blocked or down)")
	}
	if isAlert(reply) { // rejects us either way (e.g. unsupported TLS version): not the split's fault
		return up2, reply, nil
	}
	if err := p.bl.Add(host); err != nil {
		logx.Info.Printf("cannot save %s to blacklist: %v", host, err)
	}
	logx.Info.Printf("AUTO-SKIP %s: works only without splitting, added to blacklist", host)
	if p.OnSkip != nil {
		p.OnSkip(host)
	}
	return up2, reply, nil
}
