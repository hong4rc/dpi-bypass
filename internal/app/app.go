// Package app ties the proxy, blacklist, stats and system-proxy setting together.
package app

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"dpi/internal/blacklist"
	"dpi/internal/identify"
	"dpi/internal/logx"
	"dpi/internal/policy"
	"dpi/internal/proxy"
	"dpi/internal/settings"
	"dpi/internal/stats"
	"dpi/internal/sysproxy"
)

const SteamURL = "https://store.steampowered.com"

type App struct {
	Version  string
	Dir      string // where the exe, logs and blacklist live
	Addr     string
	Started  time.Time
	Policy   *policy.Policy // what is known about each site (NO_BYPASS / NEEDS_BYPASS) and how unknown sites are handled
	Proxy    *proxy.Proxy
	Stats    *stats.Store
	Settings *settings.Store
	LANAddrs func() []string // addresses to share on: the PC's private LAN addresses unless a test swaps it
	DiagEnv  proxy.DiagEnv   // how Diagnose reaches the network; the zero value is the real network

	steamURL string // only set by tests: what the connection test opens instead of Steam

	mu       sync.Mutex
	on       bool
	lan      []net.Listener // extra listeners while sharing with other devices
	identify []net.Listener // HTTPS "identify this device" listeners, up while sharing
	bound    []string       // the addresses sharing is listening on
	lanTried []string       // the addresses the last attempt to share used (nil = none yet)
	quit     chan struct{}
	once     sync.Once
	changed  chan struct{} // one pending "state changed" signal for the tray
}

func New(dir, addr, version string) *App {
	a := &App{Version: version, Dir: dir, Addr: addr, Started: time.Now(), quit: make(chan struct{}), changed: make(chan struct{}, 1)}
	a.LANAddrs = lanIPs
	a.Settings = settings.Load(filepath.Join(dir, "settings.json"))
	skip := blacklist.New(filepath.Join(dir, "blacklist.txt"), policy.SkipHeader)
	bypass := blacklist.New(filepath.Join(dir, "bypass.txt"), policy.BypassHeader)
	a.Policy = policy.New(skip, bypass, a.Mode)
	a.Stats = stats.New(filepath.Join(dir, "devices.json"))
	a.Proxy = proxy.New(addr, a.Policy)
	// A newly learned site is logged by the proxy and shown in the dashboard; it never pops up a notification.
	a.Proxy.OnLearn = func(string, policy.Decision) {
		sysproxy.Refresh() // let apps re-read the PAC (never-bypass sites go DIRECT)
	}
	a.Proxy.OnOpen = func(client, hostPort, mode string) uint64 {
		return a.Stats.Open(stats.Event{Client: stats.ClientIP(client), Host: hostPort, Mode: mode})
	}
	a.Proxy.OnClose = func(id uint64, up, down int64, dur time.Duration, err error) {
		text := ""
		if err != nil {
			text = err.Error()
		}
		a.Stats.Close(id, up, down, dur.Milliseconds(), text)
	}
	a.Proxy.OnPAC = func(client string) { a.Stats.SeenPAC(stats.ClientIP(client)) }
	a.Proxy.ProbeHost = identify.ProbeHost
	a.Proxy.OnProbe = func(client string) { a.Stats.SeenProbe(stats.ClientIP(client)) }
	a.Proxy.OnClient = func(client, ua string) {
		ip := stats.ClientIP(client)
		if label, isNew := a.Stats.SeenAgent(ip, ua); isNew {
			logx.Debug.Printf("device %s says: %q => %q", ip, ua, label) // each distinct header once, to learn what devices send
		}
	}
	return a
}

func (a *App) LogPath() string { return filepath.Join(a.Dir, "dpi-info.log") }

// DashboardURL is where the dashboard lives (also reachable by typing it in a browser).
func (a *App) DashboardURL() string { return "http://" + a.Addr + "/" }

// Start serves the proxy on ln, turns the bypass on and watches the site lists.
func (a *App) Start(ln net.Listener) {
	go a.newServer(a.Proxy).Serve(ln)
	a.Enable()
	go a.watch()
}

// Enable points the system at the proxy.
func (a *App) Enable() {
	a.mu.Lock()
	defer a.mu.Unlock()
	sysproxy.Set("http://" + a.Addr + "/proxy.pac")
	a.on = true
	a.notifyChanged()
	logx.Info.Printf("bypass ON (%s), mode %s, %d never-bypass and %d needs-bypass sites", a.Addr, a.Mode(), a.Policy.Skip.Len(), a.Policy.Bypass.Len())
}

// Disable gives the system its previous proxy setting back.
func (a *App) Disable() {
	a.mu.Lock()
	defer a.mu.Unlock()
	sysproxy.Restore()
	a.on = false
	a.notifyChanged()
	logx.Info.Println("bypass OFF, proxy settings restored")
}

// notifyChanged tells the tray the on/off or sharing state changed. It never
// blocks, and a burst of changes leaves one pending signal.
func (a *App) notifyChanged() {
	select {
	case a.changed <- struct{}{}:
	default:
	}
}

// Changed fires after the bypass or LAN sharing state changes, from anywhere
// (tray, dashboard, a new LAN address). Single consumer: the tray.
func (a *App) Changed() <-chan struct{} { return a.changed }

