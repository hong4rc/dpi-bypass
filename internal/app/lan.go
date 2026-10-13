package app

import (
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	"dpi/internal/identify"
	"dpi/internal/logx"
	"dpi/internal/settings"
	"dpi/internal/stats"
)

// lanIPs returns this PC's private IPv4 addresses on active interfaces,
// the one used for internet traffic first.
func lanIPs() []string {
	var ips []string
	ifaces, _ := net.Interfaces()
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 || isVirtual(ifc.Name) {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, ad := range addrs {
			if n, ok := ad.(*net.IPNet); ok {
				if ip4 := n.IP.To4(); ip4 != nil && ip4.IsPrivate() {
					ips = append(ips, ip4.String())
				}
			}
		}
	}
	primary := primaryIP()
	sort.SliceStable(ips, func(i, j int) bool { return ips[i] == primary && ips[j] != primary })
	return ips
}

// primaryIP is the local address the OS would use to reach the internet ("" if none).
// Dialing UDP sends nothing; it only makes the OS pick a route.
func primaryIP() string {
	c, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer c.Close()
	if a, ok := c.LocalAddr().(*net.UDPAddr); ok {
		return a.IP.String()
	}
	return ""
}

// lanOnly accepts connections from private, link-local or loopback clients only.
type lanOnly struct{ net.Listener }

func (l lanOnly) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if allowedClient(c.RemoteAddr()) {
			return c, nil
		}
		logx.Info.Printf("LAN: rejected non-LAN client %s", c.RemoteAddr())
		c.Close()
	}
}

func allowedClient(a net.Addr) bool {
	t, ok := a.(*net.TCPAddr)
	return ok && (t.IP.IsPrivate() || t.IP.IsLoopback() || t.IP.IsLinkLocalUnicast())
}

// LANWanted reports whether sharing was left on last time.
func (a *App) LANWanted() bool { return a.Settings.Get().LANSharing }

func (a *App) saveLAN(on bool) {
	if err := a.Settings.Update(func(s *settings.Settings) { s.LANSharing = on }); err != nil {
		logx.Info.Printf("cannot save settings: %v", err)
	}
}

// EnableLAN also serves the proxy on this PC's private LAN addresses so other
// devices (iPhone, Android, ...) can use it, and remembers the choice.
func (a *App) EnableLAN() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.lan) > 0 {
		return nil
	}
	if err := a.bindLAN(); err != nil {
		return err
	}
	a.saveLAN(true)
	logx.Info.Printf("LAN sharing ON, devices can use: %v", a.lanURLs())
	return nil
}

// bindLAN listens on the PC's current private addresses (a.mu held).
func (a *App) bindLAN() error {
	_, port, _ := net.SplitHostPort(a.Addr)
	addrs := a.LANAddrs()
	a.lanTried = append([]string{}, addrs...)
	var bound []string
	for _, ip := range addrs {
		ln, err := net.Listen("tcp", net.JoinHostPort(ip, port))
		if err != nil {
			logx.Info.Printf("LAN: cannot listen on %s: %v", ip, err)
			continue
		}
		a.lan = append(a.lan, ln)
		bound = append(bound, ip)
		go a.newServer(a.Proxy).Serve(lanOnly{ln})
	}
	if len(a.lan) == 0 {
		return errors.New("no LAN address found - is this PC connected to your Wi-Fi/router?")
	}
	a.bound = bound
	a.startIdentify(bound, port)
	return nil
}

// unbindLAN closes the sharing listeners without touching the saved choice (a.mu held).
func (a *App) unbindLAN() {
	for _, ln := range a.lan {
		ln.Close()
	}
	a.lan, a.bound = nil, nil
	for _, ln := range a.identify {
		ln.Close()
	}
	a.identify = nil
}

// DisableLAN stops serving other devices and forgets the choice.
func (a *App) DisableLAN() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.unbindLAN()
	a.saveLAN(false)
	logx.Info.Println("LAN sharing OFF")
}

