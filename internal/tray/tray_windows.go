// Package tray is the Windows system-tray UI: status, on/off, test, logs,
// skip list, start with Windows, quit.
package tray

import (
	"image/color"
	"os/exec"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"

	"fyne.io/systray"

	"dpi/internal/app"
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
	mStatus := systray.AddMenuItem("", "")
	mStatus.Disable()
	mToggle := systray.AddMenuItem("", "")
	mTest := systray.AddMenuItem("Test Steam connection", "Open Steam through the bypass and report")
	systray.AddSeparator()
	mLAN := systray.AddMenuItemCheckbox("Allow other devices (iPhone, Android...)", "Share the bypass on your Wi-Fi", a.LANEnabled())
	mURL := systray.AddMenuItem("", "Click to copy this address")
	systray.AddSeparator()
	mLog := systray.AddMenuItem("Open log", "")
	mSkip := systray.AddMenuItem("Edit skip list (blacklist)", "Domains that connect directly")
	mAuto := systray.AddMenuItemCheckbox("Start with Windows", "", autostartEnabled())
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
			case <-mToggle.ClickedCh:
				if a.On() {
					a.Disable()
				} else {
					a.Enable()
				}
				render()
			case <-mTest.ClickedCh:
				go func() { notify.Send("DPI bypass test", a.Test()) }()
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
				url := a.LANURLs()[0]
				copyText(url)
				go notify.Send("Sharing is ON (address copied)", "On the phone: Wi-Fi > (i) > Configure Proxy > Automatic > paste "+url)
			case <-mURL.ClickedCh:
				if urls := a.LANURLs(); len(urls) > 0 {
					copyText(urls[0])
					go notify.Send("Copied", urls[0])
				}
			case <-mLog.ClickedCh:
				exec.Command("notepad", a.LogPath()).Start()
			case <-mSkip.ClickedCh:
				exec.Command("notepad", a.BL.Path()).Start()
			case <-mAuto.ClickedCh:
				if err := setAutostart(!mAuto.Checked()); err == nil {
					if mAuto.Checked() {
						mAuto.Uncheck()
					} else {
						mAuto.Check()
					}
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

// copyText puts s on the clipboard using clip.exe (hidden, no console flash).
func copyText(s string) {
	cmd := exec.Command("clip")
	cmd.Stdin = strings.NewReader(s)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	cmd.Run()
}
