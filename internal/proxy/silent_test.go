package proxy

import (
	"testing"

	"dpi/internal/policy"
)

var allHang = func(bool, int) behavior { return bHang }

func TestASiteWhoseDirectHelloWentUnansweredIsTriedWithTheBypassFirst(t *testing.T) {
	shortTimeouts(t)
	u := startUpstream(t, dpiHangsWhole)
	r := newRig(t, policy.Learn, u)

	for i := 1; i <= 2; i++ {
		if answer, _ := r.visit(host, u); answer != "OK" {
			t.Fatalf("visit %d: got %q, want the site to load", i, answer)
		}
	}
	r.wantLearned(policy.NeedsBypass)
	if d := u.directSeen.Load(); d != 1 {
		t.Fatalf("%d direct hellos, want only the first: each one makes DPI block the site for longer", d)
	}
}

func TestASiteOnTheBypassListIsNotTriedDirectlyAfterASilentBypass(t *testing.T) {
	shortTimeouts(t)
	u := startUpstream(t, allHang)
	r := newRig(t, policy.Learn, u)
	r.pol.SetState(host, policy.NeedsBypass, "test")

	if answer, _ := r.visit(host, u); answer != "" {
		t.Fatalf("got %q from a server that never answers", answer)
	}
	if d := u.directSeen.Load(); d != 0 {
		t.Fatalf("%d direct hellos after the bypass got no reply, want none", d)
	}
}

func TestASiteOnTheBypassListStillFallsBackWhenTheServerCutsTheSplit(t *testing.T) {
	shortTimeouts(t)
	u := startUpstream(t, brokenByFragments)
	r := newRig(t, policy.Learn, u)
	r.pol.SetState(host, policy.NeedsBypass, "test")

	if answer, _ := r.visit(host, u); answer != "OK" {
		t.Fatalf("got %q, want the direct fallback to load the site", answer)
	}
}

func TestAnUndecidedSiteInAlwaysModeStillFallsBackAfterASilentBypass(t *testing.T) {
	shortTimeouts(t)
	u := startUpstream(t, allHang)
	r := newRig(t, policy.Always, u)

	r.visit(host, u)
	if d := u.directSeen.Load(); d != 1 {
		t.Fatalf("%d direct hellos, want the usual fallback", d)
	}
}
