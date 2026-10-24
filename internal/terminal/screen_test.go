package terminal

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/homeward/internal/core"
	"github.com/omriariav/homeward/internal/device"
)

type fakeScreen struct {
	width, height int
	keys          []Key
	flushes       int
	onRead        func() (Key, error)
	onFlush       func()
}

func (f *fakeScreen) Size() (int, int) { return f.width, f.height }
func (f *fakeScreen) ReadKey(context.Context) (Key, error) {
	if f.onRead != nil {
		return f.onRead()
	}
	if len(f.keys) == 0 {
		return Key{}, io.EOF
	}
	k := f.keys[0]
	f.keys = f.keys[1:]
	return k, nil
}
func (f *fakeScreen) FlushInput() error {
	f.flushes++
	if f.onFlush != nil {
		f.onFlush()
	}
	return nil
}
func runeKey(r rune) Key { return Key{Name: "rune", Rune: r} }
func typed(text string) []Key {
	keys := []Key{}
	for _, r := range text {
		if r == ' ' {
			keys = append(keys, Key{Name: "space", Rune: r})
		} else {
			keys = append(keys, runeKey(r))
		}
	}
	return keys
}
func testScreen(t *testing.T) (*screen, *fakeScreen, *bytes.Buffer) {
	t.Helper()
	app, _ := testApp(t)
	out := &bytes.Buffer{}
	input := &fakeScreen{width: 80, height: 24}
	return &screen{app: app, input: input, output: out, ctx: context.Background()}, input, out
}

func TestScreenDashboardContextReviewNeverMutates(t *testing.T) {
	s, input, out := testScreen(t)
	initial := append([]Key{runeKey('2'), runeKey('3')}, typed("Work mail books שלום")...)
	initial = append(initial, Key{Name: "enter"}, runeKey('6'))
	steps := 0
	input.onRead = func() (Key, error) {
		steps++
		if steps > 150 {
			t.Fatal("screen did not finish")
		}
		if len(initial) > 0 {
			k := initial[0]
			initial = initial[1:]
			return k, nil
		}
		if s.app.Reviewed != "" {
			return runeKey('q'), nil
		}
		return Key{Name: "enter"}, nil
	}
	if err := runScreen(context.Background(), s.app, input, out, false); err != nil {
		t.Fatal(err)
	}
	if s.app.Request != "Work mail books שלום" || s.app.Reviewed == "" {
		t.Fatal("context/review not saved")
	}
	if len(s.app.Device.(*device.Local).Calls) > 0 {
		t.Fatal("screen review mutated device")
	}
	text := out.String()
	for _, want := range []string{"HOMEWARD", "Make it yours", "Proposal", "FULL APP NAMES", "\x1b[?25h\x1b[?1049l"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q", want)
		}
	}
	if strings.Contains(text, "item-000") || strings.Contains(text, "demo.mail") || strings.Contains(text, "\x1b[36m") {
		t.Fatal("primary UI exposed debug identifiers or ignored NO_COLOR")
	}
}
func TestCoverageCannotCompleteByVisitingTabsOrJumpingToEnd(t *testing.T) {
	s, input, _ := testScreen(t)
	ready(t, s.app)
	input.height = 20
	sections := proposalSections(s.app.Review.Data(), 80, true)
	for range sections {
		input.keys = append(input.keys, Key{Name: "end"}, Key{Name: "right"})
	}
	input.keys = append(input.keys, Key{Name: "esc"})
	complete, err := s.proposal()
	if err != nil || complete {
		t.Fatal("skipping overflow completed review", complete, err)
	}
	input.keys = nil
	for i := 0; i < 150; i++ {
		input.keys = append(input.keys, Key{Name: "enter"})
	}
	complete, err = s.proposal()
	if err != nil || !complete {
		t.Fatal("reading every line did not finish", complete, err)
	}
}
func TestCoverageTracksActualViewportIncludingExclusions(t *testing.T) {
	c := coverage{}
	sections := []proposalSection{{title: "Exclusions", lines: make([]screenLine, 100)}, {title: "Notes", lines: make([]screenLine, 2)}}
	c.reset(sections)
	c.mark(0, 0, 10)
	c.mark(0, 90, 100)
	c.mark(1, 0, 2)
	if _, _, done := c.next(); done {
		t.Fatal("unseen middle lines counted as read")
	}
	section, offset, _ := c.next()
	if section != 0 || offset != 10 {
		t.Fatal(section, offset)
	}
	c.mark(0, 10, 90)
	if _, _, done := c.next(); !done {
		t.Fatal("full coverage incomplete")
	}
}
func TestSensitiveInputFlushesQueuedConsentAndRequiresFreshTyping(t *testing.T) {
	s, input, _ := testScreen(t)
	s.title = "AI consent"
	s.history = []string{"Provider and model", "Data leaves this computer."}
	input.onFlush = func() {
		if input.flushes == 1 {
			input.keys = []Key{{Name: "enter"}, runeKey('y'), runeKey('e'), runeKey('s'), {Name: "enter"}}
		} else {
			input.keys = []Key{{Name: "enter"}}
		}
	}
	answer, err := s.ask("Send this information to the selected AI service? [y/N] ")
	if err != nil || answer != "" {
		t.Fatal("queued consent carried into form", answer, err)
	}
	if input.flushes != 2 {
		t.Fatal(input.flushes)
	}
}
func TestUTF8FormBackspaceAndEscape(t *testing.T) {
	s, input, _ := testScreen(t)
	input.keys = typed("שלום")
	input.keys = append(input.keys, Key{Name: "backspace"}, runeKey('ם'), Key{Name: "enter"})
	answer, err := s.ask("Context: ")
	if err != nil || answer != "שלום" {
		t.Fatal(answer, err)
	}
	input.keys = []Key{{Name: "esc"}}
	if _, err = s.ask("Context: "); !errors.Is(err, errBack) {
		t.Fatal(err)
	}
}
func TestPickerSearchLabelsAndDockOrder(t *testing.T) {
	s, input, _ := testScreen(t)
	state, err := s.app.state()
	if err != nil {
		t.Fatal(err)
	}
	input.keys = []Key{runeKey('/')}
	input.keys = append(input.keys, typed("Books")...)
	input.keys = append(input.keys, Key{Name: "enter"}, Key{Name: "space"}, Key{Name: "enter"})
	ids, saved, err := s.pickApps(state, nil, false)
	if err != nil || !saved || len(ids) != 1 || ids[0] != "item-0007" {
		t.Fatal(ids, saved, err)
	}
	input.keys = []Key{runeKey(']'), Key{Name: "enter"}}
	ids, saved, err = s.pickApps(state, nil, true)
	if err != nil || !saved || ids[1] != "item-0001" {
		t.Fatal("Dock order not editable", ids, err)
	}
	input.keys = []Key{runeKey('p')}
	ids, saved, err = s.pickApps(state, ids, true)
	if err != nil || !saved || ids != nil {
		t.Fatal("preserve Dock not explicit", ids, saved, err)
	}
}
func TestNarrowScreenAndResizeDoNotClipControlsOrCountHiddenContent(t *testing.T) {
	s, input, out := testScreen(t)
	input.width = 50
	input.height = 16
	step := 0
	input.onRead = func() (Key, error) {
		step++
		if step == 1 {
			input.width = 80
			input.height = 24
			return Key{Name: "tick"}, nil
		}
		return runeKey('q'), nil
	}
	if err := runScreen(context.Background(), s.app, input, out, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Resize to at least 56 × 20") || !strings.Contains(out.String(), "Choose your mode") {
		t.Fatal(out.String())
	}
	ready(t, s.app)
	for _, width := range []int{1, 20, 50, 56, 80} {
		_ = proposalSections(s.app.Review.Data(), width, true)
	}
}

