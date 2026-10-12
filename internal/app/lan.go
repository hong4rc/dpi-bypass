package app

import (
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"

	"dpi/internal/logx"
	"dpi/internal/settings"
)

// legacyLANMarker is how earlier versions remembered that sharing was on.
const legacyLANMarker = "lan-sharing.on"

// lanIPs returns this PC's private IPv4 addresses on active interfaces,
// the one used for internet traffic first.
func lanIPs() []string {
	var ips []string
	ifaces, _ := net.Interfaces()
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
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

// migrateLANMarker moves the old "lan-sharing.on" marker file into settings.json.
func (a *App) migrateLANMarker() {
	marker := filepath.Join(a.Dir, legacyLANMarker)
	if _, err := os.Stat(marker); err != nil {
		return
	}
	if err := a.Settings.Update(func(s *settings.Settings) { s.LANSharing = true }); err == nil {
		os.Remove(marker)
	}
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
	_, port, _ := net.SplitHostPort(a.Addr)
	for _, ip := range lanIPs() {
		ln, err := net.Listen("tcp", net.JoinHostPort(ip, port))
		if err != nil {
			logx.Info.Printf("LAN: cannot listen on %s: %v", ip, err)
			continue
		}
		a.lan = append(a.lan, ln)
		go http.Serve(lanOnly{ln}, a.Proxy)
	}
	if len(a.lan) == 0 {
		return errors.New("no LAN address found - is this PC connected to your Wi-Fi/router?")
	}
	a.saveLAN(true)
	logx.Info.Printf("LAN sharing ON, devices can use: %v", a.lanURLs())
	return nil
}

// DisableLAN stops serving other devices and forgets the choice.
func (a *App) DisableLAN() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, ln := range a.lan {
		ln.Close()
	}
	a.lan = nil
	a.saveLAN(false)
	logx.Info.Println("LAN sharing OFF")
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
