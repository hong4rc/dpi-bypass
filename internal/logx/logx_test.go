package logx

import (
	"strings"
	"testing"
	"time"
)

func TestRecentAndSubscribe(t *testing.T) {
	Init("") // memory only
	ch, cancel := Subscribe()
	defer cancel()

	Info.Println("hello info")
	Debug.Println("hello debug")

	var got []Line
	for len(got) < 2 {
		select {
		case l := <-ch:
			got = append(got, l)
		case <-time.After(time.Second):
			t.Fatalf("only %d live lines: %+v", len(got), got)
		}
	}
	if got[0].Level != "info" || !strings.Contains(got[0].Text, "hello info") || got[1].Level != "debug" {
		t.Fatalf("unexpected live lines: %+v", got)
	}
	if got[1].Seq <= got[0].Seq {
		t.Fatalf("seq must increase: %+v", got)
	}

	recent := Recent(2)
	if len(recent) != 2 || !strings.Contains(recent[1].Text, "hello debug") {
		t.Fatalf("Recent: %+v", recent)
	}
}

func TestRingKeepsOnlyNewestLines(t *testing.T) {
	r := &ring{subs: map[chan Line]struct{}{}}
	for i := 0; i < ringSize+50; i++ {
		r.add("info", "x")
	}
	if len(r.lines) > ringSize+ringSlack || r.lines[len(r.lines)-1].Seq != int64(ringSize+50) {
		t.Fatalf("len=%d last seq=%d", len(r.lines), r.lines[len(r.lines)-1].Seq)
	}
	for i := 0; i < ringSlack; i++ { // past the slack the ring is cut back to ringSize in one go
		r.add("info", "x")
	}
	for i := 0; i < ringSize*3; i++ { // however long it runs, the ring stays between ringSize and ringSize+ringSlack
		r.add("info", "x")
		if n := len(r.lines); n > ringSize+ringSlack {
			t.Fatalf("the ring grew to %d lines", n)
		}
	}
	if n := len(r.lines); n < ringSize || r.lines[len(r.lines)-1].Seq != int64(ringSize+50+ringSlack+ringSize*3) {
		t.Fatalf("after trimming: len=%d last seq=%d", n, r.lines[len(r.lines)-1].Seq)
	}
}

// What the dashboard can fetch never exceeds the documented ring size, whatever the slack holds.
func TestRecentNeverReturnsMoreThanTheRing(t *testing.T) {
	for i := 0; i < ringSize+ringSlack/2; i++ {
		Info.Printf("line %d", i)
	}
	if got := len(Recent(0)); got > ringSize {
		t.Fatalf("Recent(0) returned %d lines, the ring is %d", got, ringSize)
	}
}
