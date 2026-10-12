//go:build !windows

package stats

import (
	"os"
	"os/exec"
)

func arpOutput() string {
	if b, err := os.ReadFile("/proc/net/arp"); err == nil { // Linux
		return string(b)
	}
	out, _ := exec.Command("arp", "-an").Output() // macOS
	return string(out)
}
