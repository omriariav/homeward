package terminal

import (
	"strings"
	"time"

	"github.com/omriariav/homeward/internal/core"
	"github.com/omriariav/homeward/internal/readiness"
)

// PhoneSummary reads only current in-memory facts. In particular it never turns a
// saved/resumed snapshot into evidence that a physical device is connected.
func (a *App) PhoneSummary(now time.Time) readiness.Summary {
	detail, hint := a.phoneConstraint()
	return readiness.Summarize(readiness.SummaryInput{Demo: a.Options.Demo, HasTarget: a.Options.Device != "" && a.Options.ECID != "", ReadOnly: a.ResumeReadOnly, Report: a.Readiness, Constraint: detail, ConstraintHint: hint, Now: now})
}
func (a *App) phoneConstraint() (string, string) {
	if a.Snapshot != nil {
		state, err := core.Normalize(a.Snapshot.Layout, a.Metadata)
		if err != nil {
			return "The saved layout needs review before automatic placement.", "review saved layout"
		}
		known := map[string]bool{}
		for _, id := range a.Snapshot.AppIDs {
			known[id] = true
		}
		seen := map[string]bool{}
		for _, item := range state.Items {
			if item.Kind == "widget" {
				return "Widget movement needs manual arrangement.", "manual widgets"
			}
			if item.SourceID == "/" || strings.Contains(item.SourceID, "://") || (len(known) > 0 && !known[item.SourceID]) {
				return "Unresolved shortcuts need manual handling.", "manual shortcuts"
			}
			if seen[item.SourceID] {
				return "Repeated icons need manual handling.", "repeated icons"
			}
			seen[item.SourceID] = true
		}
	}
	if strings.TrimSpace(a.Preferences.WidgetSpace) != "" {
		return "Reserved space needs manual arrangement.", "manual spacing"
	}
	if a.Preferences.Alignment == "bottom" {
		return "Bottom alignment needs manual arrangement.", "manual alignment"
	}
	if a.Preferences.Alignment != "standard" {
		return "Preserved icon positions need manual arrangement.", "manual positions"
	}
	return "", ""
}
func (s *screen) phoneStatusLines() []screenLine {
	status := s.app.PhoneSummary(time.Now())
	w, _ := s.size()
	connection := status.Connection
	if status.Age != "" {
		connection += " · " + status.Age
	}
	hint := " · p Check"
	if status.Stale {
		hint = " · p Recheck"
	}
	first := "Phone: " + clip(connection, max(1, w-1-visibleWidth("Phone: "+hint))) + hint
	hint = " · r Details"
	eligibility := status.Eligibility
	if status.EligibilityHint != "" {
		eligibility += " · " + status.EligibilityHint
	}
	second := "Eligibility: " + clip(eligibility, max(1, w-1-visibleWidth("Eligibility: "+hint))) + hint
	return []screenLine{line(first), dim(second)}
}
func (s *screen) checkPhone() error {
	if s.app.Options.Device == "" || s.app.Options.Demo {
		_, err := s.viewer("No physical check", "No live phone is configured. Local planning is available. Relaunch with explicit --device and --ecid to enable read-only phone checks.")
		return err
	}
	s.title = "Check phone"
	s.history = []string{"This reads the configured phone identity and lock state. It does not move apps, launch XCTest or verify phone automation. The result is a dated observation, not continuous monitoring."}
	answer, err := s.ask("Read the configured phone now? [y/N] ")
	if err == errBack {
		return nil
	}
	if err != nil {
		return err
	}
	if !yes(answer) {
		return nil
	}
	return s.performPhoneCheck()
}
