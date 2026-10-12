package dashboard

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dpi/internal/app"
	"dpi/internal/policy"
)

type siteRow struct{ Host, Note, Source string }

type sitesReply struct {
	NeedsBypass []siteRow
	NoBypass    []siteRow
}

func sites(t *testing.T, h http.Handler) sitesReply {
	t.Helper()
	var out sitesReply
	if err := json.Unmarshal(get(h, "/api/sites").Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func hosts(rows []siteRow) []string {
	var out []string
	for _, r := range rows {
		out = append(out, r.Host)
	}
	return out
}

func has(rows []siteRow, host string) bool {
	for _, r := range rows {
		if r.Host == host {
			return true
		}
	}
	return false
}

func TestAFreshInstallHasNoSitesAtAll(t *testing.T) {
	h, _ := newHandler(t)
	raw := get(h, "/api/sites").Body.String()
	if !strings.Contains(raw, `"needsBypass":[]`) || !strings.Contains(raw, `"noBypass":[]`) {
		t.Fatalf("both lists must be empty arrays (never null, never pre-filled): %s", raw)
	}
}

func TestAddingAnExplicitDecisionPutsTheSiteInTheRightList(t *testing.T) {
	h, a := newHandler(t)

	rec := post(h, "POST", "/api/sites", `{"host":"  HTTPS://Game.Example.org:443/login?x=1 ","state":"NO_BYPASS"}`)
	if rec.Code != 200 || a.Policy.Decide("game.example.org") != policy.NoBypass {
		t.Fatalf("NO_BYPASS: %d %s", rec.Code, rec.Body)
	}
	rec = post(h, "POST", "/api/sites", `{"host":"blocked.example","state":"NEEDS_BYPASS"}`)
	if rec.Code != 200 || a.Policy.Decide("blocked.example") != policy.NeedsBypass {
		t.Fatalf("NEEDS_BYPASS: %d %s", rec.Code, rec.Body)
	}

	got := sites(t, h)
	if !has(got.NoBypass, "game.example.org") || has(got.NeedsBypass, "game.example.org") {
		t.Fatalf("game.example.org must be only in the never-bypass list: %+v", got)
	}
	if !has(got.NeedsBypass, "blocked.example") || has(got.NoBypass, "blocked.example") {
		t.Fatalf("blocked.example must be only in the bypass list: %+v", got)
	}
	for _, r := range append(got.NoBypass, got.NeedsBypass...) {
		if r.Source != "you" || r.Note != "added in dashboard" {
			t.Errorf("an explicit user choice must be recorded as such: %+v", r)
		}
	}
	if !strings.Contains(read(t, a.Dir, "blacklist.txt"), "game.example.org # added in dashboard") || !strings.Contains(read(t, a.Dir, "bypass.txt"), "blocked.example # added in dashboard") {
		t.Fatal("decisions must be written to the shared files")
	}
}

func read(t *testing.T, dir, name string) string {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(dir, name))
	return string(b)
}

func TestChoosingTheOtherStateMovesTheSiteInsteadOfDuplicatingIt(t *testing.T) {
	h, _ := newHandler(t)
	post(h, "POST", "/api/sites", `{"host":"x.example","state":"NO_BYPASS"}`)
	post(h, "POST", "/api/sites", `{"host":"x.example","state":"NEEDS_BYPASS"}`)
	got := sites(t, h)
	if has(got.NoBypass, "x.example") || !has(got.NeedsBypass, "x.example") {
		t.Fatalf("must move, not copy: %+v", got)
	}
	post(h, "POST", "/api/sites", `{"host":"x.example","state":"UNKNOWN"}`)
	got = sites(t, h)
	if has(got.NoBypass, "x.example") || has(got.NeedsBypass, "x.example") {
		t.Fatalf("UNKNOWN means not stored anywhere: %+v", got)
	}
}

// NO_BYPASS always wins, so a NEEDS_BYPASS entry under a NO_BYPASS parent would be silently useless.
func TestANeedsBypassEntryUnderANoBypassParentIsRefusedWithAReason(t *testing.T) {
	h, a := newHandler(t)
	post(h, "POST", "/api/sites", `{"host":"example.com","state":"NO_BYPASS"}`)
	rec := post(h, "POST", "/api/sites", `{"host":"api.example.com","state":"NEEDS_BYPASS"}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "example.com") {
		t.Fatalf("want 409 naming the covering entry, got %d %s", rec.Code, rec.Body)
	}
	if a.Policy.Bypass.Len() != 0 {
		t.Fatal("the refused entry must not be written")
	}
	// the other direction is fine: the user explicitly says this host must never be bypassed
	post(h, "POST", "/api/sites", `{"host":"b.example","state":"NEEDS_BYPASS"}`)
	if rec := post(h, "POST", "/api/sites", `{"host":"b.example","state":"NO_BYPASS"}`); rec.Code != 200 {
		t.Fatalf("NO_BYPASS for a bypass site got %d", rec.Code)
	}
}

func TestRemovingASiteOnlyTouchesWhatWasAsked(t *testing.T) {
	h, _ := newHandler(t)
	post(h, "POST", "/api/sites", `{"host":"keep.example","state":"NO_BYPASS"}`)
	post(h, "POST", "/api/sites", `{"host":"gone.example","state":"NO_BYPASS"}`)
	post(h, "POST", "/api/sites", `{"host":"needs.example","state":"NEEDS_BYPASS"}`)

	if rec := post(h, "DELETE", "/api/sites?host=gone.example&state=NO_BYPASS", ""); rec.Code != 200 {
		t.Fatalf("remove: %d %s", rec.Code, rec.Body)
	}
	got := sites(t, h)
	if has(got.NoBypass, "gone.example") || !has(got.NoBypass, "keep.example") || !has(got.NeedsBypass, "needs.example") {
		t.Fatalf("removal damaged other entries: %+v", got)
	}
	// without a state it is forgotten from both lists
	post(h, "DELETE", "/api/sites?host=needs.example", "")
	if got := sites(t, h); has(got.NeedsBypass, "needs.example") {
		t.Fatalf("not forgotten: %+v", got)
	}
}

func TestBadSiteRequestsAreRefused(t *testing.T) {
	h, a := newHandler(t)
	for name, body := range map[string]string{
		"unknown state":  `{"host":"a.example","state":"MAYBE"}`,
		"no state":       `{"host":"a.example"}`,
		"empty host":     `{"host":"   ","state":"NO_BYPASS"}`,
		"unusable host":  `{"host":"two words.example","state":"NO_BYPASS"}`,
		"not even json":  `not json`,
		"lowercase name": `{"host":"a.example","state":"no_bypass"}`,
	} {
		if rec := post(h, "POST", "/api/sites", body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d, want 400", name, rec.Code)
		}
	}
	if rec := post(h, "DELETE", "/api/sites?host=a.example&state=BOGUS", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("bogus state on delete got %d", rec.Code)
	}
	if rec := post(h, "DELETE", "/api/sites", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("missing host on delete got %d", rec.Code)
	}
	if a.Policy.Skip.Len() != 0 || a.Policy.Bypass.Len() != 0 {
		t.Fatal("refused requests must not write anything")
	}
}

// Entries are labelled only by what the file records: nothing is claimed about unknown origins.
func TestEntrySourcesAreTakenFromTheirNotesAndNothingIsClaimedWithoutOne(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "blacklist.txt"), []byte("plain.example\nlearned.example # auto-skip 2026-10-01\nmine.example # added in dashboard\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "bypass.txt"), []byte("found.example # learned 2026-10-02\n"), 0o644)
	h := New(app.New(dir, "127.0.0.1:18080", "test"))

	src := map[string]string{}
	got := sites(t, h)
	for _, r := range append(got.NoBypass, got.NeedsBypass...) {
		src[r.Host] = r.Source
	}
	want := map[string]string{"plain.example": "existing", "learned.example": "learned", "mine.example": "you", "found.example": "learned"}
	for host, s := range want {
		if src[host] != s {
			t.Errorf("%s: source %q, want %q (all: %v)", host, src[host], s, src)
		}
	}
}

// ---- mode ----

type stateReply struct {
	Mode      string
	SiteCount int
}

func state(t *testing.T, h http.Handler) stateReply {
	t.Helper()
	var s stateReply
	if err := json.Unmarshal(get(h, "/api/state").Body.Bytes(), &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestTheModeIsShownAndDefaultsToAlways(t *testing.T) {
	h, _ := newHandler(t)
	if s := state(t, h); s.Mode != "always" {
		t.Fatalf("default mode %q", s.Mode)
	}
}

func TestSwitchingModeChangesNothingButTheModeAndSurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	a := app.New(dir, "127.0.0.1:18080", "test")
	h := New(a)
	post(h, "POST", "/api/sites", `{"host":"never.example","state":"NO_BYPASS"}`)
	post(h, "POST", "/api/sites", `{"host":"needs.example","state":"NEEDS_BYPASS"}`)
	before := sites(t, h)

	for _, m := range []string{"learn", "always", "learn"} {
		rec := post(h, "POST", "/api/mode", `{"mode":"`+m+`"}`)
		if rec.Code != 200 || state(t, h).Mode != m {
			t.Fatalf("switch to %s: %d %s", m, rec.Code, rec.Body)
		}
		after := sites(t, h)
		if strings.Join(hosts(after.NoBypass), ",") != strings.Join(hosts(before.NoBypass), ",") ||
			strings.Join(hosts(after.NeedsBypass), ",") != strings.Join(hosts(before.NeedsBypass), ",") {
			t.Fatalf("switching to %s changed the learned data: %+v -> %+v", m, before, after)
		}
		if state(t, h).SiteCount != 2 {
			t.Fatalf("site count after switching to %s: %d", m, state(t, h).SiteCount)
		}
	}

	// restart: the mode and both lists are still there
	again := New(app.New(dir, "127.0.0.1:18080", "test"))
	if s := state(t, again); s.Mode != "learn" || s.SiteCount != 2 {
		t.Fatalf("after a restart: %+v", s)
	}
	if got := sites(t, again); !has(got.NoBypass, "never.example") || !has(got.NeedsBypass, "needs.example") {
		t.Fatalf("lists lost on restart: %+v", got)
	}
}

func TestAnInvalidModeIsRefusedAndTheOldOneKept(t *testing.T) {
	h, a := newHandler(t)
	for _, body := range []string{`{"mode":"sometimes"}`, `{"mode":""}`, `{"mode":"LEARN"}`, `not json`} {
		if rec := post(h, "POST", "/api/mode", body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s got %d, want 400", body, rec.Code)
		}
	}
	if a.Mode() != policy.Always {
		t.Fatalf("mode changed to %v by refused requests", a.Mode())
	}
}
