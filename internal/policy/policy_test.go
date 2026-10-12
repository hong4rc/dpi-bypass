package policy

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"dpi/internal/blacklist"
)

// rig is a policy over two real files in a temp dir, with a mode the test can flip.
type rig struct {
	t    *testing.T
	dir  string
	mode Mode
	*Policy
}

func newRig(t *testing.T, mode Mode) *rig {
	t.Helper()
	r := &rig{t: t, dir: t.TempDir(), mode: mode}
	r.Policy = r.open()
	return r
}

// open builds a fresh Policy over the same files, as a restart of the app would.
func (r *rig) open() *Policy {
	skip := blacklist.New(filepath.Join(r.dir, "blacklist.txt"), SkipHeader)
	bypass := blacklist.New(filepath.Join(r.dir, "bypass.txt"), BypassHeader)
	return New(skip, bypass, func() Mode { return r.mode })
}

func (r *rig) file(name string) string {
	data, _ := os.ReadFile(filepath.Join(r.dir, name))
	return string(data)
}

func (r *rig) set(host string, d Decision) {
	r.t.Helper()
	if err := r.SetState(host, d, "set by test"); err != nil {
		r.t.Fatal(err)
	}
}

var (
	direct      = Plan{First: Direct, Fallback: None, Learn: Unknown}
	splitKnown  = Plan{First: Split, Fallback: Direct, Learn: Unknown}
	alwaysUnk   = Plan{First: Split, Fallback: Direct, Learn: NoBypass}
	learnUnk    = Plan{First: Direct, Fallback: Split, Learn: NeedsBypass}
	anyUnknown  = "never-seen.example"
	someHost    = "site.example"
	learnedHost = "learned.example"
)

// --- unknown sites follow the mode -------------------------------------------------

func TestUnknownSitesFollowTheSelectedMode(t *testing.T) {
	r := newRig(t, Always)
	if got := r.PlanFor(anyUnknown); got != alwaysUnk {
		t.Errorf("Always: unknown site plan %+v, want split first, learn NO_BYPASS from a working direct fallback", got)
	}
	r.mode = Learn
	if got := r.PlanFor(anyUnknown); got != learnUnk {
		t.Errorf("Learn: unknown site plan %+v, want direct first, learn NEEDS_BYPASS from a working split fallback", got)
	}
	if r.Decide(anyUnknown) != Unknown {
		t.Error("a site nothing is known about must be UNKNOWN in both modes")
	}
}

// --- known sites behave the same in both modes ---------------------------------------

func TestNeedsBypassIsBypassedInBothModes(t *testing.T) {
	for _, m := range []Mode{Always, Learn} {
		r := newRig(t, m)
		r.set(someHost, NeedsBypass)
		if got := r.Decide(someHost); got != NeedsBypass {
			t.Errorf("%s: decision %v", m, got)
		}
		if got := r.PlanFor(someHost); got != splitKnown {
			t.Errorf("%s: plan %+v, want the bypass first and nothing learned from the fallback", m, got)
		}
		if got := r.PlanFor("api." + someHost); got != splitKnown {
			t.Errorf("%s: a subdomain of a NEEDS_BYPASS site is covered too, got %+v", m, got)
		}
	}
}

func TestNoBypassIsNeverBypassedInBothModes(t *testing.T) {
	for _, m := range []Mode{Always, Learn} {
		r := newRig(t, m)
		r.set(someHost, NoBypass)
		if got := r.Decide(someHost); got != NoBypass {
			t.Errorf("%s: decision %v", m, got)
		}
		if got := r.PlanFor(someHost); got != direct {
			t.Errorf("%s: plan %+v, want direct only (no bypass attempt, no fallback)", m, got)
		}
	}
}

func TestNoBypassTakesPrecedenceOverEverythingElse(t *testing.T) {
	r := newRig(t, Always) // Always would bypass by default
	// a host listed in both files (edited by hand): never bypassed
	os.WriteFile(filepath.Join(r.dir, "blacklist.txt"), []byte("clash.example\n"), 0o644)
	os.WriteFile(filepath.Join(r.dir, "bypass.txt"), []byte("clash.example\nunder.example\n"), 0o644)
	r.Policy = r.open()
	if got := r.Decide("clash.example"); got != NoBypass {
		t.Errorf("NO_BYPASS must win over NEEDS_BYPASS, got %v", got)
	}
	// ...and over the Always default
	if got := r.PlanFor("clash.example"); got != direct {
		t.Errorf("plan %+v", got)
	}
	// a NO_BYPASS parent covers its subdomains, even those a NEEDS_BYPASS entry names
	os.WriteFile(filepath.Join(r.dir, "blacklist.txt"), []byte("example\n"), 0o644)
	future := time.Now().Add(2 * time.Second)
	os.Chtimes(filepath.Join(r.dir, "blacklist.txt"), future, future)
	r.Reload()
	if got := r.Decide("under.example"); got != NoBypass {
		t.Errorf("parent NO_BYPASS must cover under.example, got %v", got)
	}
}

