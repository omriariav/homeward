package terminal

import (
	"errors"
	"fmt"
	"io"
	"strings"
)

var errModes = errors.New("mode chooser")
var errCustom = errors.New("custom mode")
var errWizard = errors.New("wizard mode")

// Original Homeward illustration: a house beside a phone. It is compacted to
// leave navigation and controls visible, rather than making a screen scroll.
func homewardArt(width, height int) []screenLine {
	art := []string{"      /\\       .------.", "     /  \\      | :  : |", "    /____\\     | :  : |", "    | [] |     |______|", "    |____|       (__)  "}
	if height < 24 {
		art = []string{"   /\\    .----.", "  /__\\   | :: |", "  |__|   '----'"}
	}
	indent := strings.Repeat(" ", max(0, (width-26)/2))
	lines := []screenLine{}
	for _, text := range art {
		lines = append(lines, accent(indent+text))
	}
	return lines
}
func (s *screen) runModes() error {
	mode := ""
	for {
		if mode == "" {
			choice, err := s.modeChooser()
			if err != nil {
				return err
			}
			mode = choice
		}
		s.app.Mode = mode
		if err := s.app.SaveSession(); err != nil {
			return err
		}
		var err error
		if mode == "wizard" {
			err = s.wizard()
		} else {
			err = s.dashboard()
		}
		switch {
		case errors.Is(err, errModes):
			mode = ""
		case errors.Is(err, errCustom):
			mode = "custom"
		case errors.Is(err, errWizard):
			mode = "wizard"
		default:
			return err
		}
	}
}
func (s *screen) modeChooser() (string, error) {
	selected := 0
	for {
		w, h := s.size()
		body := homewardArt(w, h)
		body = append(body, line(""))
		labels := []string{"Wizard · recommended", "Custom · your dashboard", "Phone readiness & capabilities", "Session / resume help"}
		for i, label := range labels {
			prefix := "  "
			tone := ""
			if selected == i {
				prefix = "> "
				tone = "selected"
			}
			body = append(body, screenLine{fmt.Sprintf("%s%d  %s", prefix, i+1, label), tone})
		}
		status := "No proposal yet — create one before review or Apply."
		if s.app.Review != nil {
			status = "Saved proposal available. Review it before Apply."
		}
		body = append(body, line(""), dim(status))
		fits := s.paint("Choose your mode", "One form, two ways to work. Switching keeps your progress.", body, "↑↓ Enter choose  1–4 shortcut  Esc / q quit")
		k, err := s.key()
		if err != nil {
			return "", err
		}
		if isBack(k) {
			return "", errQuit
		}
		if !fits {
			continue
		}
		if down(k) {
			selected = (selected + 1) % len(labels)
		}
		if up(k) {
			selected = (selected + len(labels) - 1) % len(labels)
		}
		activate := k.Name == "enter"
		if k.Name == "rune" && k.Rune >= '1' && k.Rune <= '4' {
			selected = int(k.Rune - '1')
			activate = true
		}
		if !activate {
			continue
		}
		switch selected {
		case 0:
			return "wizard", nil
		case 1:
			return "custom", nil
		case 2:
			if err = s.readiness(); err != nil {
				return "", err
			}
		case 3:
			if err = s.sessionDetails(); err != nil {
				return "", err
			}
		}
	}
}
func (s *screen) sessionDetails() error {
	text := "Wizard and Custom share your context, preferences, answers and proposal.\n\nSession file:\n" + s.app.Directory + "/session.json\n\nResume with:\nhomeward tui --resume PATH_TO_SESSION\n\nSubmitted values and escaped text drafts are saved. Escape leaves a text edit unsubmitted so you can continue it later. Context/source changes explain which dependent answers or selections are cleared.\n\nResumed proposals are unreviewed. AI consent and Apply approval never resume. A phone session opens read-only unless matching --device and --ecid are explicitly supplied; it must then be recaptured."
	_, err := s.viewer("Your session", text)
	return err
}
func (s *screen) interviewComplete() bool {
	if s.app.Interview == nil {
		return false
	}
	for _, q := range s.app.Interview.Questions {
		if q.Status == "pending" {
			return false
		}
	}
	return true
}
func (s *screen) savedAnswers() error {
	if s.app.Interview == nil {
		_, err := s.viewer("Interview", "No answers yet. The interview is optional.")
		return err
	}
	lines := []string{}
	for _, q := range s.app.Interview.Questions {
		answer := "Not answered"
		if q.Status == "skipped" {
			answer = "Explicitly skipped"
		} else if q.Answer != nil {
			answer = *q.Answer
		}
		lines = append(lines, q.Question, answer, "")
	}
	_, err := s.viewer("Saved interview answers", strings.Join(lines, "\n"))
	return err
}

