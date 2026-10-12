package policy

import (
	"fmt"
	"testing"
	"time"
)

// Evidence that expired can never count again; it must not pile up over years of running.
func TestExpiredPendingEvidenceIsSweptAway(t *testing.T) {
	r := newRig(t, Learn)
	now := time.Now()
	r.Policy.now = func() time.Time { return now }
	for i := 0; i < pendingSweep+10; i++ {
		r.Observe(fmt.Sprintf("h%d.example.org", i), NeedsBypass) // one hiccup each, never repeated
	}
	now = now.Add(r.Window + time.Minute)
	r.Observe("fresh.example.org", NeedsBypass)
	if n := len(r.Policy.pending); n > 2 {
		t.Fatalf("%d pending observations are still held after they expired", n)
	}
}

func TestLiveEvidenceSurvivesTheSweep(t *testing.T) {
	r := newRig(t, Learn)
	now := time.Now()
	r.Policy.now = func() time.Time { return now }
	r.Observe(someHost, NeedsBypass)
	for i := 0; i < pendingSweep+10; i++ {
		r.Observe(fmt.Sprintf("h%d.example.org", i), NeedsBypass)
	}
	if persisted, _ := r.Observe(someHost, NeedsBypass); !persisted {
		t.Fatal("evidence inside the window was lost by the sweep")
	}
}
