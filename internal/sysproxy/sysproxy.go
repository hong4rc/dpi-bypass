// Package sysproxy points the operating system's proxy auto-config (PAC) URL at
// our local proxy and puts the previous setting back afterwards.
//
// Each platform file provides: Set(pac), Restore(), Refresh().
package sysproxy

import (
	"os"
	"path/filepath"
	"strings"
)

// pacName is the PAC path we serve; a setting containing it is ours (possibly stale).
const pacName = "/proxy.pac"

func isOurs(pacURL string) bool { return strings.Contains(pacURL, pacName) }

// backupFile remembers the user's previous setting across crashes.
func backupFile() string {
	d, _ := os.UserConfigDir()
	return filepath.Join(d, "dpi-proxy-backup.txt")
}
