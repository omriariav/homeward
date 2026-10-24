// Homeward is a standard-library-only terminal organizer. It never changes a
// phone or sends AI requests merely by starting.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/omriariav/homeward/internal/core"
	"github.com/omriariav/homeward/internal/organizer"
	"github.com/omriariav/homeward/internal/terminal"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", terminal.Safe(err.Error()))
		os.Exit(1)
	}
}

// Canonical reads can block until a newline. Install this watcher only after TTY
// detection and raw-mode setup have been ruled out: File.Close racing File.Fd is
// unsafe. Raw mode instead polls context on its separately owned descriptor.
func closeCanonicalInputOnCancel(ctx context.Context, input io.Reader) func() {
	file, ok := input.(*os.File)
	if !ok {
		return func() {}
	}
	stop := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		select {
		case <-ctx.Done():
			_ = file.Close()
		case <-stop:
		}
	}()
	return func() { close(stop); <-finished }
}

func rootDir() string {
	cwd, _ := os.Getwd()
	if _, err := os.Stat(filepath.Join(cwd, "runner", "HomeScreenPOC.xcodeproj")); err == nil {
		return cwd
	}
	exe, err := os.Executable()
	if err == nil {
		dir := filepath.Dir(exe)
		for i := 0; i < 5; i++ {
			if _, err := os.Stat(filepath.Join(dir, "runner", "HomeScreenPOC.xcodeproj")); err == nil {
				return dir
			}
			next := filepath.Dir(dir)
			if next == dir {
				break
			}
			dir = next
		}
	}
	return cwd
}
func flags(name string, out io.Writer) *flag.FlagSet {
	f := flag.NewFlagSet(name, flag.ContinueOnError)
	f.SetOutput(out)
	return f
}
func run(ctx context.Context, args []string, in io.Reader, out io.Writer) error {
	command := "tui"
	if len(args) > 0 {
		command = args[0]
		args = args[1:]
	}
	switch command {
	case "help", "--help", "-h":
		fmt.Fprintln(out, "Homeward — interactive iPhone Home Screen organizer\n\n  homeward tui --demo             synthetic local workflow; no phone or AI\n  homeward tui --layout FILE      import a layout, local simulation\n  homeward tui --device UDID --ecid ECID   explicitly enabled live target\n  homeward preview DRAFT          read-only saved proposal\n  homeward plan --help            draft from a saved layout\n  homeward verify-layout --expected FILE --actual FILE\n  homeward build --help           build the Apple XCTest runner\n  homeward doctor | devices\n\nEach command supports --help. No Python or third-party Go libraries required.")
		return nil
	case "tui":
		f := flags("homeward tui", out)
		options := terminal.Options{}
		f.StringVar(&options.Root, "root", rootDir(), "checkout root containing runner/ and .build/")
		f.StringVar(&options.OutputDir, "output-dir", "", "private session evidence directory (default ROOT/artifacts/tui)")
		f.StringVar(&options.Layout, "layout", "", "saved cfgutil layout; simulation unless live target supplied")
		f.StringVar(&options.Metadata, "metadata", "", "optional app labels, colors and widget observations JSON")
		f.StringVar(&options.Device, "device", "", "physical device UDID; startup does not read or change phone")
		f.StringVar(&options.ECID, "ecid", "", "same phone ECID; pairing checked during capture")
		f.StringVar(&options.Cfgutil, "cfgutil", "", "override Apple Configurator cfgutil executable")
		f.StringVar(&options.Provider, "provider", "claude", "optional external AI: claude or codex")
		f.StringVar(&options.Model, "model", "", "optional provider model override")
		f.BoolVar(&options.Demo, "demo", false, "synthetic workflow; disables all phone and AI commands")
		f.BoolVar(&options.NoAI, "no-ai", false, "local interview and planning only")
		noPager := f.Bool("no-pager", false, "plain mode; print previews without page prompts")
		plain := f.Bool("plain", false, "use the line-oriented interface, without raw mode or screen redraws")
		if err := f.Parse(args); err != nil {
			return flagError(err)
		}
		if f.NArg() != 0 {
			return errors.New("unexpected positional arguments")
		}
		ui := terminal.NewTerminal(in, out, !*noPager)
		app, err := terminal.New(options, ui, nil)
		if err != nil {
			return err
		}
		if !*plain && !*noPager && os.Getenv("TERM") != "dumb" {
			inputFile, inputOK := in.(*os.File)
			outputFile, outputOK := out.(*os.File)
			if inputOK && outputOK && terminal.IsTerminal(inputFile) && terminal.IsTerminal(outputFile) {
				screen, err := terminal.OpenScreen(inputFile, outputFile)
				if err != nil {
					return fmt.Errorf("open terminal: %w (use --plain to continue)", err)
				}
				return func() (err error) {
					defer func() {
						if closeErr := screen.Close(); closeErr != nil {
							err = errors.Join(err, fmt.Errorf("restore terminal: %w", closeErr))
						}
					}()
					return terminal.RunScreen(ctx, app, screen, out, os.Getenv("NO_COLOR") == "")
				}()
			}
		}
		stopInput := closeCanonicalInputOnCancel(ctx, in)
		defer stopInput()
		return app.Run(ctx)
	case "preview":
		f := flags("homeward preview DRAFT", out)
		if err := f.Parse(args); err != nil {
			return flagError(err)
		}
		if f.NArg() != 1 {
			return errors.New("preview requires one draft JSON path")
		}
		var artifact core.Artifact
		if err := terminal.ReadJSON(f.Arg(0), &artifact); err != nil {
			return err
		}
		text, err := terminal.Render(artifact)
		if err != nil {
			return err
		}
		fmt.Fprint(out, text)
		return nil
	case "verify-layout":
		f := flags("homeward verify-layout", out)
		expected := f.String("expected", "", "selected layout JSON")
		actual := f.String("actual", "", "fresh device readback JSON")
		if err := f.Parse(args); err != nil {
			return flagError(err)
		}
		var a, b []any
		if err := terminal.ReadJSON(*expected, &a); err != nil {
			return err
		}
		if err := terminal.ReadJSON(*actual, &b); err != nil {
			return err
		}
		for _, layout := range [][]any{a, b} {
			if _, err := core.Normalize(layout, core.Metadata{}); err != nil {
				return err
			}
		}
		if !reflect.DeepEqual(a, b) {
			return errors.New("device layout differs: extra, missing or reordered icons/pages/folders")
		}
		fmt.Fprintln(out, "Verified: icon layout exactly matches; widget/free-position geometry is not checked.")
		return nil
	case "plan":
		return plan(ctx, args, in, out)
	case "build":
		return build(ctx, args, out)
	case "doctor":
		if len(args) > 0 {
			return errors.New("doctor takes no arguments")
		}
		if err := external(ctx, out, "xcodebuild", "-version"); err != nil {
			return err
		}
		if err := external(ctx, out, "security", "find-identity", "-v", "-p", "codesigning"); err != nil {
			return err
		}
		fmt.Fprintln(out, "Physical XCTest needs a trusted, unlocked USB iPhone, Developer Mode and Settings > Developer > Enable UI Automation. Build your own signed runner; readiness is not established by file presence alone.")
		return nil
	case "devices":
		if len(args) > 0 {
			return errors.New("devices takes no arguments")
		}
		return external(ctx, out, "xcrun", "devicectl", "list", "devices", "--timeout", "20")
	default:
		return fmt.Errorf("unknown command %q; use homeward --help", command)
	}
}
func flagError(err error) error {
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	return err
}
func plan(ctx context.Context, args []string, in io.Reader, out io.Writer) error {
	f := flags("homeward plan", out)
	layoutPath := f.String("layout", "", "saved cfgutil layout JSON (required)")
	metadataPath := f.String("metadata", "", "optional label/widget metadata JSON")
	request := f.String("request", "", "free-text organization context (required)")
	output := f.String("output", "", "new draft JSON path (required)")
	pages := f.Int("max-pages", 2, "hard page budget, excluding Dock")
	keep := f.String("keep", "", "comma-separated must-keep inventory IDs")
	ai := f.Bool("ai", false, "request external AI planning, with separate consent")
	noAI := f.Bool("no-ai", false, "refuse external AI requests")
	consented := f.Bool("consent-ai", false, "explicitly authorize this request after reviewing the disclosure")
	provider := f.String("provider", "claude", "claude or codex")
	model := f.String("model", "", "optional model override")
	if err := f.Parse(args); err != nil {
		return flagError(err)
	}
	if *layoutPath == "" || strings.TrimSpace(*request) == "" || *output == "" {
		return errors.New("--layout, --request and --output are required")
	}
	if *ai && *noAI {
		return errors.New("--ai and --no-ai conflict; nothing sent")
	}
	if _, err := os.Stat(*output); err == nil {
		return errors.New("output already exists; choose a new path")
	}
	if *pages < 1 || *pages > 15 {
		return errors.New("--max-pages must be 1–15")
	}
	var layout []any
	var metadata core.Metadata
	if err := terminal.ReadJSON(*layoutPath, &layout); err != nil {
		return err
	}
	if *metadataPath != "" {
		if err := terminal.ReadJSON(*metadataPath, &metadata); err != nil {
			return err
		}
	}
	state, err := core.Normalize(layout, metadata)
	if err != nil {
		return err
	}
	prefs := core.Preferences{MaxPages: *pages, Alignment: "preserve"}
	if *keep != "" {
		prefs.Keep = strings.Fields(strings.ReplaceAll(*keep, ",", " "))
	}
	for _, item := range state.Items {
		if item.SourceID == "/" || item.Kind != "icon" {
			prefs.Keep = append(prefs.Keep, item.ID)
		}
	}
	input := core.Input{Selection: "curate", MaxPages: *pages, RequiredVisible: prefs.Keep, Request: *request, CurrentState: state, Preferences: prefs}
	var proposal core.Plan
	generation := "local"
	if *ai {
		if *model == "" {
			*model = core.DefaultModel(*provider)
		}
		ui := terminal.NewTerminal(in, out, false)
		ui.Print(terminal.Disclosure(*provider, *model, state))
		allow := *consented
		if !allow {
			answer, err := func() (string, error) {
				stopInput := closeCanonicalInputOnCancel(ctx, in)
				defer stopInput()
				return ui.Ask("Send this information to the selected AI service? [y/N] ")
			}()
			if err != nil {
				return errors.New("explicit AI consent required; nothing sent")
			}
			allow = strings.EqualFold(answer, "y") || strings.EqualFold(answer, "yes")
		}
		if !allow {
			return errors.New("AI sharing declined; nothing sent")
		}
		client := core.AIClient{}
		proposal, err = client.Plan(ctx, core.AIRequest{Provider: *provider, Model: *model, Effort: "medium", Input: input, Consent: true})
		generation = *provider
	} else {
		proposal, err = core.LocalPlan(*request, state, prefs, nil)
	}
	if err != nil {
		return err
	}
	artifact := core.Artifact{Version: 1, Status: "draft", Provider: generation, Model: *model, Input: input, Plan: proposal}
	if err = os.MkdirAll(filepath.Dir(*output), 0700); err != nil {
		return err
	}
	if err = organizer.SaveJSON(*output, artifact); err != nil {
		return err
	}
	text, err := terminal.Render(artifact)
	if err != nil {
		return err
	}
	fmt.Fprintln(out, "Saved draft:", *output)
	fmt.Fprint(out, text)
	return nil
}
func external(ctx context.Context, out io.Writer, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = out
	cmd.Stderr = out
	cmd.Env = os.Environ()
	if os.Getenv("DEVELOPER_DIR") == "" {
		cmd.Env = append(cmd.Env, "DEVELOPER_DIR=/Applications/Xcode.app/Contents/Developer")
	}
	return cmd.Run()
}
func build(ctx context.Context, args []string, out io.Writer) error {
	f := flags("homeward build", out)
	root := f.String("root", rootDir(), "checkout root")
	team := f.String("team", "", "Apple Developer team ID (physical builds)")
	udid := f.String("device", "", "physical UDID for provisioning")
	bundle := f.String("bundle-id", "local.homeward.runner", "your runner bundle ID")
	sim := f.Bool("simulator", false, "unsigned simulator compile validation")
	provision := f.Bool("allow-provisioning", false, "allow Xcode to update profiles and register the explicitly supplied device")
	if err := f.Parse(args); err != nil {
		return flagError(err)
	}
	if *sim && *udid != "" {
		return errors.New("--simulator and --device cannot be combined")
	}
	if !*sim && *team == "" {
		return errors.New("physical builds require --team YOUR_TEAM_ID and your signing identity")
	}
	platform := "device"
	destination := "generic/platform=iOS"
	if *sim {
		platform = "simulator"
		destination = "generic/platform=iOS Simulator"
	} else if *udid != "" {
		destination = "id=" + *udid
	}
	command := []string{"build-for-testing", "-project", filepath.Join(*root, "runner", "HomeScreenPOC.xcodeproj"), "-scheme", "HomeScreenPOC", "-derivedDataPath", filepath.Join(*root, ".build", platform), "-destination", destination, "PRODUCT_BUNDLE_IDENTIFIER=" + *bundle}
	if *sim {
		command = append(command, "CODE_SIGNING_ALLOWED=NO")
	} else {
		command = append(command, "DEVELOPMENT_TEAM="+*team)
	}
	if *provision {
		command = append(command, "-allowProvisioningUpdates")
		if *udid != "" {
			command = append(command, "-allowProvisioningDeviceRegistration")
		}
	}
	return external(ctx, out, "xcodebuild", command...)
}
