// Package core contains local planning and opt-in external AI requests. It never
// reads a phone or applies layouts.
package core

import (
	"encoding/json"
	"fmt"
	"strings"
)

type AppMetadata struct {
	Label string `json:"label,omitempty"`
	Color string `json:"color,omitempty"`
}
type WidgetMetadata struct {
	Label         string `json:"label"`
	Size          any    `json:"size,omitempty"`
	Configuration any    `json:"configuration,omitempty"`
	SourcePage    *int   `json:"source_page,omitempty"`
}
type Metadata struct {
	Apps         map[string]AppMetadata `json:"apps,omitempty"`
	Widgets      []WidgetMetadata       `json:"widgets,omitempty"`
	Observations []string               `json:"observations,omitempty"`
}
type Item struct {
	ID            string   `json:"id"`
	SourceID      string   `json:"source_id,omitempty"`
	Kind          string   `json:"kind"`
	Label         string   `json:"label"`
	Color         string   `json:"color,omitempty"`
	SourcePage    int      `json:"source_page"`
	SourcePath    []int    `json:"source_path"`
	SourceFolders []string `json:"source_folders"`
	WidgetSize    any      `json:"size,omitempty"`
	Configuration any      `json:"configuration,omitempty"`
}
type State struct {
	Items                []Item   `json:"items"`
	Warnings             []string `json:"warnings"`
	Observations         []string `json:"observations"`
	SourcePageConvention string   `json:"source_page_convention"`
}
type Preferences struct {
	MaxPages     int      `json:"max_pages"`
	Keep         []string `json:"keep"`
	Dock         []string `json:"dock"` // nil retains current Dock; [] explicitly empties it.
	Alignment    string   `json:"alignment"`
	WidgetSpace  string   `json:"widget_space"`
	AcceptReflow bool     `json:"accept_reflow"`
}
type Page struct {
	Title  string   `json:"title"`
	Reason string   `json:"reason"`
	Items  []string `json:"items"`
}
type Exclusion struct {
	Item   string `json:"item"`
	Reason string `json:"reason"`
}
type Plan struct {
	Summary     string      `json:"summary"`
	Dock        []string    `json:"dock"`
	Pages       []Page      `json:"pages"`
	Hidden      []Exclusion `json:"hidden"`
	Assumptions []string    `json:"assumptions"`
	Questions   []string    `json:"questions"`
}
type Question struct {
	ID       string   `json:"id"`
	Question string   `json:"question"`
	Why      string   `json:"why"`
	Options  []string `json:"options"`
	Status   string   `json:"status,omitempty"`
	Answer   *string  `json:"answer,omitempty"`
}
type Questionnaire struct {
	Summary   string     `json:"summary"`
	Questions []Question `json:"questions"`
}
type Input struct {
	Selection        string      `json:"selection"`
	MaxPages         int         `json:"max_pages"`
	RequiredVisible  []string    `json:"required_visible"`
	AllowedHidden    []string    `json:"allowed_hidden"`
	Request          string      `json:"request"`
	CurrentState     State       `json:"current_state"`
	Preferences      Preferences `json:"preferences"`
	InterviewAnswers []Question  `json:"interview_answers"`
}
type Artifact struct {
	Version  int      `json:"version"`
	Status   string   `json:"status"`
	Provider string   `json:"provider"`
	Model    string   `json:"model"`
	Input    Input    `json:"input"`
	Plan     Plan     `json:"plan"`
	Warnings []string `json:"warnings"`
}

