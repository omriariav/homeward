# Homeward

Homeward is an experimental Mac command-line toolkit for organizing an iPhone’s
Home Screen around what matters to its owner. It combines a context interview,
AI-assisted draft planning, a read-only terminal preview, and low-level XCTest
commands for device experiments.

The Python host uses only the standard library. The Swift device runner uses
Apple’s XCTest/XCUIAutomation frameworks. There are no third-party runtime
libraries. AI planning optionally invokes an installed, authenticated Claude Code
or Codex CLI; device automation requires Apple’s tooling.

## Current status

Implemented:

- Context interview with tailored questions, saved answers, pause/resume, and
  headless question/answer-file support.
- Explicit AI-sharing disclosure and consent for each request, defaulting to No;
  `--no-ai` refuses external requests.
- Validated draft planning from a saved layout, with preservation or curated
  selection, protected items, page limits, and explained exclusions.
- Read-only terminal preview of proposed pages, Dock, moves, exclusions, and
  limitations; exact layout comparison against a supplied device readback.
- XCTest primitives for inspection, icon movement/removal, widget experiments,
  and page cleanup. Removal from Home Screen keeps ordinary apps installed.

A manually orchestrated Configurator `cfgutil` placement followed by XCTest page
cleanup worked on a physical device, with the resulting layout matching the
reviewed input. This is evidence for that workflow, not an integrated Apply
implementation or a guarantee across devices and iOS versions.

**A full-screen TUI and integrated Apply/verify workflow are not implemented.**
The current preview displays four-column icon order; it does not model widget
sizes, reserved space, free placement, or top/bottom alignment. Configurator
writes have removed existing widgets in testing and can repopulate omitted apps.
Saved layouts are reconstruction aids, not guaranteed exact backups.

## Requirements

- macOS with Xcode and its first-run setup complete for device commands.
- Python 3.9 or newer; no pip installation needed for the host CLI or tests.
- Optional authenticated Claude Code or Codex CLI for external AI requests.
- Apple Configurator and its `cfgutil` for the separately operated placement path.
- For physical XCTest automation: a trusted USB-connected, unlocked iPhone,
  Developer Mode, UI Automation enabled, and your own Apple Development signing
  identity and provisioning profile.

Set `DEVELOPER_DIR` if Xcode is installed outside `/Applications/Xcode.app`.

## Try the backend

Inspect commands and normalize the synthetic example without AI or device access:

```sh
python3 homescreen.py --help
python3 homescreen.py plan --layout examples/planner-layout.json \
  --request 'Work first, retain personal interests. No folders.' \
  --dry-run --no-ai --output artifacts/normalized-example.json
```

Start an interview, then draft a relevant shortlist from its completed answers:

```sh
python3 homescreen.py interview --layout examples/planner-layout.json \
  --request 'A focused Home Screen for work and personal interests, without folders.' \
  --output artifacts/interview.json
python3 homescreen.py plan --interview artifacts/interview.json \
  --selection curate --max-pages 2 --output artifacts/draft.json
python3 homescreen.py preview artifacts/draft.json
```

AI requests disclose the selected provider/model and the inventory, layout,
metadata, context, and answers being sent. Interactive consent defaults to No.
Noninteractive callers must obtain that consent before passing `--consent-ai`.
Declining does not silently switch providers or substitute a local model. Local
preview, dry runs, and saved-answer editing do not require AI consent.

Use `interview --resume artifacts/interview.json` after a pause. The default
planning policy is `preserve`; `--selection curate` explicitly enables a shortlist
and accounts for excluded items. Relevance is inferred from supplied context,
not measured usage. Drafts currently use flat pages rather than creating folders.

A successful plan prints its preview. Reopening it sends no AI requests and makes
no device changes. Preview uses the saved planning snapshot, not a live readback.
Viewing it does not authorize applying it. Output files must use new paths.

## Device experiments

```sh
python3 homescreen.py devices
python3 homescreen.py build --team YOUR_TEAM_ID \
  --bundle-id com.yourname.homeward --device YOUR_UDID
python3 homescreen.py inspect --device YOUR_UDID
```

`--allow-provisioning` explicitly permits Xcode to update provisioning profiles
and, when a device is supplied, register it with the selected team. XCTest
commands install/start a development test runner. `inspect` captures the current
screen; `home` navigates Home. Gesture commands operate on the visible screen.
Use each command’s `--help` and inspect fresh evidence before choosing coordinates.

Page cleanup uses English labels and experimentally tested geometry. Removing
pages can also remove website bookmarks or shortcuts that cannot live in App
Library. Widget reconstruction, cross-page movement, arbitrary layouts, and exact
rollback are not generally supported. Low-level commands are experimental and
must not be treated as a completed automatic organizer.

After a separately reviewed and executed placement/cleanup workflow, compare
saved expected and actual layouts with:

```sh
python3 homescreen.py verify-layout --expected selected-layout.json \
  --actual device-readback.json
```

This checks placements/order, extra or missing icons/pages, and folders; it does
not assess relevance or widget geometry.

## Product direction

The intended TUI should select a relevant shortlist rather than place every app,
use no folders by default, preserve explicit AI consent/refusal, and show the
proposal before explicit Apply confirmation. Revision must invalidate prior
confirmation, and execution must recheck live device state.

Top/bottom icon alignment is a requested preference, and manually reserved space
for user-added widgets without Developer Mode is a requested option. Both remain
future requirements: actual placement geometry and the feasibility of that
no-Developer-Mode path are unverified. The current XCTest path needs Developer
Mode; no placeholder or widget reservation support is claimed.

## Development

```sh
python3 -m unittest discover -s tests -v
python3 homescreen.py build --simulator
```

The host suite contains 34 tests. Simulator compilation/run evidence does not
establish physical-device behavior. Keep personal layouts, interview answers,
screenshots, device evidence, and signing material local. The repository ignores
`artifacts/`, `local-notes/`, research/feedback drafts, and build outputs.

Apple’s [XCUIAutomation](https://developer.apple.com/documentation/xcuiautomation)
provides the runner APIs. Earlier research consulted
[pymobiledevice3](https://github.com/doronz88/pymobiledevice3) and
[WebDriverAgent](https://github.com/appium/WebDriverAgent); neither is a runtime
dependency and no code from either is incorporated here. Future source reuse
must comply with upstream licensing and notices.
