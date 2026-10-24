package readiness

import (
	"fmt"
	"strings"
	"time"
)

// FreshFor is a display limit, not a guarantee the cable is still attached. Every
// connection label includes its observation age; no background monitoring runs.
const FreshFor = 2 * time.Minute

type SummaryInput struct {
	Demo, HasTarget, ReadOnly bool
	Report                    *Report
	Constraint                string
	ConstraintHint            string
	Now                       time.Time
}
type Summary struct {
	Connection        string
	ConnectionReason  string
	Eligibility       string
	EligibilityReason string
	EligibilityHint   string
	Age               string
	Stale             bool
}

// Summarize is pure: no filesystem, processes, device calls or saved-snapshot
// inference. Proposal existence/approval is deliberately absent from this input.
func Summarize(input SummaryInput) Summary {
	summary := Summary{Connection: "Not checked", ConnectionReason: "Explicitly check the phone. A saved layout does not prove connection.", Eligibility: "Unverified", EligibilityReason: "Check the phone; execution has not been verified.", EligibilityHint: "check phone"}
	if input.Demo {
		return Summary{Connection: "Demo · no physical phone", ConnectionReason: "Synthetic local simulation only.", Eligibility: "Planning only", EligibilityReason: "Demo does not establish physical phone readiness.", EligibilityHint: "local only"}
	}
	if !input.HasTarget {
		summary.Connection = "No phone configured"
		summary.ConnectionReason = "Supply explicit --device and --ecid to enable a live target. Local planning remains available."
		summary.Eligibility = "Planning only"
		summary.EligibilityReason = "No physical target is enabled."
		summary.EligibilityHint = "no live target"
		if input.ReadOnly {
			summary.Connection = "Saved phone · read-only"
			summary.ConnectionReason = "Saved phone data does not enable a connection. Relaunch with matching explicit device identifiers and recapture."
		}
		return summary
	}
	report := Report{}
	if input.Report != nil {
		report = *input.Report
	}
	identity, lock := report.Find("identity"), report.Find("unlocked")
	if report.Checked {
		checked, err := time.Parse(time.RFC3339, report.CheckedAt)
		age := input.Now.Sub(checked)
		if err != nil || age < 0 {
			summary.Stale = true
			summary.Age = "time unknown"
		} else {
			summary.Age = ageLabel(age)
			summary.Stale = age >= FreshFor
		}
		switch {
		case summary.Stale:
			summary.Connection = "Stale · recheck"
			summary.ConnectionReason = "The last observation is not current. Check the phone again; connection is not continuously monitored."
		case identity.Status == Ready:
			summary.Connection = "Connected"
			if lock.Status == NeedsSetup {
				summary.Connection = "Connected, locked"
			} else if lock.Status != Ready {
				summary.Connection = "Connected, lock unknown"
			}
			summary.ConnectionReason = "Identity read succeeded at the displayed time; this is not a continuous connection monitor."
		case identity.Status == NeedsSetup:
			summary.Connection = "Identity not verified"
			if report.IdentityMismatch {
				summary.Connection = "Identity mismatch"
			}
			summary.ConnectionReason = identity.Detail
		case identity.Status == Unverified:
			summary.Connection = "Read failed / unreachable"
			summary.ConnectionReason = identity.Detail
		default:
			summary.ConnectionReason = "The phone was not read successfully. Check prerequisites and retry explicitly."
		}
	}
	// Known setup failures take precedence over unverified runtime; an empty or
	// untested runner never becomes eligible merely through file presence.
	for _, key := range []string{"cfgutil", "apple_tools", "runner"} {
		if row := report.Find(key); row.Status == NeedsSetup {
			summary.Eligibility = "Needs setup"
			summary.EligibilityReason = row.Detail
			summary.EligibilityHint = map[string]string{"cfgutil": "Configurator missing", "apple_tools": "Xcode tools missing", "runner": "runner not built"}[key]
			return summary
		}
	}
	if !summary.Stale && report.Checked && (identity.Status == NeedsSetup || identity.Status == Unverified) {
		summary.Eligibility = "Needs setup"
		summary.EligibilityReason = "Fix phone connection/identity and check again."
		summary.EligibilityHint = "check connection"
		if identity.Status == NeedsSetup {
			summary.EligibilityHint = "verify identity"
		}
		return summary
	}
	if !summary.Stale && identity.Status == Ready && lock.Status == NeedsSetup {
		summary.Eligibility = "Needs setup"
		summary.EligibilityReason = "Unlock the matched phone and recheck."
		summary.EligibilityHint = "unlock phone"
		return summary
	}
	if input.Constraint != "" {
		summary.Eligibility = "Blocked"
		summary.EligibilityReason = input.Constraint
		summary.EligibilityHint = input.ConstraintHint
		if summary.EligibilityHint == "" {
			summary.EligibilityHint = "manual layout"
		}
		return summary
	}
	if summary.Stale {
		summary.EligibilityReason = "A fresh phone check is required."
		summary.EligibilityHint = "recheck phone"
		return summary
	}
	if !report.Checked || identity.Status != Ready || lock.Status != Ready {
		summary.EligibilityReason = "Connection and unlocked state are not both verified."
		summary.EligibilityHint = "check phone / lock"
		return summary
	}
	for _, key := range []string{"cfgutil", "apple_tools", "runner", "xctest_runtime", "app_placement", "page_removal"} {
		if report.Find(key).Status != Ready {
			summary.EligibilityReason = "Phone automation has not been proven; see readiness details."
			summary.EligibilityHint = "XCTest not verified"
			return summary
		}
	}
	summary.Eligibility = "Ready"
	summary.EligibilityHint = "prerequisites met"
	summary.EligibilityReason = "Relevant prerequisites were established. A supported proposal, complete review, exact confirmation and final fresh checks are still required."
	return summary
}
func ageLabel(age time.Duration) string {
	if age < 10*time.Second {
		return "just checked"
	}
	if age < time.Minute {
		return fmt.Sprintf("%ds ago", int(age/time.Second))
	}
	if age < time.Hour {
		return fmt.Sprintf("%dm ago", int(age/time.Minute))
	}
	return fmt.Sprintf("%dh ago", int(age/time.Hour))
}

