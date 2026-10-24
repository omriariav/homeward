// Package terminal provides dependency-free plain and full-screen interfaces.
package terminal

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"

	"github.com/omriariav/homeward/internal/readiness"
	"strconv"
	"strings"
	"time"

	"github.com/omriariav/homeward/internal/core"
	"github.com/omriariav/homeward/internal/device"
	"github.com/omriariav/homeward/internal/organizer"
)

type Options struct {
	Root, OutputDir, Layout, Metadata, Device, ECID, Cfgutil, Provider, Model, Resume string
	Demo, NoAI                                                                        bool
}
type Terminal struct {
	Read        func(string) (string, error)
	InputKey    string
	Out         io.Writer
	Paginate    bool
	PrintScreen func(string)
	ShowScreen  func(string) (bool, error)
	WorkScreen  func(context.Context, string, func(context.Context) error) error
}

func NewTerminal(input io.Reader, output io.Writer, paginate bool) *Terminal {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	t := &Terminal{Out: output, Paginate: paginate}
	t.Read = func(prompt string) (string, error) {
		fmt.Fprint(output, Safe(prompt))
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return "", err
			}
			return "", io.EOF
		}
		return strings.TrimSpace(scanner.Text()), nil
	}
	return t
}
func (t *Terminal) Print(text string) {
	if t.PrintScreen != nil {
		t.PrintScreen(text)
		return
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = Safe(line)
	}
	fmt.Fprintln(t.Out, strings.Join(lines, "\n"))
}
func (t *Terminal) Ask(prompt string) (string, error) {
	value, err := t.Read(Safe(prompt))
	if strings.TrimSpace(value) == "/quit" {
		return "", io.EOF
	}
	return strings.TrimSpace(value), err
}
func (t *Terminal) Show(text string) (bool, error) {
	if t.ShowScreen != nil {
		return t.ShowScreen(text)
	}
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	for start := 0; start < len(lines); start += 22 {
		end := min(start+22, len(lines))
		t.Print(strings.Join(lines[start:end], "\n"))
		if t.Paginate && end < len(lines) {
			choice, err := t.Ask("Enter: next | q: stop viewing > ")
			if err != nil {
				return false, err
			}
			if strings.EqualFold(choice, "q") {
				return false, nil
			}
		}
	}
	return true, nil
}

// Work is a presentation seam: plain mode stays synchronous; the screen can show
// cancellable activity without changing the underlying operation or approval.
func (t *Terminal) Work(ctx context.Context, title string, fn func(context.Context) error) error {
	if t.WorkScreen != nil {
		return t.WorkScreen(ctx, title, fn)
	}
	return fn(ctx)
}

type Interview struct {
	Version    int             `json:"version"`
	Kind       string          `json:"kind"`
	Request    string          `json:"request"`
	Questions  []core.Question `json:"questions"`
	Generation string          `json:"generation"`
}

type App struct {
	Options          Options
	UI               *Terminal
	Device           device.Device
	AI               core.AIClient
	Directory        string
	Snapshot         *device.Snapshot
	Metadata         core.Metadata
	Request          string
	Preferences      core.Preferences
	Interview        *Interview
	Artifact         *core.Artifact
	Review           *organizer.Review
	Reviewed         string
	RefusedAI        bool
	serial           int
	Mode             string
	WizardStep       int
	InterviewSkipped bool
	FormDrafts       map[string]string
	NeedsCapture     bool
	ResumeReadOnly   bool
	Readiness        *readiness.Report
	ReadinessChecker readiness.Checker
}

