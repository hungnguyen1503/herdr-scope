# herdr-app-mod — Scope grouping for the Herdr desktop app

A herdr-app mod that ports the Agent-panel grouping concept of this repo's Go
TUI plugin to the desktop app: workers grouped under their **chief tag**, rows
in a **stable order** (a click, a status change or a fresh output line never
moves a row), and **short worker names** (the bare `<worker>`, not the whole
`<chief>.<repo>.<worker>` label).

The rules are not invented here. They are mirrored from the pure,
test-enforced domain layer of the Go plugin:

|JS in this mod|Go source of truth|
|:---|:---|
|`chiefTag` (`CHIEF_TAG_MAX = 16`)|`internal/domain/tag.go:27` `ChiefTag`|
|`workerName`|`internal/domain/tag.go:15` `WorkerName`|
|`rankSpaceKeys` / `rankLayoutKeys` / `cmpDesc`|`internal/domain/order.go:298` `rankStable`, `:425` `displayOrder`, `:439` `compareActive`|
|one header per tag block, member indent|`internal/domain/sidebar.go:430` `block`, `:585` `newGroup`, `:401` `depth`|

If a rule changes in Go, mirror it here; `internal/domain/stable_test.go`
pins the same behaviors (`TestStableGroupsWorkersUnderTheirChiefTag`,
`TestStableRowsNeverMoveOnAStatusChange`, `TestChiefTagSplitsOnTheFirstDot`).

## Installing

The app loads mods from `~/.config/herdr-app/mods`. Copy or junction this
folder there as `mods/scope`, then add a block to a layout
(`~/.config/herdr-app/layouts/*.json`), e.g. beside the built-in list:

```json
{ "id": "scope", "type": "block", "size": { "weight": 1 }, "min": 90,
  "content": { "mod": "scope" } }
```

`mod.json` uses `"place": "none"`: the mod claims no automatic slot — the
person decides where it hangs, exactly like the built-in `agents` mod.

Verify without a window:

```bash
herdr-app check <this folder>    # prints "check passed"
```

## How it groups

- The label of a row's space is its `space_name`; when the live name lost the
  dot, the ledger's `workspaceLabel` supplies it. `chiefTag` cuts the first
  dot segment (≤ 16 chars): `bunny-chief-1.scope-mod` → group `bunny-chief-1`.
  A label without a dot names no group — the space keeps its own header.
- All spaces with the same tag render under exactly **one** header, whatever
  their worktree/focus/status differences (the bug the Go side fixed first).
- Row name: `workerName` of the label (`scope-mod`). Untagged rows show their
  telling tab title instead.
- Order: family blocks stand at their eldest member's creation number (space
  index), members keep the app's layout order, panes of a split stay together.
  Status, focus and activity enter no key — the Go `stable` mode ported whole.
- `setAgentOrder` publishes the same order to `agent.next/prev/select`, so
  keyboard navigation follows the panel.
- Clicking a row only runs `pane.focus {terminal}`; the mod never assumes it
  saw the click and never re-sorts on focus (the "click jump" fix).

## Data sources

1. **Live** — `agents()` / `spaces()` from `herdr.d.ts`: addresses, statuses,
   focus. Statuses are the OSC 7501 program reports (`working`/`blocked`/
   `done`/`idle`), the same words the app itself shows.
2. **Run ledger** — the Bunny ledger at
   `~\hungquangnguyen\FSP\bunny-chief-of-staff\reports\orchestration-runs\<runId>.json`
   (default path; override with the `ledger` prop below). The mod lists the
   newest 80 `*.json` (newest-first via `cmd /c dir /b /o-d`) and joins each
   live agent by `workers[].terminalId` against the terminal address (remote
   `machine/term_x` addresses are matched on their `term_x` tail), falling
   back to `workspaceLabel == space_name`. A matched run adds the ledger
   chips: `state` (`running`, `awaiting-review`, …), `runtime` (`omp`), and
   `machine` when the address carries none.
3. **Degradation** — the ledger is enrichment only. If the folder is absent,
   unreadable, or a file is the old schema (no `workers[]`), the mod still
   groups and orders by the live labels; rows simply lose their chips.
   An agent with no ledger match is unaffected.

The panel shows no chief block-state anywhere — no badge text, no icon
(engineer's call after seeing `b1 0/42 · b2 0/2 +2`). The header's right-hand
icon slot carries the panel's one sort control instead, in the Agents-panel
icon idiom (`mods/workspace-chrome/clock.svg`, that panel's own sort glyph):
one click orders every group by state (working → blocked → idle → done,
rows move), the next returns all groups to the stable created order. The
per-group `state`/`sort` text chips are gone; the icon is accent-coloured
on a tinted ground while state order is on, dim while created order is.

## View props

Accepted as `content.props` in the layout block (or via `openOverlay`):

|Prop|Type|Default|Effect|
|:---|:---|:---|:---|
|`ledger`|string \| `false` \| `""`|`<home>\hungquangnguyen\FSP\bunny-chief-of-staff\reports\orchestration-runs`|Ledger folder to read. A string sets it (trailing slashes trimmed); `false` or `""` disables the ledger join entirely. The `shared("scope.ledger")` value overrides the default but not the prop.|
|`refresh`|number|`30000`|Milliseconds between ledger reloads; clamped to ≥ 5000. Live agents/spaces re-render on their own — this only re-reads the ledger folder.|

Side effects are guarded for `herdr-app check`: while `checking()` is true the
mod starts no timers, reads no files and runs no processes, so the check loads
it without touching the machine.

## Constraints honored

- Text/JSON only: no binary assets; the spinner/chevron glyphs are reused from
  the app's own `mods/workspace-chrome/`.
- Rendering stays on `herdr.d.ts` primitives (`agents`, `spaces`, `theme`,
  `run`, `setAgentOrder`, `props`, `shared`) plus the `gpui-kit` view the app
  itself requires for any list — the same kit `mods/agents/main.js` uses.
- The Go plugin's code, tests and layer rule are untouched.
