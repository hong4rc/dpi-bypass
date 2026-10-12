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

// Connection modes reported to OnOpen.
const (
	ModeBypass   = "bypass"    // ClientHello was split
	ModeDirect   = "direct"    // forwarded untouched (skip list, LAN address, not TLS)
	ModeAutoSkip = "auto-skip" // split failed, retry unsplit worked, host now skipped
	ModeFailed   = "failed"
)

type Proxy struct {
	addr     string
	bl       *blacklist.List
	TLSPorts map[string]bool // ports whose first bytes are a TLS ClientHello

	// SplitPrivate also fragments hellos sent to LAN/loopback IPs. Off by default:
	// routers and other embedded servers often can't reassemble fragments.
	SplitPrivate bool

	OnSkip func(host string) // called when a host was auto-added to the blacklist

	// Optional observers (dashboard). client is a RemoteAddr. OnOpen is called
	// as soon as a tunnel is established (or has failed) and returns an id;
	// OnClose is called with that id when the tunnel ends, so long-lived
	// connections are visible while they are still open.
	OnOpen  func(client, hostPort, mode string) uint64
	OnClose func(id uint64, up, down int64, dur time.Duration, err error)
	OnPAC   func(client string)

	// OnClient reports the User-Agent a client sent with its PAC fetch or CONNECT,
	// which usually reveals the device type (and often the model).
	OnClient func(client, userAgent string)

	// Fallback serves every path other than /proxy.pac (the dashboard).
	Fallback http.Handler
}

func New(addr string, bl *blacklist.List) *Proxy {
	return &Proxy{addr: addr, bl: bl, TLSPorts: map[string]bool{"443": true}}
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodConnect:
		p.connect(w, r)
	case r.URL.Path == "/proxy.pac" && !r.URL.IsAbs():
		p.servePAC(w, r)
	case p.Fallback != nil && !r.URL.IsAbs():
		p.Fallback.ServeHTTP(w, r)
	default:
		http.Error(w, "dpi-bypass: use the PAC file at /proxy.pac", http.StatusNotFound)
	}
}

// servePAC: only https:// to public hosts goes to this proxy; plain http, LAN
// addresses, local names and the blacklist go DIRECT.
func (p *Proxy) servePAC(w http.ResponseWriter, r *http.Request) {
	logx.Debug.Printf("PAC requested by %s (Host %s)", r.RemoteAddr, r.Host)
	if p.OnPAC != nil {
		p.OnPAC(r.RemoteAddr)
	}
	p.noteClient(r)
	proxyAddr := p.addr
	if h, _, err := net.SplitHostPort(r.Host); err == nil && net.ParseIP(h) != nil {
		proxyAddr = r.Host // a LAN device must be told our LAN address, not 127.0.0.1
	}
	var quoted []string
	for _, d := range p.bl.Items() {
		quoted = append(quoted, fmt.Sprintf("%q", d))
	}
	w.Header().Set("Content-Type", "application/x-ns-proxy-autoconfig")
	fmt.Fprintf(w, `var skip=[%s];
function FindProxyForURL(u,h){
  if(u.indexOf("https:")!=0)return "DIRECT";
  if(isPlainHostName(h)||/\.(local|localhost|internal|lan|home\.arpa|localdomain|test)$/i.test(h))return "DIRECT";
  if(/^\d+\.\d+\.\d+\.\d+$/.test(h)&&(isInNet(h,"10.0.0.0","255.0.0.0")||isInNet(h,"172.16.0.0","255.240.0.0")||isInNet(h,"192.168.0.0","255.255.0.0")||isInNet(h,"127.0.0.0","255.0.0.0")||isInNet(h,"169.254.0.0","255.255.0.0")))return "DIRECT";
  for(var i=0;i<skip.length;i++)if(h==skip[i]||dnsDomainIs(h,"."+skip[i]))return "DIRECT";
  return "PROXY %s; DIRECT";
}`, strings.Join(quoted, ","), proxyAddr)
}

func (p *Proxy) noteClient(r *http.Request) {
	if ua := r.UserAgent(); p.OnClient != nil && ua != "" {
		p.OnClient(r.RemoteAddr, ua)
	}
}

// open reports a new connection and returns a function that reports its end.
func (p *Proxy) open(r *http.Request, mode string) (end func(up, down int64, err error)) {
	if p.OnOpen == nil {
		return func(int64, int64, error) {}
	}
	start := time.Now()
	id := p.OnOpen(r.RemoteAddr, r.Host, mode)
	return func(up, down int64, err error) {
		if p.OnClose != nil {
			p.OnClose(id, up, down, time.Since(start), err)
		}
	}
}

