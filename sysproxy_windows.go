package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

const inetKey = `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`

func backupFile() string {
	d, _ := os.UserConfigDir()
	return filepath.Join(d, "dpi-proxy-backup.txt")
}

func regQuery(name string) string {
	out, err := exec.Command("reg", "query", inetKey, "/v", name).Output()
	if err != nil {
		return ""
	}
	f := strings.Fields(string(out))
	if len(f) >= 3 {
		return strings.Join(f[2:], " ")
	}
	return ""
}

func regSet(name, val string) {
	if val == "" {
		exec.Command("reg", "delete", inetKey, "/v", name, "/f").Run()
		return
	}
	exec.Command("reg", "add", inetKey, "/v", name, "/t", "REG_SZ", "/d", val, "/f").Run()
}

// setSystemProxy points WinINET (Chrome/Edge/Steam/...) at our PAC file,
// remembering the previous auto-config URL so it can be restored.
func setSystemProxy(pac string) {
	cur := regQuery("AutoConfigURL")
	if !strings.Contains(cur, "/proxy.pac") { // don't back up our own stale value
		os.WriteFile(backupFile(), []byte(cur), 0o600)
	}
	regSet("AutoConfigURL", pac)
	refreshWinInet()
}

func restoreSystemProxy() {
	prev, _ := os.ReadFile(backupFile())
	regSet("AutoConfigURL", strings.TrimSpace(string(prev)))
	refreshWinInet()
}

func refreshWinInet() {
	set := syscall.NewLazyDLL("wininet.dll").NewProc("InternetSetOptionW")
	set.Call(0, 39, 0, 0) // INTERNET_OPTION_SETTINGS_CHANGED
	set.Call(0, 37, 0, 0) // INTERNET_OPTION_REFRESH
}
