package app

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// skipNoticeWindow is how long newly learned hosts are collected before one
// notification is sent for all of them.
const skipNoticeWindow = 10 * time.Second

// notice batches "these sites were just learned" messages: a page that opens a
// dozen connections to hosts that all get learned must not produce a dozen
// pop-ups. One notice per kind of decision.
type notice struct {
	mu      sync.Mutex
	hosts   []string
	timer   *time.Timer
	message func(hosts []string) string
}

func newNotices() (skip, bypass *notice) {
	return &notice{message: skipMessage}, &notice{message: bypassMessage}
}

// note records a learned host; the notification goes out once the window closes.
func (a *App) note(n *notice, host string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.hosts = append(n.hosts, host)
	if n.timer == nil {
		n.timer = time.AfterFunc(a.noticeWindow, func() { a.flush(n) })
	}
}

func (a *App) flush(n *notice) {
	n.mu.Lock()
	hosts := n.hosts
	n.hosts, n.timer = nil, nil
	n.mu.Unlock()
	if len(hosts) == 0 {
		return
	}
	a.send("DPI bypass", n.message(hosts))
}

func (a *App) noteAutoSkip(host string)   { a.note(a.skipN, host) }
func (a *App) noteAutoBypass(host string) { a.note(a.bypassN, host) }
func (a *App) flushSkipNotice()           { a.flush(a.skipN) }

func list(hosts []string) string {
	shown := hosts
	if len(shown) > 3 {
		shown = shown[:3]
	}
	more := ""
	if len(hosts) > len(shown) {
		more = fmt.Sprintf(" (+%d more)", len(hosts)-len(shown))
	}
	return strings.Join(shown, ", ") + more
}

func skipMessage(hosts []string) string {
	if len(hosts) == 1 {
		return hosts[0] + " works better without the bypass, so it now connects directly."
	}
	return fmt.Sprintf("%d sites now connect directly: %s. Review them in the dashboard under Sites.", len(hosts), list(hosts))
}

func bypassMessage(hosts []string) string {
	if len(hosts) == 1 {
		return hosts[0] + " failed to connect normally and works with the bypass, so it now uses it."
	}
	return fmt.Sprintf("%d sites now use the bypass: %s. Review them in the dashboard under Sites.", len(hosts), list(hosts))
}
