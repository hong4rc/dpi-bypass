package stats

import (
	"regexp"
	"strings"
)

var (
	// Android: "Linux; Android 14; SM-S918B Build/UP1A..." (system PAC fetch) or "Android 13; Pixel 7)" (Chrome).
	reAndroid = regexp.MustCompile(`Android ([\d.]+)(?:; ([^;)]+))?`)
	// iOS: "iPhone; CPU iPhone OS 17_2 like Mac OS X" / "iPad; CPU OS 16_6 like Mac OS X".
	reIOS = regexp.MustCompile(`OS (\d+)[_.](\d+)`)
)

// Label turns a User-Agent into a short device description such as
// "Android 14 · SM-S918B" or "iPhone · iOS 17.2". It returns "" when the agent
// says nothing about the device (curl, Go, unknown apps).
func Label(ua string) string {
	switch {
	case strings.Contains(ua, "iPhone") || strings.Contains(ua, "iPad") || strings.Contains(ua, "iPod"):
		kind := "iPhone"
		if strings.Contains(ua, "iPad") {
			kind = "iPad"
		} else if strings.Contains(ua, "iPod") {
			kind = "iPod"
		}
		if m := reIOS.FindStringSubmatch(ua); m != nil {
			return kind + " · iOS " + m[1] + "." + m[2]
		}
		return kind
	case strings.Contains(ua, "Android"):
		m := reAndroid.FindStringSubmatch(ua)
		if m == nil {
			return "Android"
		}
		label := "Android " + m[1]
		// the model may be followed by " Build/..." and Chrome's reduced UA uses a placeholder like "K"
		model := strings.TrimSpace(strings.SplitN(m[2], " Build/", 2)[0])
		if len(model) > 2 && model != "Mobile" {
			label += " · " + model
		}
		return label
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