func New(options Options, ui *Terminal, target device.Device) (*App, error) {
	var resumed *Session
	if options.Resume != "" {
		var err error
		resumed, err = loadSession(options.Resume)
		if err != nil {
			return nil, err
		}
		options, err = resumeOptions(options, resumed)
		if err != nil {
			return nil, err
		}
	}
	if (options.Device == "") != (options.ECID == "") {
		return nil, errors.New("live targets require both --device UDID and --ecid ECID; capture verifies their pairing")
	}
	if options.Demo && (options.Device != "" || options.Metadata != "" || options.Layout != "") {
		return nil, errors.New("--demo uses synthetic data only; omit device/layout/metadata flags")
	}
	if options.Provider == "" {
		options.Provider = "claude"
	}
	if options.Provider != "claude" && options.Provider != "codex" {
		return nil, errors.New("provider must be claude or codex")
	}
	if options.OutputDir == "" {
		options.OutputDir = filepath.Join(options.Root, "artifacts", "tui")
	}
	if err := os.MkdirAll(options.OutputDir, 0700); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(options.OutputDir, "session-")
	if err != nil {
		return nil, err
	}
	app := &App{Options: options, UI: ui, Directory: dir, Device: target, RefusedAI: options.NoAI || options.Demo,
		Preferences: core.Preferences{MaxPages: 2, Alignment: "preserve"}, FormDrafts: map[string]string{}}
	if app.Device == nil && options.Device != "" {
		app.Device = device.NewLive(options.Root, options.Device, options.ECID, dir, options.Cfgutil)
	}
	if resumed != nil {
		if err := app.restore(resumed); err != nil {
			return nil, err
		}
		return app, nil
	}
	if options.Metadata != "" {
		if err := ReadJSON(options.Metadata, &app.Metadata); err != nil {
			return nil, err
		}
	}
	if options.Demo {
		layout, metadata := Demo()
		app.Metadata = metadata
		if err := app.Import(layout); err != nil {
			return nil, err
		}
	}
	if options.Layout != "" {
		var layout []any
		if err := ReadJSON(options.Layout, &layout); err != nil {
			return nil, err
		}
		if err := app.Import(layout); err != nil {
			return nil, err
		}
	}
	if err := app.SaveSession(); err != nil {
		return nil, err
	}
	return app, nil
}
func ReadJSON(path string, value any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(data, value); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}
func Demo() ([]any, core.Metadata) {
	layout := []any{[]any{"demo.phone", "demo.messages", "demo.browser"}, []any{[]any{"Work", "demo.mail", "demo.calendar", "demo.tasks"}, []any{"Interests", "demo.books", "demo.music", "demo.camera"}, []any{"Distractions", "demo.social", "demo.games", "demo.shopping"}, "demo.maps"}}
	metadata := core.Metadata{Apps: map[string]core.AppMetadata{}}
	for _, name := range []string{"phone", "messages", "browser", "mail", "calendar", "tasks", "books", "music", "camera", "social", "games", "shopping", "maps"} {
		metadata.Apps["demo."+name] = core.AppMetadata{Label: strings.ToUpper(name[:1]) + name[1:]}
	}
	return layout, metadata
}
func (a *App) save(name string, value any) (string, error) {
	a.serial++
	path := filepath.Join(a.Directory, fmt.Sprintf("%03d-%s.json", a.serial, name))
	if err := organizer.SaveJSON(path, value); err != nil {
		return path, err
	}
	return path, a.SaveSession()
}
func (a *App) invalidate() { a.Artifact = nil; a.Review = nil; a.Reviewed = "" }
func (a *App) state() (core.State, error) {
	if a.Snapshot == nil {
		return core.State{}, errors.New("import or capture a layout first (menu 1)")
	}
	return core.Normalize(a.Snapshot.Layout, a.Metadata)
}
func (a *App) Import(layout []any) error {
	if _, err := core.Normalize(layout, a.Metadata); err != nil {
		return err
	}
	snapshot := device.Snapshot{Layout: layout, Identity: map[string]string{"mode": "local-simulation"}}
	if a.Options.Device != "" {
		snapshot.Identity = map[string]string{"udid": a.Options.Device, "ecid": strings.ToLower(a.Options.ECID)}
	} else {
		a.Device = device.NewLocal(snapshot)
		var err error
		snapshot, err = a.Device.Capture(context.Background())
		if err != nil {
			return err
		}
	}
	a.Snapshot = &snapshot
	a.NeedsCapture = a.Options.Device != ""
	a.ResumeReadOnly = false
	a.InterviewSkipped = false
	a.FormDrafts = map[string]string{}
	a.Interview = nil
	a.Preferences.Keep = nil
	a.Preferences.Dock = nil
	a.invalidate()
	_, err := a.save("capture", snapshot)
	return err
}
func (a *App) Capture(ctx context.Context) error {
	if a.Options.Demo {
		layout, metadata := Demo()
		a.Metadata = metadata
		if err := a.Import(layout); err != nil {
			return err
		}
		a.UI.Print("Synthetic demo reset. Start with context (menu 3); no file or phone is needed.")
		return a.Inventory()
	}
	if a.Options.Device != "" {
		a.UI.Print("Read-only capture: cfgutil layout and device identity, plus the installed app inventory. No runner or placement starts.")
		choice, err := a.UI.Ask("Read the configured phone now? [y/N] ")
		if err != nil {
			return err
		}
		if !yes(choice) {
			return nil
		}
		var snapshot device.Snapshot
		err = a.UI.Work(ctx, "Reading the phone layout and app inventory", func(work context.Context) error {
			var e error
			snapshot, e = a.Device.Capture(work)
			return e
		})
		observation := readiness.ObserveCapture(a.ReadinessChecker.Inspect(a.readinessConfig()), a.readinessConfig(), snapshot.Identity, err, time.Now())
		a.Readiness = &observation
		if err != nil {
			return err
		}
		sameSource := a.Snapshot != nil && reflect.DeepEqual(a.Snapshot.Identity, snapshot.Identity) && reflect.DeepEqual(a.Snapshot.Layout, snapshot.Layout) && reflect.DeepEqual(a.Snapshot.AppIDs, snapshot.AppIDs) && reflect.DeepEqual(a.Snapshot.StageIDs, snapshot.StageIDs)
		a.Snapshot = &snapshot
		a.NeedsCapture = false
		a.ResumeReadOnly = false
		if a.Metadata.Apps == nil {
			a.Metadata.Apps = map[string]core.AppMetadata{}
		}
		for key, label := range snapshot.Labels {
			if _, exists := a.Metadata.Apps[key]; !exists {
				a.Metadata.Apps[key] = core.AppMetadata{Label: label}
			}
		}
		if !sameSource {
			a.Interview = nil
			a.InterviewSkipped = false
			a.FormDrafts = map[string]string{}
			a.Preferences.Keep = nil
			a.Preferences.Dock = nil
			a.invalidate()
			a.UI.Print("The source layout changed: app selections, interview and proposal were cleared. Your context and general preferences remain.")
		} else {
			a.Reviewed = ""
			if a.Artifact != nil {
				review, e := organizer.NewReview(snapshot, *a.Artifact, a.Preferences)
				if e != nil {
					return e
				}
				a.Review = &review
			}
			a.UI.Print("The captured layout matches. Your form and proposal are retained; review approval was cleared.")
		}
		if _, err := a.save("capture", snapshot); err != nil {
			return err
		}
	} else {
		path, err := a.UI.Ask("Path to saved cfgutil layout JSON (Enter cancels): ")
		if err != nil {
			return err
		}
		if path == "" {
			return nil
		}
		var layout []any
		if err = ReadJSON(path, &layout); err != nil {
			return err
		}
		if err = a.Import(layout); err != nil {
			return err
		}
	}
	return a.Inventory()
}
func (a *App) Inventory() error {
	state, err := a.state()
	if err != nil {
		return err
	}
	var b strings.Builder
	fmt.Fprintln(&b, "CURRENT SAVED INVENTORY — optional labels: --metadata FILE")
	identity, _ := json.Marshal(a.Snapshot.Identity)
	fmt.Fprintln(&b, "Target: "+string(identity))
	fmt.Fprintln(&b, "cfgutil does not capture widget configurations or free icon positions.")
	for _, warning := range a.Snapshot.Warnings {
		fmt.Fprintln(&b, "Note: "+warning)
	}
	for _, item := range state.Items {
		fmt.Fprintf(&b, "%s  %s  [%s]  %s\n", item.ID, item.Label, item.SourceID, source(item))
	}
	_, err = a.UI.Show(b.String())
	return err
}
func (a *App) Context() error {
	a.UI.Print("Current context: " + a.Request)
	a.UI.InputKey = "context"
	defer func() { a.UI.InputKey = "" }()
	text, err := a.UI.Ask("Describe work, interests and the Home Screen you want (Enter keeps current): ")
	if err != nil {
		return err
	}
	if text != "" && text != a.Request {
		a.Request = text
		a.Interview = nil
		a.InterviewSkipped = false
		a.FormDrafts = map[string]string{}
		a.UI.Print("Context updated. Old interview answers and proposal were cleared; preferences remain.")
		a.invalidate()
		_, err = a.save("context", map[string]string{"request": text})
	}
	return err
}
func yes(value string) bool { return strings.EqualFold(value, "y") || strings.EqualFold(value, "yes") }
func (a *App) chooseIDs(prompt string, current []string) ([]string, error) {
	text, err := a.UI.Ask(prompt)
	if err != nil {
		return nil, err
	}
	if text == "" {
		return current, nil
	}
	if strings.EqualFold(text, "none") {
		return []string{}, nil
	}
	state, err := a.state()
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, item := range state.Items {
		known[item.ID] = true
	}
	ids := strings.Fields(strings.ReplaceAll(text, ",", " "))
	seen := map[string]bool{}
	for _, id := range ids {
		if !known[id] || seen[id] {
			return nil, errors.New("use unique item IDs from menu 2, separated by spaces; none clears the list")
		}
		seen[id] = true
	}
	return ids, nil
}
func (a *App) PreferencesMenu() error {
	if _, err := a.state(); err != nil {
		return err
	}
	p := a.Preferences
	a.UI.Print("Enter keeps the current value. Preserved/unknown geometry blocks automatic Apply.")
	text, err := a.UI.Ask(fmt.Sprintf("Home page budget, 1–15 [%d]: ", p.MaxPages))
	if err != nil {
		return err
	}
	if text != "" {
		n, err := strconv.Atoi(text)
		if err != nil || n < 1 || n > 15 {
			return errors.New("page budget must be 1–15")
		}
		p.MaxPages = n
	}
	p.Keep, err = a.chooseIDs("Must-keep IDs ["+strings.Join(p.Keep, ", ")+"; none clears]: ", p.Keep)
	if err != nil {
		return err
	}
	p.Dock, err = a.chooseIDs("Dock IDs (up to 4, in order; none empties; Enter keeps current): ", p.Dock)
	if err != nil {
		return err
	}
	if len(p.Dock) > 4 {
		return errors.New("Dock holds at most four apps")
	}
	text, err = a.UI.Ask("Alignment: preserve / standard / bottom [" + p.Alignment + "]: ")
	if err != nil {
		return err
	}
	if text != "" {
		if text != "preserve" && text != "standard" && text != "bottom" {
			return errors.New("choose preserve, standard or bottom")
		}
		p.Alignment = text
	}
	text, err = a.UI.Ask("Manual widget space request [" + p.WidgetSpace + "; none clears]: ")
	if err != nil {
		return err
	}
	if text != "" {
		p.WidgetSpace = text
		if strings.EqualFold(text, "none") {
			p.WidgetSpace = ""
		}
	}
	a.UI.Print("Standard means sequential icon order without empty-slot or widget geometry. A bulk write can remove EXISTING widgets and reflow spaces. Restore widgets manually afterward.")
	text, err = a.UI.Ask(fmt.Sprintf("Accept this widget/reflow limitation? [y/N; current %t]: ", p.AcceptReflow))
	if err != nil {
		return err
	}
	if text != "" {
		p.AcceptReflow = yes(text)
	}
	a.Preferences = p
	a.invalidate()
	_, err = a.save("preferences", p)
	return err
}
func Disclosure(provider, model string, state core.State) string {
	return fmt.Sprintf("External AI request: %s, model %s, through your installed signed-in CLI.\nData sent: %d app/widget placements, identifiers, labels, folder/page/Dock structure, supplied metadata, free-text context and interview answers.\nThis information leaves your computer for the AI service configured by that CLI. Each request needs separate consent. It produces questions or a draft, not phone changes.", provider, model, len(state.Items))
}
func (a *App) model() string {
	if a.Options.Model != "" {
		return a.Options.Model
	}
	return core.DefaultModel(a.Options.Provider)
}
func (a *App) consent(state core.State) (bool, error) {
	if a.RefusedAI {
		a.UI.Print("External AI sharing is disabled for this session. Use local interview/planning.")
		return false, nil
	}
	a.UI.Print(Disclosure(a.Options.Provider, a.model(), state))
	choice, err := a.UI.Ask("Send this information to the selected AI service? [y/N] ")
	if err != nil {
		return false, err
	}
	if !yes(choice) {
		a.RefusedAI = true
		a.UI.Print("AI sharing declined. Nothing sent. Local planning remains available.")
		return false, a.SaveSession()
	}
	return true, nil
}
func (a *App) mode(label string) (string, error) {
	if a.RefusedAI {
		return "local", nil
	}
	mode, err := a.UI.Ask(label + ": local or ai [local]: ")
	if err != nil {
		return "", err
	}
	if mode == "" {
		mode = "local"
	}
	if mode != "local" && mode != "ai" {
		return "", errors.New("choose local or ai")
	}
	return mode, nil
}
func (a *App) input(state core.State) core.Input {
	required := map[string]bool{}
	for _, id := range a.Preferences.Keep {
		required[id] = true
	}
	for _, id := range a.Preferences.Dock {
		required[id] = true
	}
	known := map[string]bool{}
	for _, id := range a.Snapshot.AppIDs {
		known[id] = true
	}
	for _, item := range state.Items {
		if item.Kind != "icon" || (item.SourceID == "/" || strings.Contains(item.SourceID, "://")) || (len(known) > 0 && !known[item.SourceID]) {
			required[item.ID] = true
		}
	}
	ids := []string{}
	for _, item := range state.Items {
		if required[item.ID] {
			ids = append(ids, item.ID)
		}
	}
	var questions []core.Question
	if a.Interview != nil {
		questions = a.Interview.Questions
	}
	return core.Input{Selection: "curate", MaxPages: a.Preferences.MaxPages, RequiredVisible: ids, Request: a.Request, CurrentState: state, Preferences: a.Preferences, InterviewAnswers: questions}
}
func (a *App) Questionnaire(ctx context.Context) error {
	state, err := a.state()
	if err != nil {
		return err
	}
	if a.Request == "" {
		return errors.New("add free-text context first (menu 3)")
	}
	if a.Interview != nil && a.Interview.Request == a.Request {
		return a.collectInterviewAnswers()
	}
	mode, err := a.mode("Interview")
	if err != nil {
		return err
	}
	questions := []core.Question{}
	if mode == "ai" {
		allowed, err := a.consent(state)
		if err != nil || !allowed {
			return err
		}
		var result core.Questionnaire
		err = a.UI.Work(ctx, "Preparing your interview", func(work context.Context) error {
			var e error
			result, e = a.AI.Interview(work, core.AIRequest{Provider: a.Options.Provider, Model: a.model(), Effort: "medium", Input: a.input(state), Consent: true}, 4)
			return e
		})
		if err != nil {
			return err
		}
		questions = result.Questions
		a.UI.Print(result.Summary)
	} else {
		questions = core.LocalQuestions(a.Request, state)
	}
	a.invalidate()
	for i := range questions {
		questions[i].Status = "pending"
		questions[i].Answer = nil
	}
	a.Interview = &Interview{Version: 1, Kind: "homescreen-interview", Request: a.Request, Questions: questions, Generation: mode}
	if _, err = a.save("interview", a.Interview); err != nil {
		return err
	}
	return a.collectInterviewAnswers()
}
func (a *App) collectInterviewAnswers() error {
	a.InterviewSkipped = false
	defer func() { a.UI.InputKey = "" }()
	a.UI.Print("Saved answers are retained. Enter skips an unanswered question; no answer is inferred.")
	for i := range a.Interview.Questions {
		q := &a.Interview.Questions[i]
		if q.Status != "pending" {
			continue
		}
		a.UI.InputKey = "interview:" + q.ID
		a.UI.Print(q.Question + "\nWhy: " + q.Why)
		for n, option := range q.Options {
			a.UI.Print(fmt.Sprintf("%d. %s", n+1, option))
		}
		for {
			answer, err := a.UI.Ask("Answer > ")
			if err != nil {
				return err
			}
			if n, e := strconv.Atoi(answer); e == nil && len(q.Options) > 0 {
				if n < 1 || n > len(q.Options) {
					a.UI.Print("Choose a listed number or type an answer.")
					continue
				}
				answer = q.Options[n-1]
			}
			if answer == "" {
				q.Status = "skipped"
				q.Answer = nil
			} else {
				q.Status = "answered"
				q.Answer = &answer
			}
			if _, err := a.save("interview", a.Interview); err != nil {
				return err
			}
			break
		}
	}
	return nil
}
func (a *App) Generate(ctx context.Context) error       { return a.generate(ctx, true) }
func (a *App) CreateProposal(ctx context.Context) error { return a.generate(ctx, false) }
func (a *App) generate(ctx context.Context, show bool) error {
	state, err := a.state()
	if err != nil {
		return err
	}
	if a.Request == "" {
		return errors.New("add context first (menu 3)")
	}
	if a.Interview != nil {
		for _, q := range a.Interview.Questions {
			if q.Status == "pending" {
				return errors.New("interview is incomplete; run it again or revise context, rather than infer answers")
			}
		}
	}
	mode, err := a.mode("Planner")
	if err != nil {
		return err
	}
	input := a.input(state)
	var plan core.Plan
	if mode == "ai" {
		allowed, err := a.consent(state)
		if err != nil || !allowed {
			return err
		}
		err = a.UI.Work(ctx, "Creating a proposal", func(work context.Context) error {
			var e error
			plan, e = a.AI.Plan(work, core.AIRequest{Provider: a.Options.Provider, Model: a.model(), Effort: "medium", Input: input, Consent: true})
			return e
		})
		if err != nil {
			return err
		}
	} else {
		prefs := a.Preferences
		prefs.Keep = input.RequiredVisible
		plan, err = core.LocalPlan(a.Request, state, prefs, input.InterviewAnswers)
		if err != nil {
			return err
		}
	}
	if err = core.ValidatePlan(plan, state, "curate", a.Preferences.MaxPages, input.RequiredVisible); err != nil {
		return err
	}
	if a.Preferences.Dock != nil && !sameIDs(plan.Dock, a.Preferences.Dock) {
		return errors.New("planner did not respect explicit Dock choices; draft rejected")
	}
	artifact := core.Artifact{Version: 1, Status: "draft", Provider: mode, Input: input, Plan: plan, Warnings: []string{"Relevance is inferred from supplied context, not app usage.", "Geometry is not captured: bulk writes can remove widgets and reflow existing spaces."}}
	if mode == "ai" {
		artifact.Provider = a.Options.Provider
		artifact.Model = a.model()
	}
	review, err := organizer.NewReview(*a.Snapshot, artifact, a.Preferences)
	if err != nil {
		return err
	}
	a.Artifact = &artifact
	a.Review = &review
	a.Reviewed = ""
	path, err := a.save("draft", artifact)
	if err != nil {
		return err
	}
	if _, err = a.save("review", review.Data()); err != nil {
		return err
	}
	a.UI.Print("Saved draft: " + path)
	if show {
		return a.View()
	}
	return nil
}
func sameIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i, v := range a {
		if v != b[i] {
			return false
		}
	}
	return true
}
func (a *App) View() error {
	if a.Review == nil {
		return errors.New("generate a proposal first (menu 6)")
	}
	data := a.Review.Data()
	text, err := Render(data.Artifact)
	if err != nil {
		return err
	}
	prefs, _ := json.MarshalIndent(data.Preferences, "", "  ")
	identity, _ := json.Marshal(data.Snapshot.Identity)
	text += "\nPREFERENCES\n" + string(prefs) + "\nTarget: " + string(identity) + "\nOther installed apps are staged temporarily, then returned to App Library. No app uninstallation.\n"
	problems := organizer.Blockers(*a.Review, a.Device.CleanupAvailable())
	text += "\nAPPLY BLOCKERS\n"
	if len(problems) == 0 {
		text += "(none detected)\n"
	} else {
		for _, problem := range problems {
			text += "- " + problem + "\n"
		}
	}
	text += "\nManual guide: preserve shortcuts, arrange bottom positions/empty spaces by hand, then add widgets. Exported layouts are not exact widget/shortcut backups.\nReview fingerprint: " + a.Review.Digest()
	a.Reviewed = ""
	complete, err := a.UI.Show(text)
	if err != nil {
		return err
	}
	if complete {
		a.Reviewed = a.Review.Digest()
		a.UI.Print("Review complete. Apply is a separate explicit action (menu 8).")
	}
	return nil
}
func (a *App) Apply(ctx context.Context) error {
	if problem := a.sessionApplyProblem(); problem != "" {
		return errors.New(problem)
	}
	if a.Review == nil || a.Reviewed != a.Review.Digest() {
		return errors.New("read the entire current proposal first (menu 7); edits require a new review")
	}
	if problems := organizer.Blockers(*a.Review, a.Device.CleanupAvailable()); len(problems) > 0 {
		return errors.New(strings.Join(problems, "\n"))
	}
	heading := "REAL DEVICE APPLY"
	if a.Device.Simulated() {
		heading = "SIMULATION ONLY — no phone commands"
	}
	a.UI.Print(heading + "\nThis exact plan replaces icon order, may remove EXISTING widgets and reflow empty space. Trailing pages will be removed from Home Screen, leaving ordinary apps installed. No exact rollback is promised. Keep the phone unlocked and do not interact during execution.")
	identity, _ := json.Marshal(a.Snapshot.Identity)
	a.UI.Print("Target: " + string(identity))
	approval, err := a.UI.Ask("Type " + a.Review.Challenge() + " to proceed; anything else cancels: ")
	if err != nil {
		return err
	}
	a.Reviewed = ""
	if approval != a.Review.Challenge() {
		a.UI.Print("Cancelled. No device changes attempted.")
		return nil
	}
	a.serial++
	directory := filepath.Join(a.Directory, fmt.Sprintf("%03d-apply", a.serial))
	var result []any
	err = a.UI.Work(ctx, "Applying and verifying the reviewed layout", func(work context.Context) error {
		var e error
		result, e = organizer.Execute(work, *a.Review, approval, a.Device, directory)
		return e
	})
	if err != nil {
		a.Snapshot = nil
		a.invalidate()
		return err
	}
	a.Snapshot.Layout = result
	a.invalidate()
	a.Interview = nil
	a.Preferences.Keep = nil
	a.Preferences.Dock = nil
	label := ""
	if a.Device.Simulated() {
		label = "SIMULATED "
	}
	a.UI.Print(label + "VERIFIED: exact icon layout matches the reviewed plan. Widget configurations and free-position geometry were not verified. Evidence: " + directory)
	return a.SaveSession()
}
func (a *App) Run(ctx context.Context) error {
	mode := "Phone reads, AI requests and Apply require explicit menu actions."
	if a.Options.Demo {
		mode = "LOCAL DEMO: synthetic data only; no phone or AI commands."
	} else if a.Options.Device == "" {
		mode = "LOCAL SIMULATION: no phone is configured. AI requests require separate consent."
	}
	start := "Start with 1 (import), 3 (context), 4 (preferences), then 5/6."
	if a.Snapshot != nil {
		start = "Layout loaded. Start with 3 (context), 4 (preferences), then 5/6. In demo, menu 1 resets the synthetic sample."
	}
	status := a.PhoneSummary(time.Now())
	a.UI.Print("Phone: " + status.Connection + "\nEligibility: " + status.Eligibility + " — " + status.EligibilityReason)
	a.UI.Print("HOMEWARD — interactive terminal organizer\n" + mode + "\nPrivate session evidence: " + a.Directory + "\n" + start + " /quit or Ctrl-D exits.")
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		a.UI.Print("\n1 Import/read layout   2 Inspect inventory   3 Context\n4 Preferences / must-keep / Dock   5 Interview\n6 Generate proposal   7 Review proposal   8 Apply   0 Exit")
		choice, err := a.UI.Ask("Homeward > ")
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if choice == "0" {
			break
		}
		switch choice {
		case "1":
			err = a.Capture(ctx)
		case "2":
			err = a.Inventory()
		case "3":
			err = a.Context()
		case "4":
			err = a.PreferencesMenu()
		case "5":
			err = a.Questionnaire(ctx)
		case "6":
			err = a.Generate(ctx)
		case "7":
			err = a.View()
		case "8":
			err = a.Apply(ctx)
		default:
			a.UI.Print("Choose a menu number, or /quit.")
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			a.UI.Print("Stopped: " + err.Error())
		}
	}
	a.UI.Print("Saved local drafts, answers and evidence: " + a.Directory)
	return nil
}
