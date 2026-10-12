package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dpi/internal/policy"
)

// A skip list as earlier versions wrote it: the four pre-filled entries without a
// note, an entry learned by the app, one added in the dashboard, a hand edit and
// two lines that are not usable domains.
const legacySkipList = `# Domains that skip the DPI bypass (connect directly). One per line.
# A domain also covers its subdomains. Changes are picked up automatically.
riotgames.com
riotcdn.net
learned.example # auto-skip 2026-10-01
mine.example # added in dashboard
handmade.example # because I said so

two words.example
not/a/domain
`

func write(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestMigrationKeepsEveryExistingSkipEntryAsNoBypassAndRewritesNothing(t *testing.T) {
	dir := t.TempDir()
	list := write(t, dir, "blacklist.txt", legacySkipList)

	a := New(dir, "127.0.0.1:0", "test")
	m := a.Migration

	if !m.Ran || m.Err != nil || !m.BackedUp {
		t.Fatalf("migration: %+v", m)
	}
	if got := read(t, list); got != legacySkipList {
		t.Fatalf("blacklist.txt must not be rewritten by the migration:\n%q", got)
	}
	if got := read(t, list+preModesBackup); got != legacySkipList {
		t.Fatalf("backup differs from the original:\n%q", got)
	}
	// counts: 7 entries; 2 of unknown origin without a note + 1 hand edit with a note = 3 unknown
	if m.Entries != 7 || m.Learned != 1 || m.ByUser != 1 || m.Unknown != 3 || m.Invalid != 2 {
		t.Fatalf("classification: %+v", m)
	}
	// every usable entry still means "connect directly"; the new states are explicit
	for _, h := range []string{"riotgames.com", "api.riotgames.com", "riotcdn.net", "learned.example", "mine.example", "handmade.example"} {
		if got := a.Policy.Decide(h); got != policy.NoBypass {
			t.Errorf("%s: %v, want NO_BYPASS", h, got)
		}
	}
	// nothing is invented: there was no 'needs bypass' data before
	if a.Policy.Bypass.Len() != 0 || a.Policy.Decide("anything.example") != policy.Unknown {
		t.Fatalf("the migration must not create NEEDS_BYPASS records: %v", a.Policy.Bypass.Items())
	}
}

// Entries that are not a usable domain are kept in the file and reported, never deleted or converted.
func TestMigrationLeavesInvalidAndAmbiguousRecordsAloneAndSaysSo(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "blacklist.txt", "two words.example\nnot/a/domain\nok.example\n")
	a := New(dir, "127.0.0.1:0", "test")
	if a.Migration.Invalid != 2 || a.Migration.Unknown != 1 {
		t.Fatalf("report: %+v", a.Migration)
	}
	got := read(t, filepath.Join(dir, "blacklist.txt"))
	if !strings.Contains(got, "two words.example") || !strings.Contains(got, "not/a/domain") {
		t.Fatalf("records that could not be mapped were deleted:\n%s", got)
	}
	if a.Policy.Decide("two words.example") != policy.Unknown {
		t.Fatal("an unusable record must not become a confident decision")
	}
}

func TestMigrationRecordsTheSchemaAndDefaultsToLearnOnlyWhenNoModeIsSet(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "blacklist.txt", "a.example\n")
	a := New(dir, "127.0.0.1:0", "test")
	if s := a.Settings.Get(); s.Schema != schemaVersion || s.DPIMode != "learn" || a.Mode() != policy.Learn {
		t.Fatalf("settings after migration: %+v", s)
	}

	// a mode that was already chosen is never overwritten
	dir = t.TempDir()
	write(t, dir, "settings.json", `{"lanSharing": true, "dpiMode": "always"}`)
	write(t, dir, "blacklist.txt", "a.example\n")
	b := New(dir, "127.0.0.1:0", "test")
	if s := b.Settings.Get(); s.DPIMode != "always" || !s.LANSharing || s.Schema != schemaVersion {
		t.Fatalf("existing settings were changed: %+v", s)
	}
}

