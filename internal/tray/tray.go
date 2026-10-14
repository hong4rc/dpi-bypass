//go:build windows || darwin

// Package tray is the system-tray icon on Windows and the menu bar icon on macOS.
// Quick, simple actions live in its menu and never pop up notifications (the icon
// and checkmarks are the feedback; only real failures notify). Things that need
// room to read or edit (devices, activity, live logs, skip list, Steam test, phone
// setup) live in the dashboard it opens.
package tray

import (
	"image/color"
	"time"

	"fyne.io/systray"

	"dpi/internal/app"
	"dpi/internal/autostart"
	"dpi/internal/logx"
	"dpi/internal/notify"
)

// doubleClickGap is how soon after a tap the next one is ignored.
const doubleClickGap = 500 * time.Millisecond

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
	if leftClickToggles {
		// Left click flips the bypass (the icon colour is the feedback); right click opens the menu.
		var lastTap time.Time
		systray.SetOnTapped(func() {
			if time.Since(lastTap) < doubleClickGap { // a double-click must not flip it twice
				return
			}
			lastTap = time.Now()
			toggle(a)
		})
	}

	mStatus := systray.AddMenuItem("", "")
	mStatus.Disable()
	mToggle := systray.AddMenuItem("", "")
	systray.AddSeparator()
	mLAN := systray.AddMenuItemCheckbox("Share with other devices", "Let phones and other PCs on your Wi-Fi use the bypass", a.LANEnabled())
	mURL := systray.AddMenuItem("", "Click to copy this address")
	systray.AddSeparator()
	mDash := systray.AddMenuItem("Open dashboard", "Devices, activity, logs, skip list, Steam test")
	mClear := systray.AddMenuItem("Clear log", "Empty the log files and the dashboard log")
	var mAuto *systray.MenuItem
	if autostartLabel != "" {
		mAuto = systray.AddMenuItemCheckbox(autostartLabel, "", autostart.Enabled())
	}
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

	// A nil channel never fires, so a platform without the item just never selects it.
	var autostartClicks <-chan struct{}
	if mAuto != nil {
		autostartClicks = mAuto.ClickedCh
	}

	go func() {
		for {
			select {
			case <-a.Changed():
				render()
				renderLAN()
			case <-mToggle.ClickedCh:
				toggle(a)
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
				OpenURL(a.DashboardURL())
			case <-mClear.ClickedCh:
				logx.Clear()
				logx.Info.Println("log cleared from the tray menu")
			case <-autostartClicks:
				if err := autostart.Set(!mAuto.Checked()); err != nil {
					go notify.Send("Cannot change "+autostartLabel, err.Error())
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

// toggle turns the bypass off when it is on and on when it is off.
func toggle(a *app.App) {
	if a.On() {
		a.Disable()
	} else {
		a.Enable()
	}
}
