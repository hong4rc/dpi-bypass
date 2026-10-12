package dashboard

import (
	"fmt"
	"net/http"
	"strings"

	"dpi/internal/blacklist"
	"dpi/internal/logx"
	"dpi/internal/policy"
)

// siteEntry is one persisted decision. Source says where it came from, using
// only what the file records: "learned" (the app, from connection evidence),
// "you" (added in the dashboard) or "existing" (no note: a default entry of an
// earlier version or a hand edit; the origin is not known).
type siteEntry struct {
	Host   string `json:"host"`
	Note   string `json:"note"`
	Source string `json:"source"`
}

type sitesJSON struct {
	NeedsBypass []siteEntry `json:"needsBypass"` // NEEDS_BYPASS
	NoBypass    []siteEntry `json:"noBypass"`    // NO_BYPASS
}

func sourceOf(note string) string {
	switch {
	case strings.HasPrefix(note, "learned"), strings.HasPrefix(note, "auto-skip"):
		return "learned"
	case note == "added in dashboard":
		return "you"
	}
	return "existing"
}

func entries(list []blacklist.Entry) []siteEntry {
	out := make([]siteEntry, 0, len(list))
	for _, e := range list {
		out = append(out, siteEntry{Host: e.Host, Note: e.Note, Source: sourceOf(e.Note)})
	}
	return out
}

func (h *handler) sites() sitesJSON {
	return sitesJSON{
		NeedsBypass: entries(h.a.Policy.Entries(policy.NeedsBypass)),
		NoBypass:    entries(h.a.Policy.Entries(policy.NoBypass)),
	}
}

func (h *handler) sitesList(w http.ResponseWriter, r *http.Request) { writeJSON(w, h.sites()) }

// coveringNoBypass returns the NO_BYPASS entry (other than host itself) that
// covers host, since NO_BYPASS always wins and would silently override a new
// NEEDS_BYPASS entry for a subdomain of it.
func (h *handler) coveringNoBypass(host string) string {
	for _, e := range h.a.Policy.Entries(policy.NoBypass) {
		if e.Host != host && blacklist.Usable(e.Host) && strings.HasSuffix(host, "."+e.Host) {
			return e.Host
		}
	}
	return ""
}

// sitesSet is an explicit choice by the user: it moves exactly this host to the
// given list, so the two lists never contradict each other.
func (h *handler) sitesSet(w http.ResponseWriter, r *http.Request) {
	var b struct{ Host, State string }
	if !readJSON(w, r, &b) {
		return
	}
	d, ok := policy.ParseDecision(b.State)
	if !ok {
		fail(w, http.StatusBadRequest, `state must be "NEEDS_BYPASS", "NO_BYPASS" or "UNKNOWN"`)
		return
	}
	host := CleanHost(b.Host)
	if !blacklist.Usable(host) {
		fail(w, http.StatusBadRequest, "enter a domain such as example.com")
		return
	}
	if d == policy.NeedsBypass {
		if parent := h.coveringNoBypass(host); parent != "" {
			fail(w, http.StatusConflict, fmt.Sprintf("%s is covered by the never-bypass entry %s, which always wins. Remove that entry first.", host, parent))
			return
		}
	}
	if err := h.a.Policy.SetState(host, d, "added in dashboard"); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	logx.Info.Printf("sites: %s set to %s from the dashboard", host, d)
	writeJSON(w, h.sites())
}

// sitesRemove forgets a site: from one list when state is given, from both otherwise.
func (h *handler) sitesRemove(w http.ResponseWriter, r *http.Request) {
	host := CleanHost(r.URL.Query().Get("host"))
	if host == "" {
		fail(w, http.StatusBadRequest, "missing host")
		return
	}
	var err error
	switch state := r.URL.Query().Get("state"); state {
	case "":
		err = h.a.Policy.SetState(host, policy.Unknown, "")
	case "NO_BYPASS":
		err = h.a.Policy.Skip.Remove(host)
	case "NEEDS_BYPASS":
		err = h.a.Policy.Bypass.Remove(host)
	default:
		fail(w, http.StatusBadRequest, `state must be "NEEDS_BYPASS" or "NO_BYPASS"`)
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	logx.Info.Printf("sites: %s removed from the dashboard", host)
	writeJSON(w, h.sites())
}

// setMode switches how unknown sites are handled; learned decisions are not touched.
func (h *handler) setMode(w http.ResponseWriter, r *http.Request) {
	var b struct{ Mode string }
	if !readJSON(w, r, &b) {
		return
	}
	if err := h.a.SetMode(policy.Mode(b.Mode)); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, h.snapshot())
}
