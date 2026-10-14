package tray

import (
	"image/color"
	"os/exec"
	"strings"
)

// leftClickToggles: on macOS a click on the menu bar icon opens its menu, as every
// other menu bar app does; turning the bypass on or off is the first menu item.
const leftClickToggles = false

// autostartLabel is empty: on macOS start at login is `brew services start dpi-bypass`.
const autostartLabel = ""

// makeIcon returns a 44x44 (22 points at 2x) filled-circle PNG, which is what the
// menu bar takes.
func makeIcon(c color.NRGBA) []byte { return circlePNG(c, 44) }

// OpenURL opens url in the default browser.
func OpenURL(url string) { exec.Command("open", url).Start() }

// copyText puts s on the clipboard using pbcopy.
func copyText(s string) {
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(s)
	cmd.Run()
}