// Normalize retains each occurrence separately, including identical identifiers
// and '/' placeholders. These are never treated as interchangeable shortcuts.
func Normalize(layout []any, metadata Metadata) (State, error) {
	state := State{Items: []Item{}, Warnings: []string{"cfgutil does not capture widget configurations or free icon positions; supplied metadata may be incomplete."},
		Observations: append([]string{}, metadata.Observations...), SourcePageConvention: "0 = Dock; 1 = first Home Screen page; 2 = second Home Screen page; etc."}
	if len(layout) < 2 {
		return State{}, fmt.Errorf("layout must contain a Dock array followed by at least one page array")
	}
	var walk func(any, int, []int, []string) error
	walk = func(node any, page int, path []int, folders []string) error {
		switch value := node.(type) {
		case string:
			if value == "" {
				state.Warnings = append(state.Warnings, fmt.Sprintf("Empty cfgutil identifier at %v is not a modeled placement.", path))
				return nil
			}
			extra := metadata.Apps[value]
			label := extra.Label
			if label == "" {
				label = value
			}
			state.Items = append(state.Items, Item{ID: fmt.Sprintf("item-%04d", len(state.Items)+1), SourceID: value, Kind: "icon", Label: label, Color: extra.Color,
				SourcePage: page, SourcePath: append([]int{}, path...), SourceFolders: append([]string{}, folders...)})
		case []any:
			if len(value) == 0 {
				return nil
			}
			name, ok := value[0].(string)
			if !ok {
				return fmt.Errorf("invalid folder at %v", path)
			}
			context := append(append([]string{}, folders...), name)
			for index, child := range value[1:] {
				next := append(append([]int{}, path...), index+1)
				// cfgutil represents folder pages as nested arrays after the folder name.
				if contents, ok := child.([]any); ok {
					for slot, nested := range contents {
						if err := walk(nested, page, append(append([]int{}, next...), slot), context); err != nil {
							return err
						}
					}
				} else if err := walk(child, page, next, context); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("unsupported layout value at %v", path)
		}
		return nil
	}
	for page, node := range layout {
		contents, ok := node.([]any)
		if !ok {
			return State{}, fmt.Errorf("layout page %d must be an array", page)
		}
		for slot, item := range contents {
			if err := walk(item, page, []int{page, slot}, []string{}); err != nil {
				return State{}, err
			}
		}
	}
	for _, widget := range metadata.Widgets {
		if strings.TrimSpace(widget.Label) == "" {
			return State{}, fmt.Errorf("each widget metadata entry needs a label")
		}
		page := -1
		if widget.SourcePage != nil {
			page = *widget.SourcePage
		}
		state.Items = append(state.Items, Item{ID: fmt.Sprintf("item-%04d", len(state.Items)+1), Kind: "widget", Label: widget.Label, SourcePage: page,
			SourcePath: []int{}, SourceFolders: []string{}, WidgetSize: widget.Size, Configuration: widget.Configuration})
	}
	if len(state.Items) == 0 {
		return State{}, fmt.Errorf("no icons or widgets found")
	}
	return state, nil
}

// DecodeLayout accepts exactly one JSON value, then validates its shape.
func DecodeLayout(data []byte) ([]any, error) {
	var layout []any
	if err := json.Unmarshal(data, &layout); err != nil {
		return nil, fmt.Errorf("decode layout: %w", err)
	}
	if _, err := Normalize(layout, Metadata{}); err != nil {
		return nil, err
	}
	return layout, nil
}

func ValidatePlan(plan Plan, state State, selection string, maxPages int, required []string) error {
	return ValidatePlanWithAllowed(plan, state, selection, maxPages, required, nil)
}

func ValidatePlanWithAllowed(plan Plan, state State, selection string, maxPages int, required, allowedHidden []string) error {
	if selection != "preserve" && selection != "curate" {
		return fmt.Errorf("unknown selection policy %q", selection)
	}
	if maxPages < 0 || maxPages > 15 {
		return fmt.Errorf("page budget must be 1–15, or zero for unspecified")
	}
	known := map[string]Item{}
	for _, item := range state.Items {
		if item.ID == "" || (item.Kind != "icon" && item.Kind != "widget") {
			return fmt.Errorf("invalid inventory item")
		}
		if _, exists := known[item.ID]; exists {
			return fmt.Errorf("duplicate inventory item ID %s", item.ID)
		}
		known[item.ID] = item
	}
	if len(known) == 0 {
		return fmt.Errorf("inventory is empty")
	}
	must := map[string]bool{}
	allowed := map[string]bool{}
	for _, id := range required {
		if _, ok := known[id]; !ok {
			return fmt.Errorf("unknown must-keep ID %s", id)
		}
		must[id] = true
	}
	for _, id := range allowedHidden {
		if _, ok := known[id]; !ok {
			return fmt.Errorf("unknown allowed-hidden ID %s", id)
		}
		allowed[id] = true
	}
	seen := map[string]bool{}
	account := func(id string) error {
		if _, ok := known[id]; !ok {
			return fmt.Errorf("unknown plan item %s", id)
		}
		if seen[id] {
			return fmt.Errorf("plan repeats item %s", id)
		}
		seen[id] = true
		return nil
	}
	if len(plan.Dock) > 4 {
		return fmt.Errorf("Dock holds at most four icons")
	}
	for _, id := range plan.Dock {
		if err := account(id); err != nil {
			return err
		}
		if known[id].Kind == "widget" {
			return fmt.Errorf("Dock cannot contain widgets")
		}
	}
	if len(plan.Pages) == 0 || (maxPages > 0 && len(plan.Pages) > maxPages) {
		return fmt.Errorf("plan needs pages within the requested page budget")
	}
	for _, page := range plan.Pages {
		if len(page.Items) < 1 || len(page.Items) > 24 {
			return fmt.Errorf("each page must contain 1–24 items")
		}
		for _, id := range page.Items {
			if err := account(id); err != nil {
				return err
			}
		}
	}
	for _, hidden := range plan.Hidden {
		if err := account(hidden.Item); err != nil {
			return err
		}
		if must[hidden.Item] {
			return fmt.Errorf("plan hides must-keep item %s", hidden.Item)
		}
		if selection == "preserve" && !allowed[hidden.Item] {
			return fmt.Errorf("preserve plan hides %s without explicit authorization", hidden.Item)
		}
		if strings.TrimSpace(hidden.Reason) == "" {
			return fmt.Errorf("each exclusion needs a reason")
		}
	}
	if len(seen) != len(known) {
		return fmt.Errorf("plan must account for every inventory item exactly once")
	}
	return nil
}

// ExportLayout exports only ordinary flat icon order. It deliberately refuses
// widget entries rather than silently converting requested geometry.
func ExportLayout(plan Plan, state State) ([]any, error) {
	if err := ValidatePlan(plan, state, "curate", 0, nil); err != nil {
		return nil, err
	}
	known := map[string]Item{}
	for _, item := range state.Items {
		known[item.ID] = item
	}
	groups := [][]string{plan.Dock}
	for _, page := range plan.Pages {
		groups = append(groups, page.Items)
	}
	result := []any{}
	for _, keys := range groups {
		row := []any{}
		for _, key := range keys {
			item := known[key]
			if item.Kind != "icon" || item.SourceID == "" {
				return nil, fmt.Errorf("widget items cannot be exported as executable icon order")
			}
			row = append(row, item.SourceID)
		}
		result = append(result, row)
	}
	return result, nil
}
