package stats

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

func arpOutput() string {
	cmd := exec.Command("arp", "-a")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	out, _ := cmd.Output()
	return string(out)
}
