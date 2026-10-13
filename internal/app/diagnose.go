package app

import (
	"context"

	"dpi/internal/policy"
	"dpi/internal/proxy"
)

// DiagnoseResult is the network report for one site plus what the app itself
// knows and would do for it.
type DiagnoseResult struct {
	proxy.Report
	Listed   string `json:"listed"`   // NEEDS_BYPASS, NO_BYPASS or UNKNOWN
	Mode     string `json:"mode"`     // always or learn
	Plan     string `json:"plan"`     // what the proxy does for this site
	BypassOn bool   `json:"bypassOn"` // whether this PC is using the proxy at all
}

// Diagnose runs the connection diagnostic for host. It changes no list and learns nothing.
func (a *App) Diagnose(ctx context.Context, host string) DiagnoseResult {
	return DiagnoseResult{
		Report:   proxy.Diagnose(ctx, host, a.DiagEnv),
		Listed:   a.Policy.Decide(host).String(),
		Mode:     string(a.Mode()),
		Plan:     describePlan(a.Policy.PlanFor(host)),
		BypassOn: a.On(),
	}
}

func describePlan(p policy.Plan) string {
	switch {
	case p.First == policy.Direct && p.Fallback == policy.None:
		return "connects directly, never bypassed"
	case p.First == policy.Split:
		return "uses the bypass first, connects directly if that fails"
	}
	return "connects normally first, uses the bypass if that fails"
}
