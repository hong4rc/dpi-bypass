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
	if len(r.lines) != ringSize || r.lines[len(r.lines)-1].Seq != int64(ringSize+50) {
		t.Fatalf("len=%d last seq=%d", len(r.lines), r.lines[len(r.lines)-1].Seq)
	}
}