// --- switching modes never touches what was learned ---------------------------------------

// Learn -> Always -> Learn
func TestANeedsBypassSiteSurvivesLearnToAlwaysAndBack(t *testing.T) {
	r := newRig(t, Learn)
	persistTwice(t, r, learnedHost, NeedsBypass)
	r.mode = Always
	if r.Decide(learnedHost) != NeedsBypass || r.PlanFor(learnedHost) != splitKnown {
		t.Fatal("after switching to Always the site must still use the bypass")
	}
	r.mode = Learn
	if r.Decide(learnedHost) != NeedsBypass || r.PlanFor(learnedHost) != splitKnown {
		t.Fatal("after switching back to Learn the site is still known: nothing to relearn")
	}
	if !strings.Contains(r.file("bypass.txt"), learnedHost) {
		t.Fatal("the decision must have stayed in the shared file")
	}
}

// Always -> Learn
func TestANoBypassSiteSurvivesAlwaysToLearn(t *testing.T) {
	r := newRig(t, Always)
	persistTwice(t, r, learnedHost, NoBypass)
	r.mode = Learn
	if r.Decide(learnedHost) != NoBypass || r.PlanFor(learnedHost) != direct {
		t.Fatal("Learn normally starts direct, but must not discard the NO_BYPASS decision: it must stay direct with no bypass retry")
	}
	r.mode = Always
	if r.Decide(learnedHost) != NoBypass || r.PlanFor(learnedHost) != direct {
		t.Fatal("and back in Always it is still never bypassed")
	}
}

func TestSwitchingModesChangesOnlyTheDefaultForUnknownSites(t *testing.T) {
	r := newRig(t, Always)
	r.set("a.example", NeedsBypass)
	r.set("b.example", NoBypass)
	before := r.file("blacklist.txt") + "|" + r.file("bypass.txt")
	for _, m := range []Mode{Learn, Always, Learn, Learn, Always} {
		r.mode = m
		_ = r.PlanFor(anyUnknown)
		_ = r.Decide("a.example")
	}
	if after := r.file("blacklist.txt") + "|" + r.file("bypass.txt"); after != before {
		t.Fatalf("switching modes rewrote the learned data:\nbefore %q\nafter  %q", before, after)
	}
}

// --- persistence ---------------------------------------------------------------------

func TestDecisionsSurviveARestartInBothModes(t *testing.T) {
	r := newRig(t, Learn)
	persistTwice(t, r, "needs.example", NeedsBypass)
	r.set("never.example", NoBypass)

	for _, m := range []Mode{Learn, Always} {
		r.mode = m
		restarted := r.open() // new process, same files
		if restarted.Decide("needs.example") != NeedsBypass || restarted.Decide("never.example") != NoBypass {
			t.Errorf("%s: decisions lost on restart", m)
		}
		if restarted.Decide(anyUnknown) != Unknown {
			t.Errorf("%s: an unknown site became known", m)
		}
	}
}

func TestEvidenceNeverPersistedIsLostOnRestart(t *testing.T) {
	r := newRig(t, Learn)
	if persisted, _ := r.Observe(someHost, NeedsBypass); persisted {
		t.Fatal("one observation must not be written")
	}
	restarted := r.open()
	if restarted.Decide(someHost) != Unknown {
		t.Fatal("unconfirmed evidence must not become a decision")
	}
	if persisted, _ := restarted.Observe(someHost, NeedsBypass); persisted {
		t.Fatal("pending evidence lives in memory only: after a restart it starts again from zero")
	}
}

// --- the evidence rule: transient failures must not become permanent ----------------------------