type brokenScreenWriter struct {
	bytes.Buffer
	calls int
}

func (w *brokenScreenWriter) Write(data []byte) (int, error) {
	w.calls++
	w.Buffer.Write(data)
	if w.calls >= 2 {
		return 0, errors.New("output unavailable")
	}
	return len(data), nil
}
func TestScreenOutputErrorStillAttemptsCursorAndAlternateScreenRestoration(t *testing.T) {
	s, input, _ := testScreen(t)
	output := &brokenScreenWriter{}
	previous := s.app.UI
	err := runScreen(context.Background(), s.app, input, output, true)
	if err == nil {
		t.Fatal("ignored render failure")
	}
	if !strings.Contains(output.String(), "\x1b[?25h\x1b[?1049l") || s.app.UI != previous {
		t.Fatal("screen cleanup missing")
	}
}
func TestUntrustedLabelsCannotEscapeScreenStyling(t *testing.T) {
	s, input, out := testScreen(t)
	s.app.Metadata.Apps["demo.mail"] = core.AppMetadata{Label: "\x1b[?1049lMail\x07\u202e"}
	ready(t, s.app)
	input.keys = []Key{{Name: "enter"}, {Name: "esc"}}
	_, _ = s.proposal()
	if strings.Contains(out.String(), "\x1b[?1049l") || strings.ContainsAny(out.String(), "\x07\u202e") {
		t.Fatal("label escaped trusted rendering")
	}
}
func TestBusyOperationCancellationJoinsBeforeReturn(t *testing.T) {
	s, input, _ := testScreen(t)
	input.keys = []Key{{Name: "esc"}}
	joined := make(chan struct{})
	err := s.work(context.Background(), "Synthetic operation", func(ctx context.Context) error { <-ctx.Done(); close(joined); return ctx.Err() })
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	select {
	case <-joined:
	case <-time.After(time.Second):
		t.Fatal("operation goroutine left running")
	}
}
func TestInventoryPrimaryViewHidesTechnicalIdentifiers(t *testing.T) {
	s, input, out := testScreen(t)
	input.keys = []Key{{Name: "esc"}}
	if _, err := s.inventory(); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if strings.Contains(text, "item-") || strings.Contains(text, "demo.") || strings.Contains(text, "cfgutil") || strings.Contains(text, "{\"") {
		t.Fatal(text)
	}
	if !strings.Contains(text, "Phone") {
		t.Fatal("labels missing")
	}
}
