// Package dashboard is the web UI: a JSON API plus one embedded HTML page. It is
// reachable only from this PC (loopback client, loopback Host header) and
// state-changing requests need a custom header, so neither other devices on the
// LAN nor other websites can drive it.
package dashboard

import (
	_ "embed"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"dpi/internal/app"
	"dpi/internal/logx"
)

//go:embed web/index.html
var indexHTML []byte

type handler struct{ a *app.App }

// New returns the dashboard handler for a.
func New(a *app.App) http.Handler {
	h := &handler{a}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", h.index)
	mux.HandleFunc("GET /api/state", h.state)
	mux.HandleFunc("POST /api/bypass", h.setBypass)
	mux.HandleFunc("POST /api/lan", h.setLAN)
	mux.HandleFunc("POST /api/test", h.test)
	mux.HandleFunc("GET /api/devices", h.devices)
	mux.HandleFunc("POST /api/devices/name", h.nameDevice)
	mux.HandleFunc("GET /api/connections", h.connections)
	mux.HandleFunc("GET /api/logs", h.logs)
	mux.HandleFunc("GET /api/logs/stream", h.logStream)
	mux.HandleFunc("POST /api/logs/clear", h.clearLogs)
	mux.HandleFunc("GET /api/skip", h.skipList)
	mux.HandleFunc("POST /api/skip", h.skipAdd)
	mux.HandleFunc("DELETE /api/skip", h.skipRemove)
	return guard(mux)
}

// guard lets only this PC through, for its own address names, and requires the
// custom header on anything that changes state.
func guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLoopback(r.RemoteAddr) {
			http.Error(w, "The dashboard only opens on the PC running dpi-bypass: use http://127.0.0.1:18080/ there.", http.StatusForbidden)
			return
		}
		if !loopbackHost(r.Host) {
			http.Error(w, "Open the dashboard as http://127.0.0.1:18080/", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if r.Header.Get("X-Requested-With") != "dpi" {
				http.Error(w, "missing X-Requested-With header", http.StatusForbidden)
				return
			}
			if o := r.Header.Get("Origin"); o != "" {
				if u, err := url.Parse(o); err != nil || u.Host != r.Host {
					http.Error(w, "cross-site request blocked", http.StatusForbidden)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func isLoopback(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func loopbackHost(hostHeader string) bool {
	host, _, err := net.SplitHostPort(hostHeader)
	if err != nil {
		host = hostHeader
	}
	host = strings.Trim(host, "[]")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		fail(w, http.StatusBadRequest, "bad request body")
		return false
	}
	return true
}

func (h *handler) index(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(indexHTML)
}

type stateJSON struct {
	Version     string  `json:"version"`
	On          bool    `json:"on"`
	LAN         lanJSON `json:"lan"`
	UptimeSec   int64   `json:"uptimeSec"`
	Addr        string  `json:"addr"`
	Totals      any     `json:"totals"`
	SkipCount   int     `json:"skipCount"`
	DeviceCount int     `json:"deviceCount"`
}

type lanJSON struct {
	Enabled bool     `json:"enabled"`
	URLs    []string `json:"urls"`
}

func (h *handler) snapshot() stateJSON {
	urls := h.a.LANURLs()
	if urls == nil {
		urls = []string{}
	}
	return stateJSON{
		Version:     h.a.Version,
		On:          h.a.On(),
		LAN:         lanJSON{Enabled: len(urls) > 0, URLs: urls},
		UptimeSec:   int64(time.Since(h.a.Started).Seconds()),
		Addr:        h.a.Addr,
		Totals:      h.a.Stats.Totals(),
		SkipCount:   len(h.a.BL.Entries()),
		DeviceCount: len(h.a.Stats.Devices()),
	}
}

func (h *handler) state(w http.ResponseWriter, r *http.Request) { writeJSON(w, h.snapshot()) }

type onBody struct {
	On bool `json:"on"`
}

func (h *handler) setBypass(w http.ResponseWriter, r *http.Request) {
	var b onBody
	if !readJSON(w, r, &b) {
		return
	}
	if b.On {
		h.a.Enable()
	} else {
		h.a.Disable()
	}
	writeJSON(w, h.snapshot())
}

func (h *handler) setLAN(w http.ResponseWriter, r *http.Request) {
	var b onBody
	if !readJSON(w, r, &b) {
		return
	}
	if b.On {
		if err := h.a.EnableLAN(); err != nil {
			fail(w, http.StatusConflict, err.Error())
			return
		}
	} else {
		h.a.DisableLAN()
	}
	writeJSON(w, h.snapshot())
}

func (h *handler) test(w http.ResponseWriter, r *http.Request) {
	ok, msg := h.a.Check()
	writeJSON(w, map[string]any{"ok": ok, "message": msg})
}

func (h *handler) devices(w http.ResponseWriter, r *http.Request) { writeJSON(w, h.a.Stats.Devices()) }

func (h *handler) nameDevice(w http.ResponseWriter, r *http.Request) {
	var b struct{ IP, Name string }
	if !readJSON(w, r, &b) {
		return
	}
	if net.ParseIP(b.IP) == nil || len(b.Name) > 60 {
		fail(w, http.StatusBadRequest, "need a device IP and a name up to 60 characters")
		return
	}
	if err := h.a.Stats.SetName(b.IP, b.Name); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, h.a.Stats.Devices())
}

func (h *handler) connections(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, h.a.Stats.Recent(limit(r, 200, 500)))
}

func (h *handler) logs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, logx.Recent(limit(r, 800, 3000)))
}

