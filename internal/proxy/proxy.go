// Package proxy is a CONNECT proxy that fragments the TLS ClientHello so
// SNI-based DPI can't read the site name. It also serves the PAC file that
// points the system at it. Whether a given site is fragmented, and what is
// learned from how connections go, is decided by the policy package.
package proxy

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"dpi/internal/logx"
	"dpi/internal/policy"
)

// Timeouts; variables so tests can shorten them.
var (
	dialTimeout       = 10 * time.Second
	firstReplyTimeout = 6 * time.Second
	helloTimeout      = 30 * time.Second // a client that opens a tunnel and never sends its hello must not hold it forever
)

// Connection modes reported to OnOpen.
const (
	ModeBypass     = "bypass"      // ClientHello was split
	ModeDirect     = "direct"      // forwarded untouched (never-bypass site, LAN address, not TLS, Learn's normal attempt)
	ModeAutoSkip   = "auto-skip"   // Always: the split failed, the direct retry worked
	ModeAutoBypass = "auto-bypass" // Learn: the normal attempt failed, the split retry worked
	ModeFailed     = "failed"
)

type Proxy struct {
	addr     string
	pol      *policy.Policy
	TLSPorts map[string]bool // ports whose first bytes are a TLS ClientHello

	// SplitPrivate also fragments hellos sent to LAN/loopback IPs. Off by default:
	// routers and other embedded servers often can't reassemble fragments.
	SplitPrivate bool

	// OnLearn is called when the policy persisted a new decision for host
	// (NO_BYPASS or NEEDS_BYPASS) after enough evidence.
	OnLearn func(host string, d policy.Decision)

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

	// ProbeHost is a made-up name (never a real site). A CONNECT to it is not
	// forwarded: it is reported to OnProbe, which is how a phone is proven to use the
	// proxy. Empty disables the check.
	ProbeHost string
	OnProbe   func(client string)

	// Fallback serves every path other than /proxy.pac (the dashboard).
	Fallback http.Handler
}

func New(addr string, pol *policy.Policy) *Proxy {
	return &Proxy{addr: addr, pol: pol, TLSPorts: map[string]bool{"443": true}}
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
// addresses, local names and the never-bypass sites go DIRECT. Everything else
// goes through the proxy, also in Learn mode: the proxy has to see a failure to learn from it.
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
	for _, d := range p.pol.Skip.Items() {
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
	if p.ProbeHost != "" && host == p.ProbeHost {
		// "Is this client using the proxy?" check from the identify page: the name exists
		// nowhere, so only a client that sends its HTTPS requests to the proxy can ever get here.
		if p.OnProbe != nil {
			p.OnProbe(r.RemoteAddr)
		}
		http.Error(w, "dpi-bypass proxy check", http.StatusForbidden)
		return
	}
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

// send writes the ClientHello with method m and waits for the server's first
// bytes. usable is false when nothing came back, or when a split hello was
// answered with a TLS alert (the server could not reassemble it). A direct
// hello answered with an alert is a real answer: the server just refused.
func send(c net.Conn, hello []byte, m policy.Method) (reply []byte, usable bool) {
	var err error
	if m == policy.Split {
		err = writeSplit(c, hello)
	} else {
		_, err = c.Write(hello)
	}
	if err != nil {
		return nil, false
	}
	reply = readFirst(c)
	return reply, reply != nil && !(m == policy.Split && isAlert(reply))
}

func name(m policy.Method) string {
	if m == policy.Split {
		return "split"
	}
	return "direct"
}

// handshake forwards the client's ClientHello the way the policy plans for this
// host: first attempt, then a fallback on a fresh connection if the first got no
// usable reply. A working fallback is evidence for a decision, which the policy
// persists only once it has been seen often enough. It returns the connection to
// use, the server's first bytes and how the connection was handled.
func (p *Proxy) handshake(cl, up net.Conn, target, host string) (net.Conn, []byte, string, error) {
	cl.SetReadDeadline(time.Now().Add(helloTimeout))
	hello, err := readHello(cl)
	cl.SetReadDeadline(time.Time{})
	if err != nil {
		up.Close()
		return nil, nil, "", err
	}
	local := !p.SplitPrivate && (isPrivate(host) || isPrivateAddr(up.RemoteAddr()))
	plan := p.pol.PlanFor(host)
	if hello[0] != 0x16 || local || plan.Fallback == policy.None {
		// not TLS, a LAN server, or a never-bypass site: forward untouched, nothing to wait for or learn
		if _, err := up.Write(hello); err != nil {
			up.Close()
			return nil, nil, "", err
		}
		return up, nil, ModeDirect, nil
	}

	if reply, ok := send(up, hello, plan.First); ok {
		p.pol.Succeeded(host, plan) // a working first attempt disproves earlier failures: they were transient
		if plan.First == policy.Split {
			return up, reply, ModeBypass, nil
		}
		return up, reply, ModeDirect, nil
	}
	up.Close()

	logx.Debug.Printf("%s: no usable reply to the %s attempt, trying %s", target, name(plan.First), name(plan.Fallback))
	up2, err := net.DialTimeout("tcp", target, dialTimeout)
	if err != nil {
		return nil, nil, "", err
	}
	reply, usable := send(up2, hello, plan.Fallback)
	switch {
	case reply == nil:
		up2.Close()
		return nil, nil, "", fmt.Errorf("NO REPLY with or without the bypass (blocked or down)")
	case isAlert(reply):
		// the server refuses either way (for example an unsupported TLS version): not a DPI effect, nothing to learn
		return up2, reply, ModeDirect, nil
	case !usable:
		up2.Close()
		return nil, nil, "", fmt.Errorf("no usable reply")
	}

	mode := ModeDirect
	if plan.Fallback == policy.Split {
		mode = ModeBypass
	}
	if plan.Learn != policy.Unknown {
		persisted, err := p.pol.Observe(host, plan.Learn)
		switch {
		case err != nil:
			logx.Info.Printf("cannot save what was learned about %s: %v", host, err)
		case persisted:
			if plan.Learn == policy.NoBypass {
				logx.Info.Printf("AUTO-SKIP %s: works only without the bypass, added to the never-bypass list", host)
			} else {
				logx.Info.Printf("AUTO-BYPASS %s: needs the bypass, added to the bypass list", host)
			}
			if p.OnLearn != nil {
				p.OnLearn(host, plan.Learn)
			}
		default:
			logx.Debug.Printf("%s: %s worked after %s failed (%s evidence, not yet persisted)", host, name(plan.Fallback), name(plan.First), plan.Learn)
		}
		if plan.Learn == policy.NoBypass {
			mode = ModeAutoSkip
		} else {
			mode = ModeAutoBypass
		}
	}
	return up2, reply, mode, nil
}
