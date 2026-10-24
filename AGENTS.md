# Homeward contributor instructions

These instructions apply to this repository.

Homeward is an experimental macOS CLI for iPhone Home Screen organization. The
Python host uses the standard library; the Swift runner uses Apple XCTest.
Maintain the no-third-party-runtime-libraries requirement. Optional external AI
CLIs and Apple developer tools are separately installed tools.

## Working roles

The user has assigned the parent agent planning and review responsibilities.
Delegate implementation to Astra or another implementation model; the parent
reviews the resulting changes. This is a division of roles at the parent level,
not a requirement for implementation agents to recursively delegate their work.
An assigned implementation agent may perform its own edits and validation.

## Product boundaries

- The interview, draft planner, consent handling, and read-only terminal preview
  exist. A full-screen TUI and integrated Apply flow do not yet exist.
- The intended experience selects relevant apps, defaults to no folders, respects
  AI refusal, and requires preview before explicit Apply confirmation.
- Top/bottom icon alignment and manual widget reserved space without Developer
  Mode are future requirements. Actual geometry support is unverified; do not
  present placeholders or reserved space as implemented.
- Configurator placement plus XCTest cleanup has worked experimentally. Preserve
  the distinction between that manual workflow and a supported integrated Apply.
- Device operations, publishing, and external AI calls need task authorization;
  documentation/source work alone does not authorize them.

## Validation and privacy

Run `python3 -m unittest discover -s tests -v` for host changes. Do not use a live
phone or external AI provider merely to validate documentation/setup changes.
Keep personal layouts, screenshots, evidence, research notes, feedback drafts,
credentials, signing identities, and build outputs out of source control.
Use synthetic examples and placeholders for device/team identifiers. Preserve
local evidence files when preparing an import; ignore them rather than deleting
them. Never claim exact rollback or general widget preservation from a saved
Configurator layout.
