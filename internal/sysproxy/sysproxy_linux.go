package sysproxy

import (
	"os"
	"os/exec"
	"strings"

	"dpi/internal/logx"
)

const schema = "org.gnome.system.proxy"

func gsettings(args ...string) (string, error) {
	out, err := exec.Command("gsettings", args...).Output()
	return strings.TrimSpace(string(out)), err
}

// Set supports GNOME (gsettings). Elsewhere the user sets the PAC URL by hand.
func Set(pac string) {
	mode, err := gsettings("get", schema, "mode")
	if err != nil {
		logx.Info.Printf("gsettings not available: set your system proxy auto-config (PAC) URL to %s manually", pac)
		return
	}
	url, _ := gsettings("get", schema, "autoconfig-url")
	if !isOurs(url) { // never back up our own stale value
		os.WriteFile(backupFile(), []byte(mode+"\n"+url), 0o600)
	}
	gsettings("set", schema, "autoconfig-url", "'"+pac+"'")
	gsettings("set", schema, "mode", "'auto'")
}

func Restore() {
	data, err := os.ReadFile(backupFile())
	if err != nil {
		return
	}
	mode, url, _ := strings.Cut(string(data), "\n") // both keep gsettings' own quoting
	gsettings("set", schema, "autoconfig-url", url)
	gsettings("set", schema, "mode", mode)
}

func Refresh() {}
