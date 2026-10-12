package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"dpi/internal/blacklist"
	"dpi/internal/logx"
	"dpi/internal/policy"
	"dpi/internal/settings"
)

// schemaVersion is the data layout this build works with. Folders at an older
// version are migrated once; the version is recorded in settings.json only after
// everything succeeded, so a failed migration simply runs again next start.
//
//	1  (before DPI modes)  blacklist.txt = "skip list": hosts that connect directly
//	2  (DPI modes)         blacklist.txt = NO_BYPASS (unchanged file, same format and meaning)
//	                       bypass.txt    = NEEDS_BYPASS (new, same format; UNKNOWN is never stored)
//	                       settings.json = dpiMode ("always" or "learn") and schema
const schemaVersion = 2

// preModesBackup is added to blacklist.txt to name its pre-migration copy.
const preModesBackup = ".pre-modes.bak"

// Migration reports what migrate found and did.
type Migration struct {
	Ran      bool  // false: this folder was already at the current version
	BackedUp bool  // a copy of blacklist.txt was saved
	Entries  int   // existing skip-list entries: all of them are kept as NO_BYPASS
	Learned  int   // ... of which the app added itself ("auto-skip" note: it saw the bypass break the site)
	ByUser   int   // ... added in the dashboard
	Unknown  int   // ... with no note or another one: origin unknown (a default entry of an earlier version, or a hand edit)
	Invalid  int   // ... that are not a usable domain: left untouched in the file, they never match
	Err      error // the migration did not finish; it will be retried at the next start
}

// migrate brings dir up to schemaVersion. What it does, and why that is all:
//
//   - Every existing skip-list entry already means "connect directly", which is
//     exactly NO_BYPASS, so nothing is converted, rewritten or deleted. Entries whose
//     origin is unclear (no note) are kept as NO_BYPASS too: that preserves what the
//     user has been getting, and dropping them could silently break a site again.
//   - There was no "needs bypass" data before, so none is invented: bypass.txt starts
//     empty and only fills from real connection evidence or from the user.
//   - blacklist.txt is copied once to blacklist.txt.pre-modes.bak (an existing backup
//     is never overwritten). Restoring that file and deleting "schema" from
//     settings.json undoes the migration; an older version of the app reads the
//     unchanged blacklist.txt and ignores the new files.
//   - dpiMode is set to "learn" only when it is not set (Learn is the default and does
//     not touch sites that already work); a mode the user already chose is never changed.
func migrate(dir string, st *settings.Store) Migration {
	var m Migration
	if st.Get().Schema >= schemaVersion {
		return m
	}
	m.Ran = true

	path := filepath.Join(dir, "blacklist.txt")
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		bak := path + preModesBackup
		switch info, statErr := os.Stat(bak); {
		case statErr == nil && !info.Mode().IsRegular():
			m.Err = fmt.Errorf("cannot back up %s: %s is not a file", filepath.Base(path), filepath.Base(bak))
			return m
		case statErr != nil: // no backup yet (an existing one is never overwritten)
			if err := os.WriteFile(bak, data, 0o644); err != nil {
				m.Err = fmt.Errorf("cannot back up %s: %w", filepath.Base(path), err)
				return m
			}
			m.BackedUp = true
		}
		// the file exists, so this only reads it (the header argument is never used)
		for _, e := range blacklist.New(path, "").Entries() {
			m.Entries++
			switch {
			case !blacklist.Usable(e.Host):
				m.Invalid++
			case strings.HasPrefix(e.Note, "auto-skip"):
				m.Learned++
			case e.Note == "added in dashboard":
				m.ByUser++
			default:
				m.Unknown++
			}
		}
	case !os.IsNotExist(err):
		m.Err = fmt.Errorf("cannot read %s: %w", filepath.Base(path), err)
		return m
	}

	if err := st.Update(func(s *settings.Settings) {
		if s.DPIMode == "" {
			s.DPIMode = string(policy.Learn)
		}
		s.Schema = schemaVersion
	}); err != nil {
		m.Err = fmt.Errorf("cannot save settings: %w", err)
	}
	return m
}

func (m Migration) log() {
	switch {
	case m.Err != nil:
		logx.Info.Printf("data migration did not finish (will retry at the next start): %v", m.Err)
	case m.Ran && m.Entries > 0:
		logx.Info.Printf("data migrated to DPI modes: %d existing skip-list entries kept as NO_BYPASS (%d learned by the app, %d added by you, %d of unknown origin, %d unusable and left untouched); backup: blacklist.txt%s",
			m.Entries, m.Learned, m.ByUser, m.Unknown, m.Invalid, preModesBackup)
	case m.Ran:
		logx.Info.Println("new data folder prepared for DPI modes: nothing to migrate")
	}
}
