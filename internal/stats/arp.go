package stats

import (
	"regexp"
	"strings"
)

var macRe = regexp.MustCompile(`(?i)\b([0-9a-f]{1,2}[:-]){5}[0-9a-f]{1,2}\b`)

// macFor returns the MAC the OS has in its ARP table for ip ("" if unknown).
func macFor(ip string) string { return parseARP(arpOutput(), ip) }

// parseARP finds ip in `arp -a` / /proc/net/arp style output and returns its
// MAC as AA:BB:CC:DD:EE:FF. It handles the Windows, Linux and macOS layouts.
func parseARP(out, ip string) string {
	for _, line := range strings.Split(out, "\n") {
		found := false
		for _, f := range strings.Fields(line) {
			if strings.Trim(f, "()") == ip {
				found = true
				break
			}
		}
		if !found {
			continue
		}
		if m := macRe.FindString(line); m != "" {
			return normalizeMAC(m)
		}
	}
	return ""
}

func normalizeMAC(m string) string {
	parts := strings.FieldsFunc(m, func(r rune) bool { return r == ':' || r == '-' })
	for i, p := range parts {
		if len(p) == 1 { // macOS prints 0a as a
			parts[i] = "0" + p
		}
	}
	return strings.ToUpper(strings.Join(parts, ":"))
}