func TestOneObservationIsNotEnoughButTwoAre(t *testing.T) {
	r := newRig(t, Learn)
	before := r.file("bypass.txt")
	persisted, err := r.Observe(someHost, NeedsBypass)
	if err != nil || persisted {
		t.Fatalf("first observation: persisted=%v err=%v", persisted, err)
	}
	if r.file("bypass.txt") != before || r.Decide(someHost) != Unknown {
		t.Fatal("a single failure followed by a working retry looks like a network hiccup: nothing may be written")
	}
	persisted, err = r.Observe(someHost, NeedsBypass)
	if err != nil || !persisted {
		t.Fatalf("second observation: persisted=%v err=%v", persisted, err)
	}
	if r.Decide(someHost) != NeedsBypass || !strings.Contains(r.file("bypass.txt"), someHost+" # learned ") {
		t.Fatalf("not persisted with its note: %q", r.file("bypass.txt"))
	}
	if persisted, _ := r.Observe(someHost, NeedsBypass); persisted {
		t.Fatal("an already known site must not be announced again")
	}
}

func TestEvidenceOutsideTheWindowStartsOver(t *testing.T) {
	r := newRig(t, Learn)
	now := time.Now()
	r.Policy.now = func() time.Time { return now }
	r.Observe(someHost, NeedsBypass)
	now = now.Add(r.Window + time.Minute) // too long ago to count
	if persisted, _ := r.Observe(someHost, NeedsBypass); persisted {
		t.Fatal("two observations an hour apart are two separate hiccups, not evidence")
	}
	now = now.Add(time.Minute)
	if persisted, _ := r.Observe(someHost, NeedsBypass); !persisted {
		t.Fatal("two observations inside the window are")
	}
}

func TestASuccessfulFirstAttemptClearsPendingEvidence(t *testing.T) {
	r := newRig(t, Learn)
	plan := r.PlanFor(someHost)
	r.Observe(someHost, plan.Learn) // failed once, the bypass worked
	r.Succeeded(someHost, plan)     // next time the normal connection simply worked: it was a hiccup
	if persisted, _ := r.Observe(someHost, plan.Learn); persisted {
		t.Fatal("the earlier failure was disproved by a working normal connection and must not count")
	}
}

func TestOppositeEvidenceDoesNotAddUp(t *testing.T) {
	r := newRig(t, Always)
	r.Observe(someHost, NoBypass)
	if persisted, _ := r.Observe(someHost, NeedsBypass); persisted {
		t.Fatal("one NO_BYPASS and one NEEDS_BYPASS observation are not two of the same")
	}
}

func TestNothingIsLearnedAboutASiteThatIsAlreadyDecided(t *testing.T) {
	r := newRig(t, Learn)
	r.set(someHost, NoBypass)
	r.Observe(someHost, NeedsBypass)
	if persisted, _ := r.Observe(someHost, NeedsBypass); persisted {
		t.Fatal("a NO_BYPASS decision must not be overridden by learning")
	}
	if r.Decide(someHost) != NoBypass || strings.Contains(r.file("bypass.txt"), someHost) {
		t.Fatal("learning contradicted an existing decision")
	}
}

// --- no spam: UNKNOWN is never written --------------------------------------------------------

func TestUnknownSitesAreNeverWrittenAnywhere(t *testing.T) {
	for _, m := range []Mode{Always, Learn} {
		r := newRig(t, m)
		skipBefore, bypassBefore := r.file("blacklist.txt"), r.file("bypass.txt")
		for i := 0; i < 200; i++ {
			host := "visited" + string(rune('a'+i%26)) + ".example"
			plan := r.PlanFor(host)
			r.Succeeded(host, plan) // every visit works on the first attempt
		}
		r.Observe("x.example", Unknown) // an explicit UNKNOWN observation is a no-op too
		if r.file("blacklist.txt") != skipBefore || r.file("bypass.txt") != bypassBefore {
			t.Fatalf("%s: visiting sites that work must not write anything to the lists", m)
		}
		if r.Skip.Len() != 0 || r.Bypass.Len() != 0 {
			t.Fatalf("%s: lists must stay empty, got %v %v", m, r.Skip.Items(), r.Bypass.Items())
		}
	}
}

// --- explicit user choices -----------------------------------------------------------------------

func TestSetStateMovesAHostBetweenListsAndNeverDuplicatesIt(t *testing.T) {
	r := newRig(t, Always)
	r.set(someHost, NoBypass)
	r.set(someHost, NeedsBypass)
	if r.Decide(someHost) != NeedsBypass || r.Skip.Match(someHost) || r.Bypass.Len() != 1 {
		t.Fatalf("host must move, not copy: skip=%v bypass=%v", r.Skip.Items(), r.Bypass.Items())
	}
	r.set(someHost, Unknown)
	if r.Decide(someHost) != Unknown || r.Skip.Len() != 0 || r.Bypass.Len() != 0 {
		t.Fatal("setting UNKNOWN must remove the host from both lists")
	}
}