// clearLogs empties the log files and the in-memory log.
func (h *handler) clearLogs(w http.ResponseWriter, r *http.Request) {
	logx.Clear()
	logx.Info.Println("log cleared from the dashboard")
	writeJSON(w, map[string]bool{"ok": true})
}

// logStream sends new log lines as server-sent events until the page goes away.
func (h *handler) logStream(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		fail(w, http.StatusInternalServerError, "streaming not supported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	fl.Flush()

	ch, cancel := logx.Subscribe()
	defer cancel()
	beat := time.NewTicker(15 * time.Second)
	defer beat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-h.a.Done():
			return
		case l := <-ch:
			b, _ := json.Marshal(l)
			w.Write([]byte("data: "))
			w.Write(b)
			w.Write([]byte("\n\n"))
			fl.Flush()
		case <-beat.C:
			w.Write([]byte(": ping\n\n"))
			fl.Flush()
		}
	}
}

func (h *handler) skipList(w http.ResponseWriter, r *http.Request) { writeJSON(w, h.a.BL.Entries()) }

func (h *handler) skipAdd(w http.ResponseWriter, r *http.Request) {
	var b struct{ Host string }
	if !readJSON(w, r, &b) {
		return
	}
	host := CleanHost(b.Host)
	if host == "" {
		fail(w, http.StatusBadRequest, "enter a domain such as example.com")
		return
	}
	if _, err := h.a.BL.Add(host, "added in dashboard"); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	logx.Info.Printf("skip list: added %s from the dashboard", host)
	writeJSON(w, h.a.BL.Entries())
}

func (h *handler) skipRemove(w http.ResponseWriter, r *http.Request) {
	host := CleanHost(r.URL.Query().Get("host"))
	if host == "" {
		fail(w, http.StatusBadRequest, "missing host")
		return
	}
	if err := h.a.BL.Remove(host); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	logx.Info.Printf("skip list: removed %s from the dashboard", host)
	writeJSON(w, h.a.BL.Entries())
}

func limit(r *http.Request, def, max int) int {
	n := def
	if x, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil {
		n = x
	}
	if n < 1 {
		n = def
	}
	if n > max {
		n = max
	}
	return n
}

// CleanHost reduces what a user types or pastes (URL, host:port, mixed case) to a bare domain or IP.
func CleanHost(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if h, _, err := net.SplitHostPort(s); err == nil {
		s = h
	}
	return strings.Trim(s, ".[] ")
}
