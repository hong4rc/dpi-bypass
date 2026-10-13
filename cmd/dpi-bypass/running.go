package main

import (
	"encoding/json"
	"net/http"
	"time"
)

// isRunning reports whether another dpi-bypass owns addr: its dashboard answers
// GET /api/state (loopback GET needs no special header) with JSON that has a version.
func isRunning(addr string) bool {
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Get("http://" + addr + "/api/state")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	var st struct {
		Version *string `json:"version"`
	}
	return json.NewDecoder(resp.Body).Decode(&st) == nil && st.Version != nil
}
