# Homeward

Homeward is an experimental iPhone Home Screen organizer with a **Go executable**
and a keyboard-driven terminal interface. Import or inspect a layout, describe your
priorities, answer a short interview, review a curated shortlist, revise it, and
explicitly confirm Apply. The Go app has no Python runtime dependency and uses
only the Go standard library. The Apple device runner uses Swift and XCTest.

On an interactive terminal, Homeward opens a full-screen dashboard with highlighted
navigation, app pickers and a browsable proposal. It works without AI and can
exercise the entire workflow using synthetic data. A plain line-oriented interface
remains available for scripts and limited terminals.

## Build and try it

Use Go 1.24 or newer to build. No third-party Go libraries or package installation
are needed. The resulting executable does not need Go installed to run.

```sh
mkdir -p .build/bin
go build -o .build/bin/homeward ./cmd/homeward
.build/bin/homeward tui --demo
```

The demo never invokes a phone or external AI. A synthetic layout is already
loaded. Use **↑/↓** or **j/k** to move, **Enter** to open, and **1–8** as action
shortcuts. **Esc** goes back; **q** exits the dashboard. **?** shows session details
and the local evidence directory.

1. **Your context** — describe priorities, such as “Work mail, calendar and books;
   less prominent social apps.” Text fields accept Unicode; Enter submits,
   Ctrl-U clears, and Escape cancels.