// ObserveCapture replaces connection facts after an explicitly requested capture.
// Success proves identity/read access only, never lock or XCTest runtime. An error
// clears previous success rather than leaving an old Connected indicator behind.
func ObserveCapture(base Report, config Config, identity map[string]string, captureErr error, now time.Time) Report {
	base.Rows = append([]Row(nil), base.Rows...)
	base.IdentityMismatch = false
	base.Checked = true
	base.CheckedAt = now.UTC().Format(time.RFC3339)
	base.set("unlocked", NotChecked, "A layout capture does not check lock state. Use Check phone for a fresh lock read.")
	if captureErr != nil {
		base.set("identity", Unverified, "The latest capture failed; connection is not established. "+captureErr.Error())
		base.set("app_placement", Unverified, "The latest phone read failed. Recheck before attempting placement.")
		return base
	}
	if identity["udid"] != config.UDID || !strings.EqualFold(identity["ecid"], config.ECID) {
		base.IdentityMismatch = identity["udid"] != "" && identity["udid"] != config.UDID || identity["ecid"] != "" && !strings.EqualFold(identity["ecid"], config.ECID)
		base.set("identity", NeedsSetup, "Capture did not establish the configured phone identity. Verify ECID/UDID pairing.")
		base.set("app_placement", NeedsSetup, "The captured phone identity does not match the configured target.")
		return base
	}
	base.set("identity", Ready, "The configured identity and layout were read successfully at this time. Lock and automation remain unverified.")
	base.set("app_placement", Unverified, "Layout read succeeded; placement, lock state and XCTest execution are not established by capture.")
	return base
}
