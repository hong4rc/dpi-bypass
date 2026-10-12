// Package app ties the proxy, blacklist, stats and system-proxy setting together.
package app

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"sync"
	"time"

	"dpi/internal/blacklist"
	"dpi/internal/logx"
	"dpi/internal/notify"
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
	BL       *blacklist.List
	Proxy    *proxy.Proxy
	Stats    *stats.Store
	Settings *settings.Store
	LANAddrs func() []string // addresses to share on: the PC's private LAN addresses unless a test swaps it

	notice       skipNotice
	noticeWindow time.Duration
	send         func(title, msg string) // desktop notification; replaced in tests

	mu       sync.Mutex
	on       bool
	lan      []net.Listener // extra listeners while sharing with other devices
	identify []net.Listener // HTTPS "identify this device" listeners, up while sharing
	quit     chan struct{}
	once     sync.Once
}

func New(dir, addr, version string) *App {
	a := &App{Version: version, Dir: dir, Addr: addr, Started: time.Now(), quit: make(chan struct{}),
		noticeWindow: skipNoticeWindow, send: notify.Send}
	a.LANAddrs = lanIPs
	a.BL = blacklist.New(filepath.Join(dir, "blacklist.txt"))
	a.Stats = stats.New(filepath.Join(dir, "devices.json"))
	a.Settings = settings.Load(filepath.Join(dir, "settings.json"))
	a.migrateLANMarker()
	a.Proxy = proxy.New(addr, a.BL)
	a.Proxy.OnSkip = func(host string) {
		a.noteAutoSkip(host)
		sysproxy.Refresh() // let apps re-read the PAC that now sends the host direct
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

// Start serves the proxy on ln, turns the bypass on and watches the blacklist file.
func (a *App) Start(ln net.Listener) {
	go http.Serve(ln, a.Proxy)
	a.Enable()
	go a.watch()
}

// Enable points the system at the proxy.
func (a *App) Enable() {
	a.mu.Lock()
	defer a.mu.Unlock()
	sysproxy.Set("http://" + a.Addr + "/proxy.pac")
	a.on = true
	logx.Info.Printf("bypass ON (%s), skip list: %v", a.Addr, a.BL.Items())
}

// Disable gives the system its previous proxy setting back.
func (a *App) Disable() {
	a.mu.Lock()
	defer a.mu.Unlock()
	sysproxy.Restore()
	a.on = false
	logx.Info.Println("bypass OFF, proxy settings restored")
}

func (a *App) On() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.on
}

func (a *App) Quit()                 { a.once.Do(func() { close(a.quit) }) }
func (a *App) Done() <-chan struct{} { return a.quit }

// watch reloads blacklist.txt when it changes so edits apply without a restart.
func (a *App) watch() {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-a.quit:
			return
		case <-t.C:
			if a.BL.Reload() {
				logx.Info.Printf("skip list reloaded: %v", a.BL.Items())
				if a.On() {
					sysproxy.Refresh()
				}
			}
		}
	}
}

// Check opens Steam through the proxy and returns whether it worked, with a one-line verdict.
func (a *App) Check() (bool, string) {
	code, err := check(a.Addr, SteamURL)
	if err != nil {
		logx.Info.Printf("test FAILED: %v", err)
		return false, "Steam is still BLOCKED or unreachable: " + err.Error()
	}
	logx.Info.Printf("test OK: HTTP %d", code)
	return true, fmt.Sprintf("Steam works (HTTP %d). The bypass is doing its job.", code)
}

// check does an HTTPS request through the proxy; any HTTP response proves the TLS handshake passed.
func check(proxyAddr, target string) (int, error) {
	pu := &url.URL{Scheme: "http", Host: proxyAddr}
	c := &http.Client{
		Timeout:       15 * time.Second,
		Transport:     &http.Transport{Proxy: http.ProxyURL(pu)},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := c.Get(target)
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	return resp.StatusCode, nil
}
