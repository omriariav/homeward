package terminal

import (
	"fmt"
	"strings"

	"github.com/omriariav/homeward/internal/core"
)

func plural(n int, singular, multiple string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", singular)
	}
	return fmt.Sprintf("%d %s", n, multiple)
}

func (s *screen) inventory() (bool, error) {
	state, err := s.app.state()
	if err != nil {
		return false, err
	}
	selected := 0
	query := ""
	searching := false
	for {
		items := []core.Item{}
		for _, item := range state.Items {
			if strings.Contains(strings.ToLower(item.Label+" "+strings.Join(item.SourceFolders, " ")), strings.ToLower(query)) {
				items = append(items, item)
			}
		}
		selected = min(selected, max(0, len(items)-1))
		w, _ := s.size()
		listRows := max(1, s.rows()-3)
		start := max(0, selected-listRows+1)
		body := []screenLine{}
		search := "Search: " + query
		if searching {
			search += "▏"
		}
		body = append(body, dim(search))
		for i := start; i < min(len(items), start+listRows); i++ {
			item := items[i]
			prefix := "  "
			tone := ""
			if i == selected {
				prefix = "› "
				tone = "selected"
			}
			body = append(body, screenLine{prefix + pad(item.Label, (w-8)/2) + "  " + source(item), tone})
		}
		if len(items) == 0 {
			body = append(body, dim("No matching apps. Press / to change the search."))
		}
		for len(body) < listRows+1 {
			body = append(body, line(""))
		}
		if len(items) > 0 {
			body = append(body, dim(items[selected].Label))
		}
		footer := "↑↓ browse  / search  Enter details  Esc back"
		if searching {
			footer = "Type to search  Backspace edit  Enter / Esc finish"
		}
		fits := s.paint("Your apps", plural(len(items), "app shown", "apps shown")+" · from the saved layout", body, footer)
		k, err := s.key()
		if err != nil {
			return false, err
		}
		if !fits {
			if isBack(k) {
				return false, nil
			}
			continue
		}
		if searching {
			switch k.Name {
			case "enter", "esc":
				searching = false
			case "backspace":
				r := []rune(query)
				if len(r) > 0 {
					query = string(r[:len(r)-1])
					selected = 0
				}
			case "ctrl-u":
				query = ""
				selected = 0
			case "rune":
				query += string(k.Rune)
				selected = 0
			case "space":
				query += " "
				selected = 0
			}
			continue
		}
		if isBack(k) {
			return true, nil
		}
		if isRune(k, '/') {
			searching = true
			continue
		}
		if len(items) == 0 {
			continue
		}
		if down(k) {
			selected = (selected + 1) % len(items)
		}
		if up(k) {
			selected = (selected + len(items) - 1) % len(items)
		}
		if k.Name == "enter" {
			item := items[selected]
			text := item.Label + "\n\nLocation: " + source(item)
			if len(item.SourcePath) > 0 {
				text += fmt.Sprintf("\nPosition in its container: %d", item.SourcePath[len(item.SourcePath)-1]+1)
			}
			if item.SourceID != "" {
				text += "\n\nAdvanced identifier:\n" + item.SourceID
			}
			text += "\n\nRelevance comes from your context, not measured app usage."
			if _, err = s.viewer("App details", text); err != nil {
				return false, err
			}
		}
	}
}

// readDisclosure requires every displayed disclosure line, including overflow,
// before transitioning to a newly flushed typed consent/Apply form.
func (s *screen) readDisclosure(text string) (bool, error) {
	offset := 0
	lastWidth := 0
	var seen []bool
	for {
		w, _ := s.size()
		raw := wrap(text, w-4)
		if w != lastWidth {
			seen = make([]bool, len(raw))
			lastWidth = w
		}
		rows := s.rows()
		offset = min(offset, max(0, len(raw)-rows))
		body := []screenLine{}
		for _, text := range raw[offset:min(len(raw), offset+rows)] {
			body = append(body, line(text))
		}
		read := 0
		for _, v := range seen {
			if v {
				read++
			}
		}
		fits := s.paint("Before you confirm", fmt.Sprintf("Read the details · %d / %d lines", read, len(raw)), body, "↑↓ scroll  Enter next / type answer  Esc cancel")
		if fits {
			for i := offset; i < min(len(raw), offset+rows); i++ {
				seen[i] = true
			}
		}
		k, err := s.key()
		if err != nil {
			return false, err
		}
		if isBack(k) {
			return false, nil
		}
		if !fits {
			continue
		}
		if k.Name == "enter" {
			next := -1
			for i, v := range seen {
				if !v {
					next = i
					break
				}
			}
			if next < 0 {
				return true, nil
			}
			offset = next
		}
		if down(k) {
			offset = min(offset+1, max(0, len(raw)-rows))
		}
		if up(k) {
			offset = max(0, offset-1)
		}
		if k.Name == "pagedown" || k.Name == "space" {
			offset = min(offset+rows, max(0, len(raw)-rows))
		}
		if k.Name == "pageup" {
			offset = max(0, offset-rows)
		}
	}
}