// Running it again changes nothing, not even if the file changed in between.
func TestMigrationIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	list := write(t, dir, "blacklist.txt", legacySkipList)
	New(dir, "127.0.0.1:0", "test")
	firstBackup := read(t, list+preModesBackup)
	settingsAfterFirst := read(t, filepath.Join(dir, "settings.json"))

	write(t, dir, "blacklist.txt", legacySkipList+"added-later.example # added in dashboard\n") // normal use
	b := New(dir, "127.0.0.1:0", "test")                                                        // restart

	if b.Migration.Ran {
		t.Fatalf("a folder at the current version must not be migrated again: %+v", b.Migration)
	}
	if got := read(t, list+preModesBackup); got != firstBackup {
		t.Fatal("the backup was overwritten by a later run")
	}
	if got := read(t, filepath.Join(dir, "settings.json")); got != settingsAfterFirst {
		t.Fatalf("settings changed on the second run:\n%s\n%s", settingsAfterFirst, got)
	}
	if !strings.Contains(read(t, list), "added-later.example") {
		t.Fatal("later edits must survive")
	}
	if b.Policy.Decide("added-later.example") != policy.NoBypass {
		t.Fatal("later entries must still work")
	}
}

// A fresh install must not be pre-filled with any site.
func TestAFreshFolderStartsEmptyWithoutAnyPrefilledSiteOrBackup(t *testing.T) {
	dir := t.TempDir()
	a := New(dir, "127.0.0.1:0", "test")
	if a.Policy.Skip.Len() != 0 || a.Policy.Bypass.Len() != 0 {
		t.Fatalf("a new install must start with empty lists, got never-bypass %v, needs-bypass %v", a.Policy.Skip.Items(), a.Policy.Bypass.Items())
	}
	for _, name := range []string{"blacklist.txt", "bypass.txt"} {
		for _, line := range strings.Split(read(t, filepath.Join(dir, name)), "\n") {
			if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
				t.Fatalf("%s contains an entry on a fresh install: %q", name, line)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "blacklist.txt"+preModesBackup)); err == nil {
		t.Fatal("there was nothing to back up")
	}
	if !a.Migration.Ran || a.Migration.Entries != 0 || a.Migration.Err != nil {
		t.Fatalf("report: %+v", a.Migration)
	}
	if a.Mode() != policy.Learn {
		t.Fatalf("the default mode is Learn, got %v", a.Mode())
	}
}

// If the backup can not be made the migration stops before changing anything and retries at the next start.
func TestAFailedMigrationChangesNothingAndRetriesNextStart(t *testing.T) {
	dir := t.TempDir()
	list := write(t, dir, "blacklist.txt", legacySkipList)
	if err := os.Mkdir(list+preModesBackup, 0o755); err != nil { // the backup path is taken by a folder: writing it fails
		t.Fatal(err)
	}
	a := New(dir, "127.0.0.1:0", "test")
	if a.Migration.Err == nil {
		t.Fatal("the failure must be reported")
	}
	if a.Settings.Get().Schema != 0 {
		t.Fatal("the schema must not be recorded after a failed migration")
	}
	if read(t, list) != legacySkipList {
		t.Fatal("a failed migration must not touch the skip list")
	}
	if a.Policy.Decide("riotgames.com") != policy.NoBypass {
		t.Fatal("existing decisions must keep working even if the migration failed")
	}

	os.Remove(list + preModesBackup) // the problem is fixed
	b := New(dir, "127.0.0.1:0", "test")
	if !b.Migration.Ran || b.Migration.Err != nil || b.Settings.Get().Schema != schemaVersion {
		t.Fatalf("it must run again and succeed: %+v", b.Migration)
	}
}

// Downgrade safety: the old file is what an older version reads; the new files are extras it ignores.
func TestTheOldSkipListFileKeepsItsNameFormatAndMeaning(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "blacklist.txt", "keep.example # added in dashboard\n")
	a := New(dir, "127.0.0.1:0", "test")
	if err := a.Policy.SetState("new.example", policy.NoBypass, "added in dashboard"); err != nil {
		t.Fatal(err)
	}
	got := read(t, filepath.Join(dir, "blacklist.txt"))
	if !strings.Contains(got, "keep.example # added in dashboard") || !strings.Contains(got, "new.example # added in dashboard") {
		t.Fatalf("blacklist.txt must stay a plain 'domain # note' list:\n%s", got)
	}
}
