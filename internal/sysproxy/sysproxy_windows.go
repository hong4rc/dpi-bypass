package sysproxy

import (
	"os"
	"strings"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

const inetPath = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

func getAutoConfig() string {
	k, err := registry.OpenKey(registry.CURRENT_USER, inetPath, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	v, _, _ := k.GetStringValue("AutoConfigURL")
	return v
}

func setAutoConfig(v string) {
	k, err := registry.OpenKey(registry.CURRENT_USER, inetPath, registry.SET_VALUE)
	if err != nil {
		return
	}
	defer k.Close()
	if v == "" {
		k.DeleteValue("AutoConfigURL")
		return
	}
	k.SetStringValue("AutoConfigURL", v)
}

// Set points WinINET (Chrome/Edge/Steam/...) at the PAC file, remembering the
// previous auto-config URL.
func Set(pac string) {
	if cur := getAutoConfig(); !isOurs(cur) { // never back up our own stale value
		os.WriteFile(backupFile(), []byte(cur), 0o600)
	}
	setAutoConfig(pac)
	Refresh()
}

// Restore puts the previous setting back.
func Restore() {
	prev, _ := os.ReadFile(backupFile())
	setAutoConfig(strings.TrimSpace(string(prev)))
	Refresh()
}

// Refresh makes running apps re-read the proxy settings and re-download the PAC.
func Refresh() {
	set := syscall.NewLazyDLL("wininet.dll").NewProc("InternetSetOptionW")
	set.Call(0, 39, 0, 0) // INTERNET_OPTION_SETTINGS_CHANGED
	set.Call(0, 37, 0, 0) // INTERNET_OPTION_REFRESH
}
