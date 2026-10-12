package stats

import (
	"testing"
	"time"
)

func TestAProbeCountsOnlyForTheClientThatSentItAndOnlyForAWhile(t *testing.T) {
	s := New("")
	if s.ProbeSeen("192.168.1.13", time.Minute) {
		t.Fatal("nobody has probed yet")
	}
	s.SeenProbe("192.168.1.13")
	if !s.ProbeSeen("192.168.1.13", time.Minute) {
		t.Fatal("the probe was just seen")
	}
	if s.ProbeSeen("192.168.1.99", time.Minute) {
		t.Fatal("another phone's probe must not count for this one")
	}
	time.Sleep(30 * time.Millisecond)
	if s.ProbeSeen("192.168.1.13", 10*time.Millisecond) {
		t.Fatal("an old probe must not count: the phone may have stopped using the proxy since")
	}
	s.SeenProbe("192.168.1.13")
	if !s.ProbeSeen("192.168.1.13", 10*time.Millisecond) {
		t.Fatal("a fresh probe counts again")
	}
}
