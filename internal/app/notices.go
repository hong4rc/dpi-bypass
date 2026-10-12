package app

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// skipNoticeWindow is how long auto-skipped hosts are collected before one
// notification is sent for all of them.
const skipNoticeWindow = 10 * time.Second

// skipNotice batches "host now connects directly" messages: a page that opens
// a dozen connections to hosts that all need skipping must not produce a dozen
// pop-ups.
type skipNotice struct {
	mu    sync.Mutex
	hosts []string
	timer *time.Timer
}

// noteAutoSkip records an auto-skipped host; the notification goes out once the window closes.
func (a *App) noteAutoSkip(host string) {
	n := &a.notice
	n.mu.Lock()
	defer n.mu.Unlock()
	n.hosts = append(n.hosts, host)
	if n.timer == nil {
		n.timer = time.AfterFunc(a.noticeWindow, a.flushSkipNotice)
	}
}

func (a *App) flushSkipNotice() {
	n := &a.notice
	n.mu.Lock()
	hosts := n.hosts
	n.hosts, n.timer = nil, nil
	n.mu.Unlock()
	if len(hosts) == 0 {
		return
	}
	a.send("DPI bypass", skipMessage(hosts))
}

func skipMessage(hosts []string) string {
	if len(hosts) == 1 {
		return hosts[0] + " works better without the bypass, so it now connects directly."
	}
	shown := hosts
	if len(shown) > 3 {
		shown = shown[:3]
	}
	more := ""
	if len(hosts) > len(shown) {
		more = fmt.Sprintf(" (+%d more)", len(hosts)-len(shown))
	}
	return fmt.Sprintf("%d sites now connect directly: %s%s. Review them in the dashboard under Skip list.", len(hosts), strings.Join(shown, ", "), more)
}
