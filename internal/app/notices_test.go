package app

import (
	"strings"
	"sync"
	"testing"
	"time"
)

type sent struct {
	mu   sync.Mutex
	msgs []string
}

func (s *sent) add(_, msg string) { s.mu.Lock(); s.msgs = append(s.msgs, msg); s.mu.Unlock() }
func (s *sent) all() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.msgs...)
}

func newNoticeApp(t *testing.T) (*App, *sent) {
	a := New(t.TempDir(), "127.0.0.1:0", "test")
	s := &sent{}
	a.send = s.add
	a.noticeWindow = 40 * time.Millisecond
	return a, s
}

func TestAutoSkipsInOneWindowAreOneNotification(t *testing.T) {
	a, s := newNoticeApp(t)
	for _, h := range []string{"a.example", "b.example", "c.example", "d.example", "e.example"} {
		a.noteAutoSkip(h)
	}
	time.Sleep(200 * time.Millisecond)

	msgs := s.all()
	if len(msgs) != 1 {
		t.Fatalf("want exactly 1 notification, got %d: %q", len(msgs), msgs)
	}
	if !strings.Contains(msgs[0], "5 sites") || !strings.Contains(msgs[0], "+2 more") {
		t.Fatalf("summary should count the hosts: %q", msgs[0])
	}
}

func TestASingleSkipNamesTheHost(t *testing.T) {
	a, s := newNoticeApp(t)
	a.noteAutoSkip("only.example")
	time.Sleep(200 * time.Millisecond)
	if msgs := s.all(); len(msgs) != 1 || !strings.Contains(msgs[0], "only.example") {
		t.Fatalf("got %q", msgs)
	}
}

func TestALaterSkipStartsANewWindow(t *testing.T) {
	a, s := newNoticeApp(t)
	a.noteAutoSkip("first.example")
	time.Sleep(200 * time.Millisecond)
	a.noteAutoSkip("second.example")
	time.Sleep(200 * time.Millisecond)
	if msgs := s.all(); len(msgs) != 2 {
		t.Fatalf("want 2 notifications (one per window), got %q", msgs)
	}
}

func TestFlushWithNothingPendingSendsNothing(t *testing.T) {
	a, s := newNoticeApp(t)
	a.flushSkipNotice()
	if len(s.all()) != 0 {
		t.Fatal("empty flush must not notify")
	}
}