// refreshLAN keeps sharing on the PC's current addresses while the app runs for
// months: the router may hand out a new address, Wi-Fi may reconnect, and the app
// may have started before the network was up. It only acts when sharing is wanted
// and the set of addresses changed since the last attempt, so a permanent failure
// is not retried (and logged) over and over.
func (a *App) refreshLAN() {
	if !a.LANWanted() {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.LANAddrs()
	if len(now) == 0 || (a.lanTried != nil && sameAddrs(now, a.lanTried)) {
		return
	}
	a.unbindLAN()
	if err := a.bindLAN(); err != nil {
		logx.Info.Printf("LAN sharing could not follow the address change: %v", err)
		return
	}
	logx.Info.Printf("LAN sharing now on the PC's current addresses, devices can use: %v", a.lanURLs())
}

func sameAddrs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x, y := append([]string(nil), a...), append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

// newServer is the HTTP server for a listener: connections that never finish
// their request line or sit idle are dropped, so they cannot pile up for years.
// A tunnel (CONNECT) is hijacked and then lives outside these timeouts.
func (a *App) newServer(h http.Handler) *http.Server {
	return &http.Server{Handler: h, ReadHeaderTimeout: 30 * time.Second, IdleTimeout: 2 * time.Minute}
}

func (a *App) LANEnabled() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.lan) > 0
}

// LANURLs are the PAC addresses to enter on other devices (empty when sharing is off).
func (a *App) LANURLs() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lanURLs()
}

func (a *App) lanURLs() []string {
	var urls []string
	for _, ln := range a.lan {
		urls = append(urls, "http://"+ln.Addr().String()+"/proxy.pac")
	}
	return urls
}

// isVirtual reports adapters that only talk to VMs and containers (Hyper-V, WSL, Docker, VMware...).
// Phones are never on them, and listing their addresses would only confuse the setup address.
func isVirtual(name string) bool {
	name = strings.ToLower(name)
	for _, s := range []string{"vethernet", "wsl", "hyper-v", "vmware", "virtualbox", "vboxnet", "docker", "veth", "virbr", "br-"} {
		if strings.Contains(name, s) {
			return true
		}
	}
	return false
}

// identifyPort is where the "identify this device" HTTPS page listens (the
// proxy port is 18080; tests use port 0 and get a random one here too).
const identifyPort = "18443"

// startIdentify serves the identify page over HTTPS on the same LAN addresses.
// The certificate is kept next to the exe so a browser's "proceed anyway" choice survives restarts.
func (a *App) startIdentify(ips []string, proxyPort string) {
	port := identifyPort
	if proxyPort == "0" {
		port = "0"
	}
	var parsed []net.IP
	for _, ip := range ips {
		parsed = append(parsed, net.ParseIP(ip))
	}
	cert := identify.Renewing(a.Dir, parsed) // re-checked now and then: an app left running for years must not serve an expired certificate
	if _, err := cert(); err != nil {
		logx.Info.Printf("identify page unavailable: %v", err)
		return
	}
	srv := &http.Server{
		ErrorLog:    log.New(io.Discard, "", 0), // phones that refuse the certificate would otherwise spam errors
		ReadTimeout: 30 * time.Second,           // also covers the TLS handshake: a client that never finishes it is dropped
		IdleTimeout: time.Minute,
		Handler: identify.New(func(client string, id stats.Identity) error {
			ip := stats.ClientIP(client)
			logx.Info.Printf("device %s identified itself: %s", ip, id.Describe())
			return a.Stats.SetIdentity(ip, id)
		}, a.proxyStatus),
	}
	for _, ip := range ips {
		raw, err := net.Listen("tcp", net.JoinHostPort(ip, port))
		if err != nil {
			logx.Info.Printf("identify page: cannot listen on %s: %v", ip, err)
			continue
		}
		a.identify = append(a.identify, raw)
		go srv.Serve(identify.TLSListenerFrom(lanOnly{raw}, cert))
	}
}

// IdentifyURLs are the addresses of the identify page (empty when sharing is off).
func (a *App) IdentifyURLs() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var urls []string
	for _, ln := range a.identify {
		urls = append(urls, "https://"+ln.Addr().String()+"/identify")
	}
	return urls
}

// proxyCheckWindow is how recent a phone's probe must be to count as "using the proxy".
const proxyCheckWindow = 15 * time.Second

// proxyStatus answers the identify page's question "is this phone using the proxy?",
// and gives it the setup address for the PC address it reached us on.
func (a *App) proxyStatus(client, host string) (usingProxy bool, pac string) {
	return a.Stats.ProbeSeen(stats.ClientIP(client), proxyCheckWindow), a.pacFor(host)
}

// pacFor is the proxy setup address on the given PC address ("" if the PC is not sharing on it).
func (a *App) pacFor(host string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, ln := range a.lan {
		if h, _, err := net.SplitHostPort(ln.Addr().String()); err == nil && h == host {
			return "http://" + ln.Addr().String() + "/proxy.pac"
		}
	}
	return ""
}