2. **Layout preferences** — choose a page budget, then select must-keeps and Dock
   apps by their names. **Space** toggles an app; **/** searches; Enter saves.
   In the Dock picker, **[ / ]** moves a selected app earlier/later and **p** keeps
   the current Dock order. IDs are available only in optional app details.
3. **Interview** — answer focused questions or press Enter to explicitly skip.
4. **Create proposal** — browse the overview, Dock, four-column phone pages,
   changes, exclusions and notes. **←/→** changes sections; ↑/↓ or Page Up/Down
   scrolls. Enter moves to the next unread portion and finishes only after every
   line has been displayed. Escape leaves the proposal unreviewed.
5. **Apply reviewed proposal** — for the simulation, first select `standard`
   alignment and explicitly accept the widget/reflow limitation in preferences.
   Read the final disclosure, then freshly type the displayed `APPLY <fingerprint>`.
   Anything else cancels. Nothing reaches a phone in demo mode.

The dashboard explains when Apply is unavailable and opens its details. Revision
invalidates the previous review. Jumping over long exclusions or entering a section
without scrolling through it cannot complete review. Resizing to a different width
resets review coverage conservatively. Typed consent and Apply prompts discard
queued navigation/paste input, so keys from the previous screen cannot confirm them.

Long AI/device operations show elapsed time and an activity indicator, without
invented progress percentages. Escape or Ctrl-C cancels the operation and waits
for it to stop; outside an operation, Ctrl-C or Ctrl-D exits. Cursor visibility and
terminal mode are restored on exit, cancellation and errors.

The full-screen backend targets macOS, with native Linux amd64/arm64 support.
It uses only standard-library terminal system calls. `NO_COLOR=1` removes colors
while retaining selection markers. `TERM=dumb`, non-TTY input/output and unsupported
platforms use the plain interface. Small windows show a resize hint instead of
clipped controls; use at least **56 columns × 20 rows**. To explicitly use plain mode:

```sh
.build/bin/homeward tui --demo --plain
.build/bin/homeward tui --demo --no-pager   # plain, unpaginated previews
```

The visual hierarchy and keyboard-navigation direction were inspired by
[Mole](https://github.com/tw93/Mole). Homeward's interface is independently
implemented; no Mole code, assets or branding are incorporated.

## Saved layouts and local planning

```sh
.build/bin/homeward tui --layout examples/planner-layout.json --no-ai
.build/bin/homeward plan --layout examples/planner-layout.json \
  --request 'Work first, keep personal interests represented. No folders.' \
  --max-pages 2 --no-ai --output artifacts/local-draft.json
.build/bin/homeward preview artifacts/local-draft.json
```

Without a configured live target, Apply changes only an in-memory simulation.
Optional `--metadata FILE` supplies app labels/colors, widget observations and
notes. For example:

```json
{
  "apps": {
    "com.apple.mobilephone": {"label": "Phone"},
    "com.example.reader": {"label": "Books", "color": "orange"}
  },
  "observations": ["Existing widget configuration is not captured by cfgutil."]
}
```

The default policy is a relevant shortlist with **no folders**. Local planning
matches context, labels and folder names; it is deliberately provisional, not a
semantic AI model or usage analysis. It honors explicit must-keeps, Dock order and
page limits, and explains every exclusion. Without useful matches it labels its
small original-order fallback for review. It never claims an app is unused.

Each session writes private, separate capture, context, answer, preference and
draft files under ignored `artifacts/tui/session-*`. Draft paths are never
silently overwritten. These files can contain personal information; keep them
local. Saved Go interview answers are evidence for that session; Go interview
resume/import is not implemented yet. The Python reference retains its resumable
interview command.

## Optional external AI

Choose `ai` in an interview or planning text prompt. Each request independently
discloses the provider, model and data being sent, then asks permission with **No**
as the default. Consent for questions does not authorize the planning request.
Refusal disables external AI for the rest of that session; local planning remains
available. `--no-ai` and `--demo` prevent external requests entirely.

The installed, authenticated Claude Code or Codex CLI receives layout identifiers,
labels, metadata, context, preferences and answers through stdin from an isolated
temporary directory. Provider/model selection is explicit; there is no automatic
provider fallback. Responses are schema-checked and semantically validated.

```sh
.build/bin/homeward tui --layout examples/planner-layout.json --provider codex
.build/bin/homeward plan --layout examples/planner-layout.json \
  --request 'A focused work and reading Home Screen' --ai --provider codex \
  --output artifacts/ai-draft.json
```

Headless callers can pass `--consent-ai` only after obtaining consent to that
specific disclosure. Saved preview and local planning make no external requests.

## Physical device workflow

Physical automation requires macOS, Apple Configurator (`cfgutil`), Xcode,
a trusted USB-connected and unlocked iPhone, Developer Mode, UI Automation,
and your own Apple Development signing identity/profile. The app does not
configure trust, enable Developer Mode or request provisioning automatically.

```sh
.build/bin/homeward doctor
.build/bin/homeward devices
.build/bin/homeward build --team YOUR_TEAM_ID --device YOUR_UDID \
  --bundle-id com.yourname.homeward
.build/bin/homeward tui --device YOUR_UDID --ecid YOUR_ECID --no-ai
```

Find ECID in Apple Configurator. The app verifies that ECID and UDID identify the
same phone before accepting a capture. Menu **1** explicitly requests a read-only
capture. Startup does not contact the phone. The complete `devicectl` app catalog
includes App Library apps; the known incomplete `cfgutil installedApps` output is
not used as an authority. If capture fails, import a saved layout for local review.

`build --allow-provisioning` explicitly allows Xcode to update profiles and, with
`--device`, register that device. Use `--root PATH` when the runner checkout cannot
be found from the executable/current directory. Set `DEVELOPER_DIR` for a nonstandard
Xcode installation. Rebuild the runner after moving the checkout; old build products
can contain absolute paths. A runner file alone does not prove signing, trust,
Developer Mode or UI Automation readiness.

For an approved, supported plan, Go performs this sequence directly, without Python:

1. Validate the exact reviewed plan, preferences and device binding; check the
   runner and unlocked state; recapture identity, icon layout and app inventory.
   A changed layout or captured inventory rejects Apply before any placement.
2. Save the before-state and exact expected/staging layouts. Stage the selected
   Dock/pages first and **all observed launchable apps** on explicit trailing pages
   of at most 24 icons. Inventory checks precede cfgutil's `--force`, which avoids
   its known false unknown-app reports. More than 15 staging pages blocks Apply.
3. Require exact staging readback. If it differs, stop without page cleanup.
4. Use the existing Swift runner to hide then remove the trailing pages. This
   removes ordinary apps from Home Screen, not from the phone.
5. Require final icon-order readback to match the reviewed layout exactly.

Command logs, before-state, intermediate/final readbacks and XCTest result bundles
stay in the private session directory. A failure after a mutation is reported as
**partial or unknown**, with evidence and instructions to inspect the phone. There
is no automatic rollback or blind retry. Cancellation stops host subprocess groups;
a device-side XCTest operation may still be finishing.

The Go executor has synthetic/injected tests, **not physical-device validation**.
The earlier manually orchestrated cfgutil + XCTest sequence worked experimentally
on one physical device. That evidence is not a guarantee for the new Go integration,
other devices, or iOS versions.

## Capability limits

- **Bottom alignment, preserved free positions and reserved widget spaces are not
  executable.** Preferences are stored and shown, and block automatic Apply. Use
  the preview as a manual guide when these matter.
- Standard order means sequential rows without empty slots. A bulk write can
  remove existing widgets and reflow spaces. Explicit acceptance is part of the
  reviewed preferences and final confirmation. Readback checks icon order, not
  widget configurations or geometric placement. Add/reconfigure widgets manually.
- **Unresolved shortcuts, including `/` placeholders, block automatic Apply.**
  Distinct occurrences are never considered interchangeable. Non-app identifiers
  absent from the complete catalog and repeated identifiers also block execution.
  Existing shortcut-bearing layouts require a manual workflow; do not delete or
  recreate a shortcut based on an ambiguous cfgutil export.
- Automatic cleanup requires the XCTest path and Developer Mode. A cfgutil-only,
  no-Developer-Mode path is disabled because omitted apps may reappear.
- Saved cfgutil layouts are reconstruction aids, not exact backups of shortcuts,
  widgets or arbitrary icon geometry. Cleanup uses English labels and experimental
  runner geometry. App uninstallation is never part of this workflow.

Compare independently saved icon layouts with:

```sh
.build/bin/homeward verify-layout --expected selected-layout.json \
  --actual device-readback.json
```

## Development and Python reference

```sh
go test ./...
go test -race ./...
go vet ./...
python3 -m unittest discover -s tests -v
.build/bin/homeward build --simulator
```

Go tests use synthetic data and injected subprocess/device adapters, including a
complete menu-to-Apply simulation. Screen tests cover navigation, UTF-8, exact
review coverage, queued-consent rejection, resize, output errors and terminal
restoration. Native macOS PTY tests exercise keys and complete screen exit paths.
They do not contact a phone or AI service.
Simulator compilation does not establish physical-device behavior.

The original standard-library Python prototype remains available as reference:
`python3 homescreen.py plan --help`, `interview --help`, `preview DRAFT`, and its
low-level device experiment commands. Existing Python commands are unchanged;
the Go executable does not invoke them. Keep personal evidence, signing material,
screenshots and build outputs out of source control.

Apple's [XCUIAutomation](https://developer.apple.com/documentation/xcuiautomation)
provides the runner APIs. Earlier research consulted
[pymobiledevice3](https://github.com/doronz88/pymobiledevice3) and
[WebDriverAgent](https://github.com/appium/WebDriverAgent); neither is a dependency
and no code from either is incorporated here. Future reuse must follow upstream
licensing and notices.
