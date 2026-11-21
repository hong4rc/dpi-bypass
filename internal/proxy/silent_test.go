package proxy

import (
	"strings"
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
	if s := u.splitSeen.Load(); s != 2 {
		t.Fatalf("%d split hellos, want the bypass tried once more", s)
	}
}

func TestASiteOnTheBypassListLoadsWhenDPIMissesOnlyTheFirstSplit(t *testing.T) {
	shortTimeouts(t)
	u := startUpstream(t, func(frag bool, n int) behavior {
		if frag && n > 0 {
			return bOK
		}
		return bHang
	})
	r := newRig(t, policy.Learn, u)
	r.pol.SetState(host, policy.NeedsBypass, "test")

	if answer, mode := r.visit(host, u); answer != "OK" || mode != ModeBypass {
		t.Fatalf("got %q (%s), want the second bypass to load the site", answer, mode)
	}
	r.wantNothingLearned()
	if d := u.directSeen.Load(); d != 0 {
		t.Fatalf("%d direct hellos, want none", d)
	}
}

func TestASilentSiteThatLoadsOnTheSecondSplitIsNotLearnedAsNoBypass(t *testing.T) {
	shortTimeouts(t)
	u := startUpstream(t, func(frag bool, n int) behavior {
		if frag && n == 1 { // the first split (after the silent direct hello) works, the next one is missed once
			return bHang
		}
		if frag {
			return bOK
		}
		return bHang
	})
	r := newRig(t, policy.Learn, u)

	r.visit(host, u) // direct goes silent, the bypass loads it
	if answer, _ := r.visit(host, u); answer != "OK" {
		t.Fatalf("got %q, want the retried bypass to load the site", answer)
	}
	if strings.Contains(r.file("blacklist.txt"), host) {
		t.Fatal("a site that only loads with the bypass must never be put on the never-bypass list")
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
