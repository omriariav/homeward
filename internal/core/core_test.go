package core

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func sample(t *testing.T) State {
	t.Helper()
	layout, err := DecodeLayout([]byte(`[["phone"],[["Work",["mail","calendar"],["tasks"]],["Interests","books","music"],"social","games","shop"]]`))
	if err != nil {
		t.Fatal(err)
	}
	state, err := Normalize(layout, Metadata{Apps: map[string]AppMetadata{"mail": {Label: "Mail"}, "books": {Label: "Books"}}})
	if err != nil {
		t.Fatal(err)
	}
	return state
}
func local(t *testing.T) (State, Plan) {
	t.Helper()
	state := sample(t)
	plan, err := LocalPlan("Work and books", state, Preferences{MaxPages: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return state, plan
}
func copyPlan(t *testing.T, plan Plan) Plan {
	t.Helper()
	data, _ := json.Marshal(plan)
	var result Plan
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	return result
}
func aiRequest(t *testing.T) (AIRequest, Plan) {
	t.Helper()
	state, plan := local(t)
	return AIRequest{Provider: "claude", Consent: true, Input: Input{Selection: "curate", MaxPages: 1, Request: "Work and books", CurrentState: state, Preferences: Preferences{MaxPages: 1}}}, plan
}
func envelope(t *testing.T, plan Plan) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{"subtype": "success", "structured_output": plan})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestNormalizePreservesFolderPagesDuplicateOccurrencesAndPlaceholders(t *testing.T) {
	layout, err := DecodeLayout([]byte(`[["phone"],[["Work",["mail","/"],["mail","/"]]] ]`))
	if err != nil {
		t.Fatal(err)
	}
	state, err := Normalize(layout, Metadata{})
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Items) != 5 {
		t.Fatalf("lost occurrences: %#v", state.Items)
	}
	if state.Items[1].SourceID != state.Items[3].SourceID || state.Items[1].ID == state.Items[3].ID {
		t.Fatal("duplicate app occurrences were merged")
	}
	if state.Items[2].SourceID != "/" || state.Items[4].SourceID != "/" || state.Items[2].ID == state.Items[4].ID {
		t.Fatal("shortcut placeholders were merged")
	}
	if !reflect.DeepEqual(state.Items[4].SourcePath, []int{1, 0, 2, 1}) || !reflect.DeepEqual(state.Items[4].SourceFolders, []string{"Work"}) {
		t.Fatalf("folder context lost: %#v", state.Items[4])
	}
	for _, bad := range []string{`[]`, `[[],42]`, `[[],[false]]`, `[[],[[42]]]`, `[[],[]]`, `[["a"],["b"]] {}`} {
		if _, err := DecodeLayout([]byte(bad)); err == nil {
			t.Fatalf("accepted malformed layout %s", bad)
		}
	}
}

func TestLocalShortlistHonorsContextKeepsAndBudget(t *testing.T) {
	state, plan := local(t)
	if len(plan.Hidden) == 0 {
		t.Fatal("local curate selected every app")
	}
	selected := map[string]bool{}
	for _, page := range plan.Pages {
		for _, id := range page.Items {
			selected[id] = true
		}
	}
	for _, item := range state.Items {
		if item.SourceID == "books" && !selected[item.ID] {
			t.Fatal("context interest not represented")
		}
	}
	keep := state.Items[len(state.Items)-1].ID
	plan, err := LocalPlan("Work", state, Preferences{MaxPages: 1, Keep: []string{keep}, Dock: []string{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Dock) != 0 {
		t.Fatal("explicit empty Dock ignored")
	}
	for _, entry := range plan.Hidden {
		if entry.Item == keep {
			t.Fatal("must-keep excluded")
		}
	}
	if err := ValidatePlan(plan, state, "curate", 1, []string{keep}); err != nil {
		t.Fatal(err)
	}
	manyLayout := []any{[]any{}, []any{}}
	row := []any{}
	for n := 0; n < 30; n++ {
		row = append(row, string(rune('a'+n)))
	}
	manyLayout[1] = row
	many, err := Normalize(manyLayout, Metadata{})
	if err != nil {
		t.Fatal(err)
	}
	all := []string{}
	for _, item := range many.Items {
		all = append(all, item.ID)
	}
	if _, err := LocalPlan("all", many, Preferences{MaxPages: 1, Keep: all, Dock: []string{}}, nil); err == nil {
		t.Fatal("page budget silently exceeded")
	}
}

func TestValidationRejectsDuplicateMissingUnknownAndHiddenMustKeep(t *testing.T) {
	state, valid := local(t)
	cases := map[string]func(*Plan){
		"duplicate":    func(p *Plan) { p.Pages[0].Items = append(p.Pages[0].Items, p.Dock[0]) },
		"unknown":      func(p *Plan) { p.Pages[0].Items[0] = "missing" },
		"missing":      func(p *Plan) { p.Hidden = p.Hidden[1:] },
		"blank reason": func(p *Plan) { p.Hidden[0].Reason = "  " },
		"empty page":   func(p *Plan) { p.Pages = append(p.Pages, Page{Items: []string{}}) },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			plan := copyPlan(t, valid)
			change(&plan)
			if err := ValidatePlan(plan, state, "curate", 2, nil); err == nil {
				t.Fatal("invalid proposal accepted")
			}
		})
	}
	if err := ValidatePlan(valid, state, "curate", 1, []string{valid.Hidden[0].Item}); err == nil {
		t.Fatal("required item hidden")
	}
	if err := ValidatePlan(valid, state, "preserve", 1, nil); err == nil {
		t.Fatal("preserve policy silently curated")
	}
	allowed := []string{}
	for _, entry := range valid.Hidden {
		allowed = append(allowed, entry.Item)
	}
	if err := ValidatePlanWithAllowed(valid, state, "preserve", 1, nil, allowed); err != nil {
		t.Fatal(err)
	}
}