// fail reports a connection that never got established.
func (p *Proxy) fail(r *http.Request, err error) { p.open(r, ModeFailed)(0, 0, err) }

func (p *Proxy) connect(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	host, port, err := net.SplitHostPort(r.Host)
	if err != nil {
		http.Error(w, "bad host", http.StatusBadRequest)
		return
	}
	logx.Debug.Printf("CONNECT %s from %s", r.Host, r.RemoteAddr)
	p.noteClient(r)
	up, err := net.DialTimeout("tcp", r.Host, dialTimeout)
	if err != nil {
		logx.Info.Printf("FAIL dial %s: %v", r.Host, err)
		p.fail(r, err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if !clientIsLocal(r.RemoteAddr) && isSelf(up.RemoteAddr()) {
		up.Close()
		logx.Info.Printf("BLOCKED %s: LAN client %s tried to reach this PC through the proxy", r.Host, r.RemoteAddr)
		p.fail(r, fmt.Errorf("blocked: LAN client may not reach this PC"))
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
	mode := ModeDirect
	if p.TLSPorts[port] {
		var reply []byte
		if up, reply, mode, err = p.handshake(cl, up, r.Host, host); err != nil {
			logx.Info.Printf("FAIL %s: %v", r.Host, err)
			p.fail(r, err)
			return
		}
		if len(reply) > 0 {
			n, _ := cl.Write(reply)
			down = int64(n)
		}
		logx.Debug.Printf("handshake ok %s mode=%s (%s)", r.Host, mode, time.Since(start).Round(time.Millisecond))
	}
	defer func() { up.Close() }()
	end := p.open(r, mode) // counted now, while the tunnel is still open

	upBytes := make(chan int64, 1)
	go func() { n, _ := io.Copy(up, cl); upBytes <- n }()
	n, err := io.Copy(cl, up)
	cl.Close()
	up.Close()
	upN := <-upBytes
	logx.Debug.Printf("closed %s up=%d down=%d dur=%s err=%v", r.Host, upN, down+n, time.Since(start).Round(time.Millisecond), err)
	end(upN, down+n, nil)
}

// handshake sends the client's ClientHello upstream, split unless the host is
// blacklisted. If the split hello gets no usable reply it retries once unsplit
// on a fresh connection and, when that works, blacklists the host. It returns
// the connection to use, the server's first bytes and how it was handled.
func (p *Proxy) handshake(cl, up net.Conn, target, host string) (net.Conn, []byte, string, error) {
	hello, err := readHello(cl)
	if err != nil {
		up.Close()
		return nil, nil, "", err
	}
	if hello[0] != 0x16 || p.bl.Match(host) || (!p.SplitPrivate && (isPrivate(host) || isPrivateAddr(up.RemoteAddr()))) { // not TLS, or told to skip: forward untouched
		if _, err := up.Write(hello); err != nil {
			up.Close()
			return nil, nil, "", err
		}
		return up, nil, ModeDirect, nil
	}
	if err := writeSplit(up, hello); err != nil {
		up.Close()
		return nil, nil, "", err
	}
	reply := readFirst(up)
	if reply != nil && !isAlert(reply) {
		return up, reply, ModeBypass, nil
	}
	up.Close()

	// No reply, or a TLS alert: the server may not reassemble fragmented hellos.
	logx.Debug.Printf("split hello to %s got no usable reply (alert=%v), retrying unsplit", target, reply != nil)
	up2, err := net.DialTimeout("tcp", target, dialTimeout)
	if err != nil {
		return nil, nil, "", err
	}
	if _, err := up2.Write(hello); err != nil {
		up2.Close()
		return nil, nil, "", err
	}
	reply = readFirst(up2)
	if reply == nil {
		up2.Close()
		return nil, nil, "", fmt.Errorf("NO REPLY with or without split (blocked or down)")
	}
	if isAlert(reply) { // rejects us either way (e.g. unsupported TLS version): not the split's fault
		return up2, reply, ModeDirect, nil
	}
	added, err := p.bl.Add(host, "auto-skip "+time.Now().Format("2006-01-02"))
	switch {
	case err != nil:
		logx.Info.Printf("cannot save %s to blacklist: %v", host, err)
	case added: // several connections to one host fail together: announce it once
		logx.Info.Printf("AUTO-SKIP %s: works only without splitting, added to blacklist", host)
		if p.OnSkip != nil {
			p.OnSkip(host)
		}
	}
	return up2, reply, ModeAutoSkip, nil
}