func TestSetStateKeepsOtherEntriesAndComments(t *testing.T) {
	r := newRig(t, Always)
	r.set("keep.example", NoBypass)
	r.set(someHost, NoBypass)
	r.set(someHost, Unknown)
	if r.Decide("keep.example") != NoBypass || !strings.Contains(r.file("blacklist.txt"), "# Sites that never use the DPI bypass") {
		t.Fatalf("other entries or the header were damaged: %q", r.file("blacklist.txt"))
	}
}

func TestEntriesListEachStateSeparately(t *testing.T) {
	r := newRig(t, Learn)
	r.set("n.example", NoBypass)
	r.set("b.example", NeedsBypass)
	if e := r.Entries(NoBypass); len(e) != 1 || e[0].Host != "n.example" {
		t.Errorf("NO_BYPASS entries: %+v", e)
	}
	if e := r.Entries(NeedsBypass); len(e) != 1 || e[0].Host != "b.example" {
		t.Errorf("NEEDS_BYPASS entries: %+v", e)
	}
	if r.Entries(Unknown) != nil {
		t.Error("UNKNOWN has no entries by definition")
	}
}

func TestReloadSeesHandEditsOfEitherList(t *testing.T) {
	r := newRig(t, Learn)
	r.Reload()
	os.WriteFile(filepath.Join(r.dir, "bypass.txt"), []byte("edited.example\n"), 0o644)
	future := time.Now().Add(3 * time.Second)
	os.Chtimes(filepath.Join(r.dir, "bypass.txt"), future, future)
	if !r.Reload() || r.Decide("edited.example") != NeedsBypass {
		t.Fatal("an edit of bypass.txt was not picked up")
	}
	if r.Reload() {
		t.Fatal("nothing changed since")
	}
}

// --- concurrency -----------------------------------------------------------------------------------

// A page opens many connections to one host at once; all of them fail together.
func TestManyConcurrentObservationsPersistTheDecisionExactlyOnce(t *testing.T) {
	r := newRig(t, Learn)
	var wg sync.WaitGroup
	var persisted atomic.Int32
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, err := r.Observe(someHost, NeedsBypass); err != nil {
				t.Error(err)
			} else if ok {
				persisted.Add(1)
			}
		}()
	}
	wg.Wait()
	if persisted.Load() != 1 || strings.Count(r.file("bypass.txt"), someHost) != 1 {
		t.Fatalf("persisted %d times, %d lines", persisted.Load(), strings.Count(r.file("bypass.txt"), someHost))
	}
}

// --- names ----------------------------------------------------------------------------------------------

func TestDecisionAndModeNamesRoundTrip(t *testing.T) {
	for _, d := range []Decision{Unknown, NeedsBypass, NoBypass} {
		if back, ok := ParseDecision(d.String()); !ok || back != d {
			t.Errorf("decision %v does not round-trip through %q", d, d.String())
		}
	}
	if _, ok := ParseDecision("MAYBE"); ok {
		t.Error("unknown decision names must be rejected")
	}
	for _, m := range []Mode{Always, Learn} {
		if back, ok := ParseMode(string(m)); !ok || back != m {
			t.Errorf("mode %v does not round-trip", m)
		}
	}
	if m, ok := ParseMode("sometimes"); ok || m != Learn {
		t.Errorf("an invalid mode must be rejected and fall back to Learn, got %v %v", m, ok)
	}
}

func TestAnInvalidModeSettingFallsBackToLearn(t *testing.T) {
	r := newRig(t, Mode("garbage"))
	if r.Mode() != Learn {
		t.Fatalf("mode %q", r.Mode())
	}
	if got := r.PlanFor(anyUnknown); got != learnUnk {
		t.Fatalf("plan %+v", got)
	}
}

// persistTwice drives the evidence rule until the decision is written.
func persistTwice(t *testing.T, r *rig, host string, d Decision) {
	t.Helper()
	for i := 0; i < r.Threshold; i++ {
		if _, err := r.Observe(host, d); err != nil {
			t.Fatal(err)
		}
	}
	if r.Decide(host) != d {
		t.Fatalf("%s did not become %v after %d observations", host, d, r.Threshold)
	}
}