func (a *App) On() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.on
}

func (a *App) Quit()                 { a.once.Do(func() { close(a.quit) }) }
func (a *App) Done() <-chan struct{} { return a.quit }

// watch reloads blacklist.txt and bypass.txt when they change so edits apply without a restart.
func (a *App) watch() {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for tick := 1; ; tick++ {
		select {
		case <-a.quit:
			return
		case <-t.C:
			if tick%15 == 0 { // every 30 seconds
				a.refreshLAN()
			}
			if a.Policy.Reload() {
				logx.Info.Printf("site lists reloaded: %d never-bypass, %d needs-bypass", a.Policy.Skip.Len(), a.Policy.Bypass.Len())
				if a.On() {
					sysproxy.Refresh()
				}
			}
		}
	}
}

// CheckResult is the answer to the Steam test: a plain sentence, and the raw detail for "details".
type CheckResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
	Detail  string `json:"detail,omitempty"`
}

// Check opens Steam through the proxy. It goes through the proxy even while the
// bypass is off for this PC, so the message says which of the two it proved.
func (a *App) Check() CheckResult {
	began := time.Now()
	tr, err := check(a.Addr, a.checkTarget())
	detail := a.checkDetail(tr, err, began)
	if err != nil {
		logx.Info.Printf("test FAILED: %v", err)
		return CheckResult{Message: plainError(err), Detail: detail}
	}
	logx.Info.Printf("test OK: HTTP %d", tr.Status)
	msg := "Steam works through the bypass."
	if !a.On() {
		msg = "Steam works through the bypass, but the bypass is off for this PC. Turn it on to use it."
	}
	return CheckResult{OK: true, Message: msg, Detail: detail}
}

// checkTarget is what the connection test opens (a test can point it elsewhere).
func (a *App) checkTarget() string {
	if a.steamURL != "" {
		return a.steamURL
	}
	return SteamURL
}

// plainError turns a network error into one sentence a person can act on.
func plainError(err error) string {
	var ne net.Error
	msg := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout(), strings.Contains(msg, "timeout"):
		return "No reply in time: the site is blocked or very slow."
	case strings.Contains(msg, "reset by peer"), strings.Contains(msg, "forcibly closed"), strings.Contains(msg, "aborted by the software"), errors.Is(err, io.EOF):
		return "The connection was cut, which is what a site block looks like."
	case strings.Contains(msg, "no such host"):
		return "The name could not be found (DNS problem or no internet)."
	case strings.Contains(msg, "refused"):
		return "The proxy did not answer."
	case strings.Contains(msg, "bad gateway"):
		return "The proxy could not reach Steam."
	}
	return "Steam could not be reached."
}

// check does an HTTPS request through the proxy; any HTTP response proves the TLS handshake passed.
func check(proxyAddr, target string) (checkTrace, error) {
	var tr checkTrace
	start := time.Now()
	var tlsStart time.Time
	trace := &httptrace.ClientTrace{
		TLSHandshakeStart: func() { tlsStart = time.Now(); tr.TunnelAt = time.Since(start) },
		TLSHandshakeDone:  func(_ tls.ConnectionState, _ error) { tr.TLS = time.Since(tlsStart) },
	}
	pu := &url.URL{Scheme: "http", Host: proxyAddr}
	c := &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{Proxy: http.ProxyURL(pu), OnProxyConnectResponse: func(_ context.Context, _ *url.URL, _ *http.Request, res *http.Response) error {
			tr.ConnectCode = res.Status
			if res.StatusCode == http.StatusOK {
				return nil
			}
			// the proxy says why in the body ("no such host", "timeout"...); the default error would only say "Bad Gateway"
			why, _ := io.ReadAll(io.LimitReader(res.Body, 512))
			return fmt.Errorf("%s: %s", res.Status, strings.TrimSpace(string(why)))
		}},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return tr, err
	}
	resp, err := c.Do(req.WithContext(httptrace.WithClientTrace(req.Context(), trace)))
	tr.Total = time.Since(start)
	if err != nil {
		return tr, err
	}
	resp.Body.Close()
	tr.Status, tr.Proto = resp.StatusCode, resp.Proto
	if st := resp.TLS; st != nil {
		tr.TLSVersion = tls.VersionName(st.Version)
		if len(st.PeerCertificates) > 0 {
			tr.Subject, tr.Issuer = proxy.CertName(st.PeerCertificates[0].Subject), proxy.CertName(st.PeerCertificates[0].Issuer)
		}
	}
	return tr, nil
}

// Mode is how unknown sites are handled; an empty or invalid setting means Learn,
// which leaves sites that work normally untouched.
func (a *App) Mode() policy.Mode {
	m, _ := policy.ParseMode(a.Settings.Get().DPIMode)
	return m
}

// SetMode switches between Always and Learn. It only changes what happens for
// sites nothing is known about: every learned decision stays as it is.
func (a *App) SetMode(m policy.Mode) error {
	if _, ok := policy.ParseMode(string(m)); !ok {
		return fmt.Errorf("unknown mode %q (use \"always\" or \"learn\")", m)
	}
	if err := a.Settings.Update(func(s *settings.Settings) { s.DPIMode = string(m) }); err != nil {
		return err
	}
	logx.Info.Printf("DPI mode: %s", m)
	return nil
}