func TestQuestionAnswersAreNeverInvented(t *testing.T) {
	state := sample(t)
	questions := LocalQuestions("focus on work", state)
	if len(questions) < 2 {
		t.Fatal("context not reflected in local interview")
	}
	for _, q := range questions {
		if q.Answer != nil || q.Status != "pending" {
			t.Fatal("invented answer")
		}
	}
	if _, err := LocalPlan("work", state, Preferences{MaxPages: 1}, questions); err == nil {
		t.Fatal("pending answers accepted")
	}
	for n := range questions {
		questions[n].Status = "skipped"
	}
	if _, err := LocalPlan("work", state, Preferences{MaxPages: 1}, questions); err != nil {
		t.Fatal(err)
	}
	answer := "invented"
	questions[0].Answer = &answer
	if err := ValidateAnswers(questions); err == nil {
		t.Fatal("skipped answer inferred")
	}
}

func TestWidgetMetadataPreventsExecutableExport(t *testing.T) {
	layout, _ := DecodeLayout([]byte(`[["phone"],["mail"]]`))
	state, err := Normalize(layout, Metadata{Widgets: []WidgetMetadata{{Label: "Calendar widget", Size: "large"}}})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := LocalPlan("calendar", state, Preferences{MaxPages: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ExportLayout(plan, state); err == nil {
		t.Fatal("widget geometry silently exported as icon")
	}
}

func TestPlanJSONRequiresExactSchema(t *testing.T) {
	_, plan := local(t)
	data, _ := json.Marshal(plan)
	if _, err := DecodePlan(data); err != nil {
		t.Fatal(err)
	}
	var values map[string]any
	if err := json.Unmarshal(data, &values); err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []func(map[string]any){func(v map[string]any) { delete(v, "summary") }, func(v map[string]any) { v["unsafe_action"] = "apply" }, func(v map[string]any) { v["dock"] = nil }, func(v map[string]any) { v["summary"] = 42 }} {
		var changed map[string]any
		encoded, _ := json.Marshal(values)
		_ = json.Unmarshal(encoded, &changed)
		mutation(changed)
		bad, _ := json.Marshal(changed)
		if _, err := DecodePlan(bad); err == nil {
			t.Fatalf("invalid shape accepted: %s", bad)
		}
	}
}

func TestAIConsentRequiredForEveryRequestAndRefusalWins(t *testing.T) {
	request, plan := aiRequest(t)
	calls := 0
	client := AIClient{Runner: func(context.Context, []string, string, string) (string, error) {
		calls++
		return envelope(t, plan), nil
	}}
	request.Consent = false
	if _, err := client.Plan(context.Background(), request); err == nil || calls != 0 {
		t.Fatal("unconsented request sent")
	}
	request.Consent = true
	request.Refused = true
	if _, err := client.Plan(context.Background(), request); err == nil || calls != 0 {
		t.Fatal("refusal ignored")
	}
	request.Refused = false
	if _, err := client.Plan(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	request.Consent = false
	if _, err := client.Interview(context.Background(), request, 4); err == nil || calls != 1 {
		t.Fatal("planning consent reused for interview")
	}
}

func TestProviderIsolationAndArgvTransport(t *testing.T) {
	for _, provider := range []string{"claude", "codex"} {
		t.Run(provider, func(t *testing.T) {
			request, plan := aiRequest(t)
			request.Provider = provider
			request.Model = "model with spaces; $(not-a-command)"
			request.Input.Request = "work\n`not-a-command` $(echo private)"
			directory := ""
			client := AIClient{Runner: func(ctx context.Context, command []string, stdin, cwd string) (string, error) {
				directory = cwd
				if filepath.Base(cwd) == "homeward" {
					t.Fatal("provider ran inside repository")
				}
				info, err := os.Stat(cwd)
				if err != nil || info.Mode().Perm()&0077 != 0 {
					t.Fatal("provider directory not private")
				}
				if !strings.Contains(stdin, "INPUT_DATA:") || !strings.Contains(stdin, "$(echo private)") {
					t.Fatal("context did not travel through stdin")
				}
				foundModel := false
				for n, arg := range command {
					if arg == "--model" && n+1 < len(command) {
						foundModel = command[n+1] == request.Model
					}
				}
				if !foundModel {
					t.Fatal("model was interpolated/split instead of argv")
				}
				if _, err := os.Stat(filepath.Join(cwd, "schema.json")); err != nil {
					t.Fatal("schema not isolated")
				}
				if provider == "codex" {
					data, _ := json.Marshal(plan)
					if err := os.WriteFile(filepath.Join(cwd, "answer.json"), data, 0600); err != nil {
						t.Fatal(err)
					}
					return "CLI informational output", nil
				}
				return envelope(t, plan), nil
			}}
			if _, err := client.Plan(context.Background(), request); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(directory); !os.IsNotExist(err) {
				t.Fatal("provider temp files leaked")
			}
		})
	}
}

func TestAIOutputSemanticChecksAndNoRetry(t *testing.T) {
	request, plan := aiRequest(t)
	request.Input.RequiredVisible = []string{plan.Hidden[0].Item}
	calls := 0
	client := AIClient{Runner: func(context.Context, []string, string, string) (string, error) {
		calls++
		return envelope(t, plan), nil
	}}
	if _, err := client.Plan(context.Background(), request); err == nil || calls != 1 {
		t.Fatal("unsafe output accepted or retried")
	}
	request.Input.RequiredVisible = nil
	request.Input.Preferences.Dock = []string{}
	if _, err := client.Plan(context.Background(), request); err == nil {
		t.Fatal("AI ignored explicit Dock choice")
	}
	request.Input.Preferences.Dock = nil
	client.Runner = func(context.Context, []string, string, string) (string, error) {
		return `{"subtype":"error","structured_output":{}}`, nil
	}
	if _, err := client.Plan(context.Background(), request); err == nil {
		t.Fatal("failed Claude envelope accepted")
	}
	client.Runner = func(context.Context, []string, string, string) (string, error) {
		return "", errors.New("provider unavailable")
	}
	if _, err := client.Plan(context.Background(), request); err == nil {
		t.Fatal("provider failure ignored")
	}
}

func TestInterviewSchemaRejectsFabricatedAnswer(t *testing.T) {
	request, _ := aiRequest(t)
	client := AIClient{Runner: func(context.Context, []string, string, string) (string, error) {
		return `{"subtype":"success","structured_output":{"summary":"Question","questions":[{"id":"goal","question":"Which apps?","why":"Priorities","options":[],"answer":"Work"}]}}`, nil
	}}
	if _, err := client.Interview(context.Background(), request, 4); err == nil {
		t.Fatal("model supplied user answer")
	}
	client.Runner = func(context.Context, []string, string, string) (string, error) {
		return `{"subtype":"success","structured_output":{"summary":"Question","questions":[{"id":"goal","question":"Which apps?","why":"Priorities","options":[]}]}}`, nil
	}
	result, err := client.Interview(context.Background(), request, 4)
	if err != nil {
		t.Fatal(err)
	}
	if result.Questions[0].Status != "pending" || result.Questions[0].Answer != nil {
		t.Fatal("generated question is already answered")
	}
}

func TestAIHonorsCancellation(t *testing.T) {
	request, _ := aiRequest(t)
	request.Timeout = time.Millisecond
	client := AIClient{Runner: func(ctx context.Context, _ []string, _, _ string) (string, error) { <-ctx.Done(); return "", ctx.Err() }}
	if _, err := client.Plan(context.Background(), request); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout not propagated: %v", err)
	}
}

func TestCancelledAIRequestDoesNotDispatchRunner(t *testing.T) {
	request, _ := aiRequest(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	client := AIClient{Runner: func(context.Context, []string, string, string) (string, error) {
		calls++
		return "", nil
	}}
	if _, err := client.Plan(ctx, request); !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatalf("cancelled request dispatched: calls=%d error=%v", calls, err)
	}
}