type wizardAction struct {
	label string
	run   func() error
}

func (s *screen) wizard() error {
	s.notice = ""
	selected := 0
	lastStep := -1
	advance := func() error { s.app.WizardStep = min(6, s.app.WizardStep+1); return s.app.SaveSession() }
	for {
		step := s.app.WizardStep
		if step != lastStep {
			selected = 0
			lastStep = step
		}
		titles := []string{"Source & readiness", "Your context", "Layout preferences", "Optional interview", "Create proposal", "Review proposal", "Optional Apply"}
		descriptions := []string{"Start locally, or explicitly read a configured phone.", "Tell us what matters before choosing apps.", "A page budget and must-keeps shape the shortlist.", "Keep your answers, or explicitly skip this step.", "Create a proposal before review or Apply.", "Read every section; no approval is implied.", "Review is complete only when every section was read. Apply is optional."}
		summary := []string{}
		actions := []wizardAction{}
		editContext := func() error { s.title = "Your context"; s.history = nil; return s.app.Context() }
		interview := func() error { s.title = "Interview"; s.history = nil; return s.app.Questionnaire(s.ctx) }
		source := func() error { s.title = "Read / import layout"; s.history = nil; return s.app.Capture(s.ctx) }
		create := func() error {
			s.title = "Create proposal"
			s.history = nil
			if err := s.app.CreateProposal(s.ctx); err != nil {
				return err
			}
			if s.app.Review != nil {
				return advance()
			}
			return nil
		}
		switch step {
		case 0:
			if s.app.Snapshot != nil {
				summary = []string{"A saved layout is loaded. No phone read is needed to plan."}
				if s.app.NeedsCapture {
					summary = []string{"Resumed phone snapshot: recapture before any live Apply."}
				}
				if s.app.ResumeReadOnly {
					summary = []string{"Resumed phone data is read-only. Planning remains available."}
				}
				actions = append(actions, wizardAction{"Continue with loaded layout", advance}, wizardAction{"Read / replace source", source})
			} else {
				summary = []string{"No layout loaded yet."}
				actions = append(actions, wizardAction{"Read / import a layout", source})
			}
			actions = append(actions, wizardAction{"Readiness & capabilities", s.readiness})
		case 1:
			if s.app.Request == "" {
				summary = []string{"No context yet."}
				actions = append(actions, wizardAction{"Describe your priorities", editContext})
			} else {
				summary = []string{s.app.Request}
				actions = append(actions, wizardAction{"Continue with saved context", advance}, wizardAction{"Edit context", editContext})
			}
		case 2:
			summary = []string{fmt.Sprintf("%s · %d must-keeps · %s alignment", plural(s.app.Preferences.MaxPages, "page", "pages"), len(s.app.Preferences.Keep), s.app.Preferences.Alignment), "Unsupported spacing can still be planned manually."}
			actions = append(actions, wizardAction{"Edit layout preferences", func() error {
				if err := s.preferences(); err != nil {
					return err
				}
				selected = 1
				return nil
			}}, wizardAction{"Continue with these preferences", advance})
		case 3:
			if s.interviewComplete() || s.app.InterviewSkipped {
				summary = []string{"Saved answers / explicit skips are retained."}
				actions = append(actions, wizardAction{"Continue with saved answers / skip", advance}, wizardAction{"Review saved answers", s.savedAnswers})
			} else {
				summary = []string{"Questions are optional. Skipped answers remain unknown."}
				actions = append(actions, wizardAction{"Answer / continue interview", interview}, wizardAction{"Skip optional interview", func() error {
					if err := s.app.SkipInterview(); err != nil {
						return err
					}
					return advance()
				}})
			}
		case 4:
			if s.app.Review == nil {
				summary = []string{"No proposal yet — create one before review or Apply."}
				actions = append(actions, wizardAction{"CREATE PROPOSAL", create})
			} else {
				summary = []string{"Your proposal is saved. Nothing has been applied."}
				actions = append(actions, wizardAction{"Continue to review", advance}, wizardAction{"Regenerate proposal", create})
			}
		case 5:
			if s.app.Review == nil {
				summary = []string{"Your proposal was invalidated by an edit. Create a new one."}
				actions = append(actions, wizardAction{"Go to CREATE PROPOSAL", func() error { s.app.WizardStep = 4; return nil }})
			} else if s.app.Reviewed == s.app.Review.Digest() {
				summary = []string{"Every proposal section has been read."}
				actions = append(actions, wizardAction{"Continue after review", advance}, wizardAction{"Review again", s.app.View})
			} else {
				summary = []string{"Read the overview, Dock, pages, changes, exclusions and notes."}
				actions = append(actions, wizardAction{"Review every section", s.app.View})
			}
		case 6:
			summary = []string{"You can finish with a manual guide. Apply is a separate choice."}
			actions = append(actions, wizardAction{"Finish without applying · open Custom", func() error { return errCustom }})
			problems := s.applyProblems()
			if len(problems) > 0 {
				actions = append(actions, wizardAction{"Apply unavailable · see why", func() error { _, err := s.viewer("Apply unavailable", strings.Join(problems, "\n\n")); return err }})
			} else {
				actions = append(actions, wizardAction{"Apply reviewed proposal…", func() error { s.title = "Apply reviewed proposal"; s.history = nil; return s.app.Apply(s.ctx) }})
			}
		}
		selected = min(selected, max(0, len(actions)-1))
		body := []screenLine{}
		for _, text := range summary {
			body = append(body, line(text))
		}
		body = append(body, line(""))
		for i, action := range actions {
			prefix := "  "
			tone := ""
			if i == selected {
				prefix = "> "
				tone = "selected"
			}
			body = append(body, screenLine{prefix + action.label, tone})
		}
		body = append(body, line(""))
		if s.notice != "" {
			body = append(body, dim(s.notice))
		} else {
			body = append(body, dim("Your form stays saved when you go Back or switch modes."))
		}
		fits := s.paint(fmt.Sprintf("Wizard  %d/7 · %s", step+1, titles[step]), descriptions[step], body, "↑↓ Enter  Esc back  c Custom  r readiness  q quit")
		k, err := s.key()
		if err != nil {
			return err
		}
		if isRune(k, 'q') {
			return nil
		}
		if k.Name == "esc" {
			if step == 0 {
				return errModes
			}
			s.app.WizardStep--
			if err = s.app.SaveSession(); err != nil {
				return err
			}
			continue
		}
		if !fits {
			continue
		}
		if isRune(k, 'c') {
			return errCustom
		}
		if isRune(k, 'r') {
			if err = s.readiness(); err != nil {
				return err
			}
			continue
		}
		if down(k) {
			selected = (selected + 1) % len(actions)
		}
		if up(k) {
			selected = (selected + len(actions) - 1) % len(actions)
		}
		if k.Name != "enter" {
			continue
		}
		err = actions[selected].run()
		if len(s.history) > 0 {
			s.notice = s.history[len(s.history)-1]
		}
		if errors.Is(err, errCustom) || errors.Is(err, errModes) || errors.Is(err, errQuit) || errors.Is(err, io.EOF) {
			return err
		}
		if errors.Is(err, errBack) {
			continue
		}
		if err != nil {
			if _, e := s.viewer("Action stopped", err.Error()); e != nil {
				return e
			}
		}
		if err = s.app.SaveSession(); err != nil {
			return err
		}
	}
}
