package sysproxy

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"

	"golang.org/x/sys/windows"

	"dpi/internal/logx"
)

// StartGuard launches a hidden copy of this exe that restores the proxy setting
// as soon as the current process ends, however it ends (crash, kill, ...).
func StartGuard() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(exe, "-guard", strconv.Itoa(os.Getpid()))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	if err := cmd.Start(); err != nil {
		logx.Info.Printf("crash guard not started: %v", err)
		return
	}
	logx.Debug.Printf("crash guard started, pid %d watching pid %d", cmd.Process.Pid, os.Getpid())
}

// RunGuard blocks until process pid exits, then restores the proxy setting if
// it is still ours.
func RunGuard(pid int) {
	if h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid)); err == nil {
		windows.WaitForSingleObject(h, windows.INFINITE)
		windows.CloseHandle(h)
	}
	if isOurs(getAutoConfig()) {
		Restore()
	}
}
