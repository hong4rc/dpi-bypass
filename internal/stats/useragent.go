package stats

import (
	"regexp"
	"strings"
)

var (
	reAndroidVersion = regexp.MustCompile(`Android ([\d.]+)`)
	// the "(...)" part that follows the Android version: "Android 14; SM-S918B Build/UP1A..." or
	// "Android 10; en_US; SM-G973F Build/QP1A...; Cronet/121"
	reAndroidParts = regexp.MustCompile(`Android [\d.]+;([^)]*)\)`)
	reLocale       = regexp.MustCompile(`^[a-z]{2,3}[_-][A-Za-z]{2,4}$`)
	reFBDevice     = regexp.MustCompile(`FBDV/([^;/\]]+)`) // Facebook / Messenger app: FBDV/SM-G973F
	// iOS: "iPhone; CPU iPhone OS 17_2", "iPad; CPU OS 16_6", "CPU iOS 17_2"
	reIOSVersion = regexp.MustCompile(`(?:OS|iOS) (\d+)[_.](\d+)`)
	// iOS apps: "(iPhone14,5; U; CPU iOS 17_2 ...)" carries Apple's model identifier
	reAppleModel = regexp.MustCompile(`(iPhone|iPad|iPod)(\d+,\d+)`)
)

// Label turns a User-Agent into a short device description such as
// "Android 14 · SM-S918B" or "iPhone · iOS 17.2". It returns "" when the agent
// says nothing about the device (curl, Go, unknown apps).
func Label(ua string) string {
	switch {
	case strings.Contains(ua, "iPhone") || strings.Contains(ua, "iPad") || strings.Contains(ua, "iPod"):
		return appleLabel(ua)
	case strings.Contains(ua, "Android"):
		return androidLabel(ua)
	case strings.Contains(ua, "Windows NT"):
		return "Windows PC"
	case strings.Contains(ua, "Macintosh"):
		return "Mac"
	case strings.Contains(ua, "CrOS"):
		return "Chromebook"
	case strings.Contains(ua, "Linux") || strings.Contains(ua, "X11"):
		return "Linux PC"
	case strings.Contains(ua, "CFNetwork") || strings.Contains(ua, "Darwin"):
		return "Apple device"
	}
	return ""
}

func appleLabel(ua string) string {
	kind := "iPhone"
	switch {
	case strings.Contains(ua, "iPad"):
		kind = "iPad"
	case strings.Contains(ua, "iPod"):
		kind = "iPod"
	}
	label := kind
	if m := reAppleModel.FindStringSubmatch(ua); m != nil {
		label = m[1] + m[2] // Apple's identifier, e.g. iPhone14,5
	}
	if m := reIOSVersion.FindStringSubmatch(ua); m != nil {
		label += " · iOS " + m[1] + "." + m[2]
	}
	return label
}

func androidLabel(ua string) string {
	label := "Android"
	// Chrome freezes its header to "Android 10; K" whatever the phone really runs
	// (a real Android 12 phone sends exactly this), so that version means nothing.
	reduced := strings.Contains(ua, "Android 10; K)")
	if m := reAndroidVersion.FindStringSubmatch(ua); m != nil && !reduced {
		label += " " + m[1]
	}
	if model := androidModel(ua); model != "" {
		label += " · " + model
	}
	return label
}

// androidModel finds the phone model if the agent carries one. Chrome's reduced
// User-Agent only says "Android 10; K", which tells nothing, so it yields "".
func androidModel(ua string) string {
	if m := reFBDevice.FindStringSubmatch(ua); m != nil {
		return strings.TrimSpace(m[1])
	}
	m := reAndroidParts.FindStringSubmatch(ua)
	if m == nil {
		return ""
	}
	for _, part := range strings.Split(m[1], ";") {
		part = strings.TrimSpace(strings.SplitN(part, " Build/", 2)[0])
		switch {
		case len(part) <= 2, // "K" and similar placeholders
			part == "wv", part == "U", part == "Mobile", part == "Tablet",
			reLocale.MatchString(part),
			strings.Contains(part, "/"): // "Cronet/121..." and other app tokens
			continue
		}
		return part
	}
	return ""
}
