// Package tray is the Windows system-tray icon. Quick, simple actions live in
// its menu and never pop up notifications (the icon and checkmarks are the
// feedback; only real failures notify). Things that need room to read or edit
// (devices, activity, live logs, skip list, Steam test, phone setup) live in the
// dashboard it opens.
package tray

import (
	"image/color"
	"os/exec"
	"strings"
	"syscall"

	"fyne.io/systray"
	"golang.org/x/sys/windows"

	"dpi/internal/app"
	"dpi/internal/autostart"
	"dpi/internal/logx"
	"dpi/internal/notify"
)

var (
	iconOn  = makeIcon(color.NRGBA{R: 0x2e, G: 0xb8, B: 0x5c, A: 0xff})
	iconOff = makeIcon(color.NRGBA{R: 0x8a, G: 0x8a, B: 0x8a, A: 0xff})
)

// Run shows the tray icon and blocks until the app quits.
func Run(a *app.App) {
	go func() { <-a.Done(); systray.Quit() }()
	systray.Run(func() { build(a) }, func() {})
}

func build(a *app.App) {
	systray.SetTooltip("DPI bypass " + a.Version)
	systray.SetOnTapped(func() { openBrowser(a.DashboardURL()) }) // left click

	mStatus := systray.AddMenuItem("", "")
	mStatus.Disable()
	mToggle := systray.AddMenuItem("", "")
	systray.AddSeparator()
	mLAN := systray.AddMenuItemCheckbox("Share with other devices", "Let phones and other PCs on your Wi-Fi use the bypass", a.LANEnabled())
	mURL := systray.AddMenuItem("", "Click to copy this address")
	systray.AddSeparator()
	mDash := systray.AddMenuItem("Open dashboard", "Devices, activity, logs, skip list, Steam test")
	mClear := systray.AddMenuItem("Clear log", "Empty the log files and the dashboard log")
	mAuto := systray.AddMenuItemCheckbox("Start with Windows", "", autostart.Enabled())
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Quit", "")

	render := func() {
		if a.On() {
			systray.SetIcon(iconOn)
			mStatus.SetTitle("● Bypass is ON")
			mToggle.SetTitle("Turn off")
		} else {
			systray.SetIcon(iconOff)
			mStatus.SetTitle("○ Bypass is OFF")
			mToggle.SetTitle("Turn on")
		}
	}
	render()

	renderLAN := func() {
		if urls := a.LANURLs(); len(urls) > 0 {
			mLAN.Check()
			mURL.SetTitle("Copy phone address: " + urls[0])
			mURL.Show()
		} else {
			mLAN.Uncheck()
			mURL.Hide()
		}
	}
	renderLAN()

	go func() {
		for {
			select {
			case <-a.Changed():
				render()
				renderLAN()
			case <-mToggle.ClickedCh:
				if a.On() {
					a.Disable()
				} else {
					a.Enable()
				}
				render()
			case <-mLAN.ClickedCh:
				if a.LANEnabled() {
					a.DisableLAN()
					renderLAN()
					break
				}
				if err := a.EnableLAN(); err != nil {
					go notify.Send("Cannot share with other devices", err.Error())
					break
				}
				renderLAN()
				if urls := a.LANURLs(); len(urls) > 0 {
					copyText(urls[0]) // silently: the checked menu item is the feedback
				}
			case <-mURL.ClickedCh:
				if urls := a.LANURLs(); len(urls) > 0 {
					copyText(urls[0])
				}
			case <-mDash.ClickedCh:
				openBrowser(a.DashboardURL())
			case <-mClear.ClickedCh:
				logx.Clear()
				logx.Info.Println("log cleared from the tray menu")
			case <-mAuto.ClickedCh:
				if err := autostart.Set(!mAuto.Checked()); err != nil {
					go notify.Send("Cannot change start with Windows", err.Error())
					break
				}
				if mAuto.Checked() {
					mAuto.Uncheck()
				} else {
					mAuto.Check()
				}
			case <-mQuit.ClickedCh:
				a.Quit()
				return
			case <-a.Done():
				return
			}
		}
	}()
}

// openBrowser opens url in the default browser without flashing a console window.
func openBrowser(url string) {
	cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	cmd.Start()
}

// copyText puts s on the clipboard using clip.exe (hidden, no console flash).
func copyText(s string) {
	cmd := exec.Command("clip")
	cmd.Stdin = strings.NewReader(s)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	cmd.Run()
}
