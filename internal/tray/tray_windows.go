// Package tray is the Windows system-tray UI: status, on/off, test, logs,
// skip list, start with Windows, quit.
package tray

import (
	"image/color"
	"os/exec"

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
