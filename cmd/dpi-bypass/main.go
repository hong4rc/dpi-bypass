// dpi-bypass: local proxy that beats SNI-based DPI blocking (e.g. Steam) by
// splitting the TLS ClientHello. User-mode only: no driver, no admin needed.
//
// Files next to the exe (logs are recreated on each start):
//
//	blacklist.txt  domains that skip the bypass and connect directly
//	dpi-info.log   start/stop + failures
//	dpi-debug.log  everything, incl. every connection
package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"dpi/internal/app"
	"dpi/internal/logx"
	"dpi/internal/notify"
	"dpi/internal/sysproxy"
	"dpi/internal/tray"
)

const addr = "127.0.0.1:18080"

var version = "dev" // set with -ldflags "-X main.version=..."

func main() {
	restore := flag.Bool("restore", false, "restore the system proxy setting and exit")
	guard := flag.Int("guard", 0, "internal: restore the proxy setting when this process id exits")
	showVersion := flag.Bool("version", false, "print version and exit")
	lan := flag.Bool("lan", false, "share the bypass with other devices on your LAN (remembered)")
	noLAN := flag.Bool("no-lan", false, "stop sharing with other devices (remembered)")
	flag.Parse()

	switch {
	case *showVersion:
		fmt.Println(version)
		return
	case *restore:
		sysproxy.Restore()
		fmt.Println("System proxy settings restored.")
		return
	case *guard > 0:
		sysproxy.RunGuard(*guard)
		return
	}

	// Take the port first: it doubles as the single-instance lock, and a second
	// copy must not truncate the running copy's logs.
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		notify.Send("DPI bypass", "Could not start: is it already running? ("+addr+" is busy)")
		os.Exit(1)
	}
	exe, _ := os.Executable()
	dir := filepath.Dir(exe)
	logx.Init(dir)

	a := app.New(dir, addr, version)
	a.Start(ln)
	if *noLAN {
		a.DisableLAN()
	} else if *lan || a.LANWanted() {
		if err := a.EnableLAN(); err != nil {
			logx.Info.Printf("LAN sharing not started: %v", err)
		}
	}
	defer a.Disable() // also restores on a panic in this goroutine
	sysproxy.StartGuard()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() { <-sig; a.Quit() }()

	logx.Info.Printf("dpi-bypass %s running. Close it from the tray menu (or Ctrl+C in a terminal).", version)
	go notify.Send("DPI bypass is ON", "Blocked sites like Steam should work now.")
	tray.Run(a)
}
