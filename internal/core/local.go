package core

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

func LocalQuestions(request string, state State) []Question {
	questions := []Question{{ID: "priorities", Question: "Which 2–4 activities deserve the fastest access?", Why: "These words and app names guide the local shortlist.", Options: []string{}, Status: "pending"}}
	names := map[string]bool{}
	for _, item := range state.Items {
		for _, folder := range item.SourceFolders {
			names[folder] = true
		}
	}
	folders := []string{}
	for folder := range names {
		folders = append(folders, folder)
	}
	sort.Strings(folders)
	if len(folders) > 0 {
		questions = append(questions, Question{ID: "interests", Question: "Which existing interests should remain represented? " + strings.Join(folders, ", "), Why: "Keeping an interest does not require keeping every app in its folder.", Options: []string{}, Status: "pending"})
	}
	lower := strings.ToLower(request)
	for _, word := range []string{"focus", "work", "distract", "minimal"} {
		if strings.Contains(lower, word) {
			questions = append(questions, Question{ID: "distractions", Question: "Which apps should be less prominent? Name them, or skip.", Why: "The local planner deprioritizes matching names; must-keep choices still win.", Options: []string{}, Status: "pending"})
			break
		}
	}
	return questions
}

var questionID = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

func ValidateQuestionnaire(questionnaire Questionnaire, maximum int) error {
	if maximum < 0 || maximum > 6 || len(questionnaire.Questions) > maximum {
		return fmt.Errorf("interview exceeds its question limit")
	}
	seen := map[string]bool{}
	for _, q := range questionnaire.Questions {
		if !questionID.MatchString(q.ID) || seen[q.ID] {
			return fmt.Errorf("question IDs must be unique snake_case identifiers")
		}
		seen[q.ID] = true
		if strings.TrimSpace(q.Question) == "" || strings.TrimSpace(q.Why) == "" {
			return fmt.Errorf("questions need text and a reason")
		}
		if len(q.Options) == 1 || len(q.Options) > 4 {
			return fmt.Errorf("questions support zero or 2–4 options")
		}
		options := map[string]bool{}
		for _, option := range q.Options {
			if strings.TrimSpace(option) == "" || options[option] {
				return fmt.Errorf("question options must be distinct and nonempty")
			}
			options[option] = true
		}
	}
	return nil
}

func ValidateAnswers(questions []Question) error {
	if err := ValidateQuestionnaire(Questionnaire{Questions: questions}, 6); err != nil {
		return err
	}
	for _, q := range questions {
		switch q.Status {
		case "answered":
			if q.Answer == nil || strings.TrimSpace(*q.Answer) == "" {
				return fmt.Errorf("answered question %s needs a nonempty answer", q.ID)
			}
		case "skipped":
			if q.Answer != nil {
				return fmt.Errorf("skipped question %s cannot have an inferred answer", q.ID)
			}
		default:
			return fmt.Errorf("question %s is unanswered; answer or explicitly skip it", q.ID)
		}
	}
	return nil
}

func words(text string) map[string]bool {
	result := map[string]bool{}
	ignored := map[string]bool{"the": true, "and": true, "for": true, "with": true, "want": true, "keep": true, "apps": true, "app": true, "screen": true, "home": true, "more": true, "less": true, "that": true, "this": true, "from": true, "have": true, "only": true, "first": true, "without": true, "folders": true}
	for _, word := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) }) {
		if len([]rune(word)) > 2 && !ignored[word] {
			result[word] = true
		}
	}
	return result
}

