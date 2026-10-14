package tray

import (
	"bytes"
	"encoding/binary"
	"image/color"
	"os/exec"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

// leftClickToggles: on Windows a left click flips the bypass and a right click opens the menu.
const leftClickToggles = true

// autostartLabel is the text of the start-at-login menu item.
const autostartLabel = "Start with Windows"

// makeIcon returns a 32x32 filled-circle .ico (PNG payload) so no binary assets are needed.
func makeIcon(c color.NRGBA) []byte {
	p := circlePNG(c, 32)
	var b bytes.Buffer
	binary.Write(&b, binary.LittleEndian, []uint16{0, 1, 1}) // ICONDIR: reserved, type=icon, count
	b.Write([]byte{32, 32, 0, 0})                            // width, height, palette, reserved
	binary.Write(&b, binary.LittleEndian, []uint16{1, 32})   // planes, bits per pixel
	binary.Write(&b, binary.LittleEndian, uint32(len(p)))    // image size
	binary.Write(&b, binary.LittleEndian, uint32(22))        // image offset
	b.Write(p)
	return b.Bytes()
}

// OpenURL opens url in the default browser without flashing a console window.
func OpenURL(url string) {
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
