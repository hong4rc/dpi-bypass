// Package app ties the proxy, blacklist and system-proxy setting together.
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
	"dpi/internal/sysproxy"
)

const SteamURL = "https://store.steampowered.com"

type App struct {
	Version string
	Dir     string // where the exe, logs and blacklist live
	Addr    string
	BL      *blacklist.List
	Proxy   *proxy.Proxy

	mu   sync.Mutex
	on   bool
	quit chan struct{}
	once sync.Once
}

func New(dir, addr, version string) *App {
	a := &App{Version: version, Dir: dir, Addr: addr, quit: make(chan struct{})}
	a.BL = blacklist.New(filepath.Join(dir, "blacklist.txt"))
	a.Proxy = proxy.New(addr, a.BL)
	a.Proxy.OnSkip = func(host string) {
		go notify.Send("DPI bypass", host+" works better without the bypass; added to your skip list.")
		sysproxy.Refresh() // let apps re-read the PAC that now sends the host direct
	}
	return a
}

func (a *App) LogPath() string { return filepath.Join(a.Dir, "dpi-info.log") }

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

// Test opens Steam through the proxy and returns a one-line verdict.
func (a *App) Test() string {
	code, err := check(a.Addr, SteamURL)
	if err != nil {
		logx.Info.Printf("test FAILED: %v", err)
		return "Steam is still BLOCKED or unreachable: " + err.Error()
	}
	logx.Info.Printf("test OK: HTTP %d", code)
	return fmt.Sprintf("Steam works (HTTP %d). The bypass is doing its job.", code)
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
