package stats

import "testing"

func TestLabelFromUserAgent(t *testing.T) {
	for name, tc := range map[string]struct{ ua, want string }{
		"android system PAC fetch": {"Dalvik/2.1.0 (Linux; U; Android 14; SM-S918B Build/UP1A.231005.007)", "Android 14 · SM-S918B"},
		"android chrome model":     {"Mozilla/5.0 (Linux; Android 13; Pixel 7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Mobile Safari/537.36", "Android 13 · Pixel 7"},
		"android reduced UA":       {"Mozilla/5.0 (Linux; Android 10; K) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Mobile Safari/537.36", "Android 10"},
		"android no model":         {"Mozilla/5.0 (Linux; Android 12) Chrome/120", "Android 12"},
		"iphone safari":            {"Mozilla/5.0 (iPhone; CPU iPhone OS 17_2 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.2 Mobile/15E148 Safari/604.1", "iPhone · iOS 17.2"},
		"ipad":                     {"Mozilla/5.0 (iPad; CPU OS 16_6 like Mac OS X) AppleWebKit/605.1.15", "iPad · iOS 16.6"},
		"iphone app":               {"MyApp/1.0 CFNetwork/1485 Darwin/23.1.0 (iPhone)", "iPhone"},
		"windows":                  {"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36 Edg/120.0.0.0", "Windows PC"},
		"mac":                      {"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15", "Mac"},
		"chromebook":               {"Mozilla/5.0 (X11; CrOS x86_64 14541.0.0) AppleWebKit/537.36", "Chromebook"},
		"linux":                    {"Mozilla/5.0 (X11; Linux x86_64; rv:121.0) Gecko/20100101 Firefox/121.0", "Linux PC"},
		"apple network stack":      {"com.apple.WebKit.Networking/8617.1.17 CFNetwork/1485 Darwin/23.1.0", "Apple device"},
		"go client":                {"Go-http-client/1.1", ""},
		"curl":                     {"curl/8.4.0", ""},
		"empty":                    {"", ""},
	} {
		if got := Label(tc.ua); got != tc.want {
			t.Errorf("%s: Label(%q)=%q want %q", name, tc.ua, got, tc.want)
		}
	}
}
