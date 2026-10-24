package terminal

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/omriariav/homeward/internal/core"
	"github.com/omriariav/homeward/internal/device"
	"github.com/omriariav/homeward/internal/organizer"
)

// Session stores data, never executable paths, runtime readiness or approval.
// Resuming creates a new evidence directory; previous artifacts remain untouched.
type Session struct {
	Version          int               `json:"version"`
	Kind             string            `json:"kind"`
	Demo             bool              `json:"demo"`
	Snapshot         *device.Snapshot  `json:"snapshot,omitempty"`
	Metadata         core.Metadata     `json:"metadata"`
	Request          string            `json:"request"`
	Preferences      core.Preferences  `json:"preferences"`
	Interview        *Interview        `json:"interview,omitempty"`
	Artifact         *core.Artifact    `json:"artifact,omitempty"`
	Provider         string            `json:"provider"`
	Model            string            `json:"model"`
	RefusedAI        bool              `json:"refused_ai"`
	Mode             string            `json:"mode"`
	WizardStep       int               `json:"wizard_step"`
	InterviewSkipped bool              `json:"interview_skipped"`
	FormDrafts       map[string]string `json:"form_drafts,omitempty"`
}

func loadSession(path string) (*Session, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		path = filepath.Join(path, "session.json")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var session Session
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&session); err != nil {
		return nil, fmt.Errorf("invalid session: %w", err)
	}
	if err = decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, errors.New("session must contain exactly one JSON value")
	}
	if session.Version != 1 || session.Kind != "homeward-session" {
		return nil, errors.New("not a supported Homeward session file")
	}
	if session.Provider != "claude" && session.Provider != "codex" {
		return nil, errors.New("invalid saved provider")
	}
	if session.WizardStep < 0 || session.WizardStep > 6 || session.Mode != "" && session.Mode != "wizard" && session.Mode != "custom" {
		return nil, errors.New("invalid saved wizard state")
	}
	p := session.Preferences
	if p.MaxPages < 1 || p.MaxPages > 15 || p.Alignment != "preserve" && p.Alignment != "standard" && p.Alignment != "bottom" {
		return nil, errors.New("invalid saved preferences")
	}
	if session.Snapshot != nil {
		state, err := core.Normalize(session.Snapshot.Layout, session.Metadata)
		if err != nil {
			return nil, err
		}
		known := map[string]bool{}
		icons := map[string]bool{}
		for _, item := range state.Items {
			known[item.ID] = true
			icons[item.ID] = item.Kind == "icon"
		}
		for _, ids := range [][]string{p.Keep, p.Dock} {
			seen := map[string]bool{}
			for _, id := range ids {
				if !known[id] || seen[id] {
					return nil, errors.New("saved selections contain unknown or repeated item IDs")
				}
				seen[id] = true
			}
		}
		if len(p.Dock) > 4 {
			return nil, errors.New("saved Dock exceeds four apps")
		}
		for _, id := range p.Dock {
			if !icons[id] {
				return nil, errors.New("saved Dock contains a widget")
			}
		}
	} else if session.Artifact != nil || len(p.Keep) > 0 || p.Dock != nil || session.Interview != nil {
		return nil, errors.New("saved source-bound data has no layout")
	}
	if session.Interview != nil {
		interview := session.Interview
		if interview.Version != 1 || interview.Kind != "homescreen-interview" || interview.Request != session.Request {
			return nil, errors.New("saved interview does not match its context")
		}
		if err = core.ValidateQuestionnaire(core.Questionnaire{Summary: "Saved interview", Questions: interview.Questions}, 6); err != nil {
			return nil, err
		}
		for _, q := range interview.Questions {
			switch q.Status {
			case "pending", "skipped":
				if q.Answer != nil {
					return nil, errors.New("pending/skipped answer cannot contain assumed text")
				}
			case "answered":
				if q.Answer == nil || strings.TrimSpace(*q.Answer) == "" {
					return nil, errors.New("answered question lacks an answer")
				}
			default:
				return nil, errors.New("invalid saved answer status")
			}
		}
	}
	if session.Artifact != nil {
		artifact := session.Artifact
		if artifact.Input.Request != session.Request || artifact.Input.MaxPages != session.Preferences.MaxPages || !reflect.DeepEqual(artifact.Input.Preferences, session.Preferences) {
			return nil, errors.New("saved proposal does not match the current context/preferences; regenerate it")
		}
		state, e := core.Normalize(session.Snapshot.Layout, session.Metadata)
		if e != nil {
			return nil, e
		}
		form := &App{Snapshot: session.Snapshot, Request: session.Request, Metadata: session.Metadata, Preferences: session.Preferences, Interview: session.Interview}
		expected := form.input(state)
		if !reflect.DeepEqual(artifact.Input.RequiredVisible, expected.RequiredVisible) || !reflect.DeepEqual(artifact.Input.InterviewAnswers, expected.InterviewAnswers) {
			return nil, errors.New("saved proposal selection/answers do not match the saved form")
		}
		if _, err = organizer.NewReview(*session.Snapshot, *session.Artifact, session.Preferences); err != nil {
			return nil, fmt.Errorf("saved proposal is invalid: %w", err)
		}
	}
	// A stored acknowledgement is not renewed consent. The draft is retained for
	// review, but automatic Apply needs a fresh acknowledgement and regeneration.
	session.Preferences.AcceptReflow = false
	if session.Artifact != nil {
		session.Artifact.Input.Preferences.AcceptReflow = false
	}
	return &session, nil
}
func (a *App) SaveSession() error {
	if a.Directory == "" {
		return nil
	}
	session := Session{Version: 1, Kind: "homeward-session", Demo: a.Options.Demo, Snapshot: a.Snapshot, Metadata: a.Metadata, Request: a.Request, Preferences: a.Preferences, Interview: a.Interview, Artifact: a.Artifact, Provider: a.Options.Provider, Model: a.Options.Model, RefusedAI: a.RefusedAI, Mode: a.Mode, WizardStep: a.WizardStep, InterviewSkipped: a.InterviewSkipped, FormDrafts: a.FormDrafts}
	raw, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(a.Directory, ".session-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err = file.Write(append(raw, '\n')); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(name, filepath.Join(a.Directory, "session.json"))
}
func resumeOptions(options Options, session *Session) (Options, error) {
	if options.Layout != "" || options.Metadata != "" || options.Demo {
		return options, errors.New("--resume cannot replace saved source data; omit --layout, --metadata and --demo")
	}
	if options.Device != "" {
		if session.Snapshot == nil || session.Snapshot.Identity["udid"] != options.Device || !strings.EqualFold(session.Snapshot.Identity["ecid"], options.ECID) {
			return options, errors.New("explicit device/ECID do not match the saved session; no phone command was run")
		}
	}
	if options.Provider == "" {
		options.Provider = session.Provider
	}
	if options.Model == "" && options.Provider == session.Provider {
		options.Model = session.Model
	}
	options.Demo = session.Demo
	return options, nil
}
func (a *App) restore(session *Session) error {
	a.Snapshot = session.Snapshot
	a.Metadata = session.Metadata
	a.Request = session.Request
	a.Preferences = session.Preferences
	a.Interview = session.Interview
	a.Artifact = session.Artifact
	a.Mode = session.Mode
	a.WizardStep = session.WizardStep
	a.InterviewSkipped = session.InterviewSkipped
	a.FormDrafts = session.FormDrafts
	if a.FormDrafts == nil {
		a.FormDrafts = map[string]string{}
	}
	a.RefusedAI = a.Options.NoAI || a.Options.Demo || session.RefusedAI
	a.Reviewed = ""
	if a.Options.Device != "" {
		a.NeedsCapture = true
	} else if session.Snapshot != nil {
		a.Device = device.NewLocal(*session.Snapshot)
		a.ResumeReadOnly = session.Snapshot.Identity["udid"] != ""
	}
	if a.Artifact != nil {
		review, err := organizer.NewReview(*a.Snapshot, *a.Artifact, a.Preferences)
		if err != nil {
			return err
		}
		a.Review = &review
	}
	return a.SaveSession()
}
func (a *App) sessionApplyProblem() string {
	if a.ResumeReadOnly {
		return "This resumed phone session is read-only. Relaunch with matching --device and --ecid to enable a live target; then recapture it."
	}
	if a.NeedsCapture {
		return "Resumed phone state is not current. Read the phone layout again before reviewing or applying."
	}
	return ""
}
func (a *App) SkipInterview() error {
	if a.Interview != nil {
		for i := range a.Interview.Questions {
			q := &a.Interview.Questions[i]
			if q.Status == "pending" {
				q.Status = "skipped"
				q.Answer = nil
			}
		}
	}
	a.InterviewSkipped = true
	a.invalidate()
	return a.SaveSession()
}
