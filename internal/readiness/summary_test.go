package readiness

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func checkedReport(now time.Time) Report {
	return Report{Checked: true, CheckedAt: now.Format(time.RFC3339), Rows: []Row{{Key: "cfgutil", Status: Ready}, {Key: "apple_tools", Status: Ready}, {Key: "identity", Status: Ready}, {Key: "unlocked", Status: Ready}, {Key: "runner", Status: Unverified}, {Key: "xctest_runtime", Status: Unverified}, {Key: "app_placement", Status: Unverified}, {Key: "page_removal", Status: Unverified}}}
}
func TestConnectionAndEligibilityAreIndependentAndNeverInferSavedState(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name                    string
		change                  func(*SummaryInput, *Report)
		connection, eligibility string
	}{
		{"demo", func(i *SummaryInput, r *Report) { i.Demo = true }, "Demo · no physical phone", "Planning only"},
		{"no target", func(i *SummaryInput, r *Report) { i.HasTarget = false }, "No phone configured", "Planning only"},
		{"read only", func(i *SummaryInput, r *Report) { i.HasTarget = false; i.ReadOnly = true }, "Saved phone · read-only", "Planning only"},
		{"not checked", func(i *SummaryInput, r *Report) { i.Report = nil }, "Not checked", "Unverified"},
		{"connected runtime unknown", func(i *SummaryInput, r *Report) {}, "Connected", "Unverified"},
		{"connected locked", func(i *SummaryInput, r *Report) { r.set("unlocked", NeedsSetup, "Locked") }, "Connected, locked", "Needs setup"},
		{"capture without lock", func(i *SummaryInput, r *Report) { r.set("unlocked", NotChecked, "Unknown") }, "Connected, lock unknown", "Unverified"},
		{"unreachable", func(i *SummaryInput, r *Report) { r.set("identity", Unverified, "USB read failed") }, "Read failed / unreachable", "Needs setup"},
		{"malformed identity", func(i *SummaryInput, r *Report) { r.set("identity", NeedsSetup, "Incomplete response") }, "Identity not verified", "Needs setup"},
		{"explicit mismatch", func(i *SummaryInput, r *Report) {
			r.set("identity", NeedsSetup, "Different phone")
			r.IdentityMismatch = true
		}, "Identity mismatch", "Needs setup"},
		{"stale", func(i *SummaryInput, r *Report) { r.CheckedAt = now.Add(-3 * time.Minute).Format(time.RFC3339) }, "Stale · recheck", "Unverified"},
		{"invalid timestamp", func(i *SummaryInput, r *Report) { r.CheckedAt = "bad" }, "Stale · recheck", "Unverified"},
		{"future timestamp", func(i *SummaryInput, r *Report) { r.CheckedAt = now.Add(time.Hour).Format(time.RFC3339) }, "Stale · recheck", "Unverified"},
		{"missing setup", func(i *SummaryInput, r *Report) { r.set("runner", NeedsSetup, "Build runner") }, "Connected", "Needs setup"},
		{"unsupported preference", func(i *SummaryInput, r *Report) { i.Constraint = "Bottom alignment is manual" }, "Connected", "Blocked"},
		{"all relevant facts proven", func(i *SummaryInput, r *Report) {
			for n := range r.Rows {
				r.Rows[n].Status = Ready
			}
		}, "Connected", "Ready"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := checkedReport(now)
			input := SummaryInput{HasTarget: true, Report: &report, Now: now}
			test.change(&input, &report)
			result := Summarize(input)
			if result.Connection != test.connection || result.Eligibility != test.eligibility {
				t.Fatalf("%+v", result)
			}
			if result.EligibilityReason == "" || result.ConnectionReason == "" || result.EligibilityHint == "" {
				t.Fatal("missing explanation", result)
			}
			if result.Stale && strings.Contains(result.Connection, "Connected") {
				t.Fatal("stale connection presented as current")
			}
		})
	}
}
func TestCaptureObservationsReplacePriorConnectionButDoNotProveLockOrRuntime(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	config := Config{UDID: "synthetic", ECID: "0x1"}
	base := checkedReport(now)
	success := ObserveCapture(base, config, map[string]string{"udid": "synthetic", "ecid": "0x1"}, nil, now)
	summary := Summarize(SummaryInput{HasTarget: true, Report: &success, Now: now})
	if summary.Connection != "Connected, lock unknown" || summary.Eligibility == "Ready" {
		t.Fatal(summary)
	}
	failure := ObserveCapture(base, config, nil, errors.New("read failed"), now)
	summary = Summarize(SummaryInput{HasTarget: true, Report: &failure, Now: now})
	if strings.Contains(summary.Connection, "Connected") {
		t.Fatal("old success survived failure")
	}
	if base.Find("identity").Status != Ready {
		t.Fatal("observation mutated prior report")
	}
	missing := ObserveCapture(base, config, map[string]string{}, nil, now)
	if missing.IdentityMismatch {
		t.Fatal("missing identity is not explicit mismatch")
	}
	mismatch := ObserveCapture(base, config, map[string]string{"udid": "other", "ecid": "0x1"}, nil, now)
	if !mismatch.IdentityMismatch {
		t.Fatal("explicit mismatch lost")
	}
}
func TestIdentityEvidenceDistinguishesMalformedFromMismatch(t *testing.T) {
	config := Config{UDID: "expected", ECID: "0x1"}
	if err := matchingIdentity([]byte(`{"Output":{"0x1":{"UDID":"other"}}}`), config); !errors.Is(err, errIdentityMismatch) {
		t.Fatal(err)
	}
	for _, raw := range []string{`{}`, `{"Output":{"0x1":{}}}`, `not json`} {
		if errors.Is(matchingIdentity([]byte(raw), config), errIdentityMismatch) {
			t.Fatal("malformed response labeled explicit mismatch")
		}
	}
}
