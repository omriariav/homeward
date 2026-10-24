package terminal

import (
	"context"
	"fmt"
	"strings"

	"github.com/omriariav/homeward/internal/readiness"
)

func (a *App) readinessConfig() readiness.Config {
	return readiness.Config{Root: a.Options.Root, Cfgutil: a.Options.Cfgutil, UDID: a.Options.Device, ECID: a.Options.ECID}
}
func (s *screen) readiness() error {
	report := s.app.ReadinessChecker.Inspect(s.app.readinessConfig())
	if s.app.Readiness != nil {
		report = *s.app.Readiness
	}
	s.app.Readiness = &report
	selected := 0
	for {
		mode := "Physical phone readiness is separate from local planning."
		if s.app.Options.Demo {
			mode = "DEMO: simulation only. No physical readiness is established."
		} else if s.app.Options.Device == "" {
			mode = "LOCAL: no live target is enabled. Plan without a phone."
		}
		rows := report.Rows
		selected = min(selected, len(rows)-1)
		listRows := max(1, s.rows()-3)
		start := max(0, selected-listRows+1)
		w, _ := s.size()
		body := []screenLine{}
		for i := start; i < min(len(rows), start+listRows); i++ {
			row := rows[i]
			prefix := "  "
			tone := ""
			if i == selected {
				prefix = "> "
				tone = "selected"
			}
			body = append(body, screenLine{prefix + pad(row.Title, max(15, w-24)) + string(row.Status), tone})
		}
		for len(body) < listRows {
			body = append(body, line(""))
		}
		blockers := s.applyProblems()
		aggregate := "Proposal can be reviewed; actual device execution remains unverified."
		if len(blockers) > 0 {
			aggregate = "Apply: " + blockers[0]
		}
		if s.app.Review == nil {
			aggregate = "Ready to plan locally. No proposal yet — CREATE PROPOSAL first."
		}
		body = append(body, line(""), dim(aggregate))
		footer := "↑↓ details  Enter open  c explicit check  Esc back"
		fits := s.paint("Readiness & capabilities", mode, body, footer)
		k, err := s.key()
		if err != nil {
			return err
		}
		if isBack(k) {
			return nil
		}
		if !fits {
			continue
		}
		if down(k) {
			selected = (selected + 1) % len(rows)
		}
		if up(k) {
			selected = (selected + len(rows) - 1) % len(rows)
		}
		if k.Name == "enter" {
			row := rows[selected]
			text := string(row.Status) + "\n\n" + row.Detail
			if row.Key == "app_placement" || row.Key == "page_removal" {
				text += "\n\nCurrent plan / approval requirements:\n" + strings.Join(blockers, "\n")
				if len(blockers) == 0 {
					text += "Final preflight, exact confirmation and readback remain required."
				}
			}
			if report.Checked {
				text += "\n\nLast read-only check: " + report.CheckedAt + "\nThis is not a continuous monitor."
			}
			if _, err = s.viewer(row.Title, text); err != nil {
				return err
			}
		}

		if isRune(k, 'c') || isRune(k, 'p') {
			if err = s.checkPhone(); err != nil {
				return err
			}
			if s.app.Readiness != nil {
				report = *s.app.Readiness
			}
		}
	}
}
func (s *screen) performPhoneCheck() error {
	var report readiness.Report
	err := s.app.UI.Work(s.ctx, "Reading identity and lock state", func(ctx context.Context) error {
		var e error
		report, e = s.app.ReadinessChecker.Check(ctx, s.app.readinessConfig())
		return e
	})
	s.app.Readiness = &report
	if _, saveErr := s.app.save("readiness", report); saveErr != nil {
		return saveErr
	}
	if err != nil {
		_, viewErr := s.viewer("Phone check stopped", fmt.Sprintf("%v\n\nThe summary shows only what was established. No mutation was attempted.", err))
		return viewErr
	}
	return nil
}