// LocalPlan is a transparent keyword shortlist, not an inferred usage model.
// It prioritizes explicit choices, then context matches, then original order.
func LocalPlan(request string, state State, prefs Preferences, questions []Question) (Plan, error) {
	if strings.TrimSpace(request) == "" {
		return Plan{}, fmt.Errorf("free-text context is required")
	}
	if err := ValidateAnswers(questions); err != nil {
		return Plan{}, err
	}
	if prefs.MaxPages < 1 || prefs.MaxPages > 15 {
		return Plan{}, fmt.Errorf("page budget must be 1–15")
	}
	known := map[string]Item{}
	for _, item := range state.Items {
		known[item.ID] = item
	}
	required := map[string]bool{}
	for _, id := range prefs.Keep {
		if _, ok := known[id]; !ok {
			return Plan{}, fmt.Errorf("unknown must-keep ID %s", id)
		}
		required[id] = true
	}
	for _, item := range state.Items {
		if item.Kind == "widget" || item.SourceID == "/" || strings.HasPrefix(item.SourceID, "https://") || strings.HasPrefix(item.SourceID, "http://") {
			required[item.ID] = true
		}
	}
	dock := append([]string{}, prefs.Dock...)
	if prefs.Dock == nil {
		for _, item := range state.Items {
			if item.Kind == "icon" && item.SourcePage == 0 && len(dock) < 4 {
				dock = append(dock, item.ID)
			}
		}
	}
	if len(dock) > 4 {
		return Plan{}, fmt.Errorf("Dock holds at most four icons")
	}
	dockSet := map[string]bool{}
	for _, id := range dock {
		item, ok := known[id]
		if !ok || item.Kind != "icon" || dockSet[id] {
			return Plan{}, fmt.Errorf("Dock needs distinct known icon IDs")
		}
		dockSet[id] = true
		required[id] = true
	}
	positive, negative := request, ""
	for _, q := range questions {
		if q.Status == "answered" {
			if q.ID == "distractions" {
				negative += " " + *q.Answer
			} else {
				positive += " " + *q.Answer
			}
		}
	}
	positiveWords, negativeWords := words(positive), words(negative)
	score := func(item Item) int {
		haystack := strings.ToLower(item.Label + " " + item.SourceID + " " + strings.Join(item.SourceFolders, " "))
		value := 0
		for word := range positiveWords {
			if strings.Contains(haystack, word) {
				value++
			}
		}
		for word := range negativeWords {
			if strings.Contains(haystack, word) {
				value -= 10
			}
		}
		return value
	}
	candidates := []Item{}
	requiredPages, positiveCount := 0, 0
	for _, item := range state.Items {
		if dockSet[item.ID] {
			continue
		}
		candidates = append(candidates, item)
		if required[item.ID] {
			requiredPages++
		}
		if score(item) > 0 {
			positiveCount++
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if required[a.ID] != required[b.ID] {
			return required[a.ID]
		}
		return score(a) > score(b)
	})
	target := positiveCount
	if target == 0 {
		target = 6
	}
	if target > 12 {
		target = 12
	}
	if target < requiredPages {
		target = requiredPages
	}
	capacity := prefs.MaxPages * 24
	if requiredPages > capacity {
		return Plan{}, fmt.Errorf("must-keep choices exceed the page budget")
	}
	if target > capacity {
		target = capacity
	}
	if target > len(candidates) {
		target = len(candidates)
	}
	if target == 0 {
		return Plan{}, fmt.Errorf("at least one page icon is needed; move a Dock app onto a page")
	}
	chosen := candidates[:target]
	plan := Plan{Summary: "Local provisional shortlist; review every choice. No AI request was made.", Dock: dock, Pages: []Page{}, Hidden: []Exclusion{},
		Assumptions: []string{"No usage statistics are available. Existing Dock choices are retained unless edited.", "Local matching uses supplied words, labels and folders; it cannot understand every interest."}, Questions: []string{}}
	selected := map[string]bool{}
	for _, id := range dock {
		selected[id] = true
	}
	for start := 0; start < len(chosen); start += 24 {
		end := start + 24
		if end > len(chosen) {
			end = len(chosen)
		}
		page := Page{Title: "Priorities", Reason: "Must-keep choices first, then matches to supplied context and folder labels.", Items: []string{}}
		if start > 0 {
			page.Title = "More priorities"
		}
		for _, item := range chosen[start:end] {
			page.Items = append(page.Items, item.ID)
			selected[item.ID] = true
		}
		plan.Pages = append(plan.Pages, page)
	}
	for _, item := range state.Items {
		if selected[item.ID] {
			continue
		}
		reason := "Lower match to the supplied priorities than the selected shortlist; usage is unknown."
		if score(item) < 0 {
			reason = "Matches a stated distraction; keep accessible in App Library."
		}
		plan.Hidden = append(plan.Hidden, Exclusion{Item: item.ID, Reason: reason})
	}
	if positiveCount == 0 {
		plan.Questions = append(plan.Questions, "No clear label match: the shortlist uses original icon order. Edit must-keep/Dock choices or add specific app names.")
	}
	ids := []string{}
	for id := range required {
		ids = append(ids, id)
	}
	if err := ValidatePlan(plan, state, "curate", prefs.MaxPages, ids); err != nil {
		return Plan{}, err
	}
	return plan, nil
}
