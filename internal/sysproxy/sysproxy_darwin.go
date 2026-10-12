package sysproxy

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
)

func networksetup(args ...string) string {
	out, _ := exec.Command("networksetup", args...).Output()
	return string(out)
}

// services lists network services (Wi-Fi, Ethernet, ...); the first line is a notice.
func services() []string {
	var list []string
	for i, l := range strings.Split(networksetup("-listallnetworkservices"), "\n") {
		if l = strings.TrimSpace(l); i > 0 && l != "" && !strings.HasPrefix(l, "*") {
			list = append(list, l)
		}
	}
	return list
}

// autoURL returns a service's PAC URL when auto-proxy is enabled, else "".
func autoURL(svc string) string {
	var url, enabled string
	for _, l := range strings.Split(networksetup("-getautoproxyurl", svc), "\n") {
		k, v, _ := strings.Cut(l, ": ")
		switch k {
		case "URL":
			url = v
		case "Enabled":
			enabled = v
		}
	}
	if enabled != "Yes" || url == "(null)" {
		return ""
	}
	return url
}

func Set(pac string) {
	prev, stale := map[string]string{}, false
	for _, s := range services() {
		u := autoURL(s)
		prev[s], stale = u, stale || isOurs(u)
	}
	if b, err := json.Marshal(prev); err == nil && !stale { // never back up our own stale value
		os.WriteFile(backupFile(), b, 0o600)
	}
	for s := range prev {
		networksetup("-setautoproxyurl", s, pac)
	}
}

func Restore() {
	data, err := os.ReadFile(backupFile())
	if err != nil {
		return
	}
	prev := map[string]string{}
	if json.Unmarshal(data, &prev) != nil {
		return
	}
	for s, u := range prev {
		if u == "" {
			networksetup("-setautoproxystate", s, "off")
		} else {
			networksetup("-setautoproxyurl", s, u)
		}
	}
}

func Refresh() {}
