package app

import (
	"context"
	"fmt"
	"strings"

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
	Text     string `json:"text"`     // the whole report as plain text, for the page and for copying
}

// Diagnose runs the connection diagnostic for host. It changes no list and learns nothing.
func (a *App) Diagnose(ctx context.Context, host string) DiagnoseResult {
	r := DiagnoseResult{
		Report:   proxy.Diagnose(ctx, host, a.DiagEnv),
		Listed:   a.Policy.Decide(host).String(),
		Mode:     string(a.Mode()),
		Plan:     describePlan(a.Policy.PlanFor(host)),
		BypassOn: a.On(),
	}
	r.Text = r.text()
	return r
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

// plainResult says what an attempt's result means in everyday words.
func plainResult(result string) string {
	switch {
	case result == "ok":
		return "worked"
	case result == "reset":
		return "the connection was cut"
	case result == "timeout":
		return "no reply in time"
	case result == "certificate error":
		return "the certificate was not accepted"
	case strings.HasPrefix(result, "tls alert"):
		return "the site refused (" + result + ")"
	}
	return "failed"
}

func listName(decision string) string {
	switch decision {
	case "NEEDS_BYPASS":
		return "Needs bypass"
	case "NO_BYPASS":
		return "Never bypass"
	}
	return "not listed"
}

// text is the whole report in plain words, the same for "Test a site" and for
// the details of the Steam test. Raw error text goes last, for bug reports.
func (r DiagnoseResult) text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Site: %s\n", r.Host)
	if r.ResolveError != "" {
		fmt.Fprintf(&b, "Name lookup failed: %s\n", r.ResolveError)
	} else if len(r.IPs) > 0 {
		fmt.Fprintf(&b, "Addresses: %s (looked up in %d ms)\n", strings.Join(r.IPs, ", "), r.ResolveMS)
	}
	var cert *proxy.Attempt
	for i := range r.Attempts {
		at := &r.Attempts[i]
		name := map[string]string{"direct": "Normal connection", "split": "With the bypass"}[at.Name]
		if i >= 2 {
			name += " (repeated to confirm)"
		}
		fmt.Fprintf(&b, "%s: %s (connect %d ms, handshake %d ms)\n", name, plainResult(at.Result), at.DialMS, at.HandshakeMS)
		if cert == nil && at.Result == "ok" {
			cert = at
		}
	}
	if cert != nil {
		fmt.Fprintf(&b, "Secure connection: %s, certificate for %s, issued by %s\n", cert.TLSVersion, cert.Subject, cert.Issuer)
	}

	fmt.Fprintf(&b, "\nResult: %s\n%s\n", r.Verdict, r.nextStep())
	fmt.Fprintf(&b, "\nOn the lists: %s\nDPI mode: %s\nWhat the app does for this site: %s\nBypass on this PC: %s\n",
		listName(r.Listed), r.Mode, r.Plan, map[bool]string{true: "on", false: "off"}[r.BypassOn])

	var raw []string
	for _, at := range r.Attempts {
		if at.Error != "" {
			raw = append(raw, fmt.Sprintf("- %s: %s", map[string]string{"direct": "Normal connection", "split": "With the bypass"}[at.Name], at.Error))
		}
	}
	if len(raw) > 0 {
		fmt.Fprintf(&b, "\nTechnical errors (for bug reports)\n%s\n", strings.Join(raw, "\n"))
	}
	return strings.TrimRight(b.String(), "\n")
}

func (r DiagnoseResult) nextStep() string {
	switch {
	case r.Suggest != "" && r.Suggest == r.Listed:
		return "Nothing to do: it is already on the " + listName(r.Suggest) + " list."
	case r.Suggest != "":
		return "Next step: add it to the " + listName(r.Suggest) + " list."
	case r.Level == "ok":
		return "Nothing to do."
	case r.Level == "warn":
		return "Run the test again."
	}
	return "The bypass cannot fix this. Check the site or your connection."
}
