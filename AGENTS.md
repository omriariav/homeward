# Homeward contributor instructions

These instructions apply to this repository.

Homeward is an experimental Go terminal organizer for iPhone Home Screens. The
Go host uses only the standard library; the Swift runner uses Apple XCTest. The
original standard-library Python prototype remains a reference, not a runtime
dependency of the Go executable. Maintain the no-third-party-runtime-libraries
requirement. Optional AI CLIs and Apple developer tools are separate installations.

## Working roles

The user has assigned the parent agent planning and review responsibilities.
Delegate implementation to Astra or another implementation model; the parent
reviews the resulting changes. This is a division of roles at the parent level,
not a requirement for implementation agents to recursively delegate their work.
An assigned implementation agent may perform its own edits and validation.

## Product boundaries

- The Go app provides an interactive numbered terminal menu, not a full-screen TUI.
  It includes local/AI interview and planning, paged review, revision, synthetic
  Apply and a guarded live executor. The Python prototype remains available.
- Default to a relevant shortlist, no folders, independent consent for each AI
  request, and exact reviewed-plan confirmation. Edits invalidate confirmation.
- Bottom alignment, preserved free geometry and manual widget reservation are
  stored preferences, not executable placement features. Block unsupported Apply;
  never silently top-fill a bottom-aligned request.
- Standard-row bulk placement can remove widgets/reflow spaces and needs explicit
  acknowledgement. cfgutil readback does not validate widget/free-position state.
- The Go cfgutil + direct XCTest path is fake-tested, not physically validated.
  Prior manual workflow evidence is not proof of the new integration. Require
  exact staging/final readback, fresh-state checks, and honest partial failures.
- Preserve unresolved shortcuts (especially distinct '/' occurrences), block
  ambiguous automation, and never uninstall apps. No-Developer-Mode cfgutil-only
  cleanup is unsupported.
- Device operations, publishing, and external AI calls need task authorization;
  documentation/source work alone does not authorize them.

## Validation and privacy

Run `go test ./...` and `go vet ./...` for Go changes; use `go test -race ./...`
for executor/concurrency changes. Run `python3 -m unittest discover -s tests -v`
when changing or checking the Python reference. Do not use a live phone or external
AI provider merely to validate implementation, documentation or setup changes.
Keep personal layouts, screenshots, evidence, research notes, feedback drafts,
credentials, signing identities, and build outputs out of source control.
Use synthetic examples and placeholders for device/team identifiers. Preserve
local evidence files when preparing an import; ignore them rather than deleting
them. Never claim exact rollback or general widget preservation from a saved
Configurator layout.
