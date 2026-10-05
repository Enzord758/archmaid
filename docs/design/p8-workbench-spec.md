# P8: the Workbench redesign of archmaid-gui (implementation spec)

Source: the owner's Claude Design export, "UI mockups for Archmaid.zip"
(2026-10-05): `ArchmaidWorkbench.dc.html` (the owner's choice),
`ArchmaidConsole.dc.html` and `ArchmaidPlate.dc.html` (the alternatives),
`archmaid-data.js` (the mock data), `tokyo-night.css`, the Industry design
system (`_ds/`) and six screenshots of today's GUI (`uploads/`). The index
page, "archmaid GUI · three directions × eight screens", labels the
directions **1a Console, 1b Workbench, 1c Spec plate**. Workbench is 1b:
"Sidebar with live gauges. Overview as a modular grid. Run is one timeline
where each step carries its own output, and sudo asks inline."

Spec base: `refactor/p0` at 4d3b11d, plus `feat/gui-one-password` (7e2b5f6,
decision F), which P8 must build on. No product code changes in this
document.

**The shape of P8:** a visual and layout redesign of the existing GUI. The
pages (Overview, Run, Logs, Settings), the actions, the dialogs and every
engine interaction stay. Section 2 lists the few real additions on their own.
**No engine API change is needed.** Everything the design shows already
arrives in an Event or can be read without the engine. One small additive
change outside the engine is proposed: totals in `sysstat.Status`.

## 0. Ground rules for whoever builds it

- Content comes from the engine, never from the mockup. The mockup's data is
  partly wrong for today's code (see 1.9). Every step list, count, label and
  password sentence is computed from `QueueStarted`/`NeedsConfirm.Plan`/
  `PlanStep` and the run's Events.
- `gui/internal/ui` stays the only gotk4 package. New logic goes into the
  plain-Go packages (`runview`, `overview`, `logview`, `settings`), where it
  can be tested without a display.
- Every behaviour in section 4 holds unchanged, and the existing harness
  scenarios keep passing, with widget lookups updated, never weakened.

## 1. Workbench, screen by screen

The mockup canvas is 1320×1000. The GUI must also work at **630×372** (half
a 1280×800 screen; today's `TestGUISmallWindow` runs 640×370), so every
region below has a narrow form (1.8).

### 1.1 The shell (every page)

| Region | Mockup | Today | Becomes |
|---|---|---|---|
| Sidebar, 248 px | brand mark + "ARCHMAID"; nav rows Overview, Run, Logs, Settings (Lucide icons); Run carries a badge `02/09` while running, `done` after | `AdwViewSwitcher` (wide) in the header | restyle: the same four `AdwViewStack` pages, chosen from a sidebar |
| Sidebar "System" block | three meters: Disk free `162G` + bar, RAM available `22Gi` + bar, Package cache `0 B` + bar | the Overview's one-line status label ("Free 162G · RAM available 22Gi · Cache 0") | restyle + **addition A1** (bars need totals) |
| Sidebar footer | status dot + "Ready · free 162G" / "Running · 96 lines · free 162G" | the bottom status label (`statusText`) | restyle: the same text, unchanged (`STUB`, `DRY RUN`, `N not shown (in the log)` included) |
| Content header, 56 px | page title ("Overview", or the action's name on Run); in a dry run the tag "DRY RUN · NOTHING CHANGES" and a striped header; Stop (red outline) only while running; window close ✕ | `AdwHeaderBar` with the switcher, Stop always shown (insensitive when idle), ✕ | restyle; Stop keeps its exact behaviour, only hidden instead of insensitive when idle |
| — | — | stub banner "Demo — the stub, changes nothing" | **kept** exactly (top bar, always revealed under `--stub`) |
| — | — | `AdwToastOverlay` | kept |

### 1.2 Overview

| Element | Mockup | Today's feature | Data |
|---|---|---|---|
| Hero cell (2×2 of a 4×3 hairline grid, corner marks) | kicker "Recommended · 7 of 9 steps as root", "Full Cleanup" in 46 px condensed, a description, the numbered step list (2 columns), buttons **Run Full Cleanup** (primary) and **Dry run first** | the "Full Cleanup (Recommended)" menu row | label: `actions.Menu`; steps and root count: **addition A2** (plan preview); Run = `startAction(IDFullCleanup)`; Dry run first = **addition A3** |
| Last run cell | "LAST RUN", action name, outcome tag (`OK`), "Mon 5 Oct 2026, 00:45 · 17 h ago", the run's log files, **Open in Logs** | the "Last Run" row (`overview.ReadLastRun`: title, issues, level icon) | restyle; level → tag tone (ok/warn/error), `Issues` lines kept; file list + Open in Logs: **addition A4** |
| Six action cells | icon, `root` tag, title, one-line description: Clear user cache, Repair broken packages, Delete old downloads, Run topgrade, Disable unused services, Audit AUR packages | the menu rows (same six, same order) | `overview.Entries()`; description and `root` tag are static per entry (**A2**, small); click = `startAction(id)`, insensitive while busy, spinner + "Running"/Busy text on the running one as today |
| — | "View log" is gone | the View log row (`KindLogs`) | removed (the IA change in the design notes; the Logs page covers it) |

### 1.3 Confirm run (the gate)

Mockup: a 480 px right-hand sheet over a scrim. "Full Cleanup", the kicker
"9 steps, in this order", "Proceed with full cleanup?", a numbered rail of
steps with `asks`/`root` tags, two notes ("Seven steps run as root. One
password prompt, up front." / "Step 05 asks again before it wipes the whole
package cache."), **Proceed**, **Dry run instead**, **Cancel**.

Today: `confirmDialog` for `NeedsConfirm{Kind: ConfirmGate}`, with
`Plan` listed as "Steps (n)", `YesLabel` "Proceed", and Cancel.

Becomes: the same `AdwAlertDialog` and the same responses (yes =
`Confirm{Yes:true}`, no or close = `Confirm{Yes:false}`). The extra child is
restyled: the rail with tags from `PlanStep.Confirm` (`asks`) and
`PlanStep.NeedsPriv` (`root`). The notes are computed from the plan: the
root count, and each `Confirm` step by number. The password sentence must
say what decision F does: "One password for this run, asked the first time a
step needs it." The mockup's "up front" is wrong under polkit. "Dry run
instead" is open question Q3.

Every other confirm (orphans with Direct/Cascade lists, the cache wipe,
Repair's reinstall, the AUR gate before topgrade, downloads, services
`NeedsSelection`, the AUR `OutputReport`) gets the same visual treatment and
no behavioural change.

### 1.4 sudo password

Mockup: an inline card inside the running step of the timeline: lock icon,
"sudo needs your password", "Asked by `/usr/bin/yay` (pid 44045) · [sudo]
password for enzord", a password field with a reveal button, Cancel,
**Authenticate**, and "Wrong password · 2 attempts left. After 3, the root
steps are skipped." The step's output shows "Waiting for sudo · asked by
/usr/bin/yay (pid 44045)".

Today: `passwordDialog` (an `AdwAlertDialog` with `AdwPasswordEntryRow`),
"sudo Asks for Your Password", "Asked by …", sudo's prompt clipped to 48
runes, `Error` and attempts left. With one-password: "kept for this run
only".

Becomes: a restyle of the dialog, which stays a dialog (Q1). The card's
content maps 1:1: `NeedsPassword.Requester`, `Prompt` (sanitized, clipped),
`Error`, `AttemptsLeft`, `Kind` (up front or askpass), and one-password's
"kept for this run" line. The synthetic "Waiting for sudo · asked by …" line
in the step block is display-only (`runview`, never logged).

### 1.5 Run (running, failed, dry run)

| Element | Mockup | Today | Data |
|---|---|---|---|
| Run header | kicker "Running · step 2 of 9" / "Finished with 1 failure" / "Dry run · step 6 of 9", meta "Started 17:26:32 · 00:32 elapsed", big mono elapsed counter, on finish tags `8 done` `1 failed` `0 skipped` | status label only | `QueueStarted.Started`, row states, `Finished` (Outcome, Errs, Refused, NotRun); the counter ticks from a 1 s GLib timeout while busy (frontend only) |
| Segment bar | one 4 px segment per step, coloured by state | — | row states |
| Timeline | per step: square marker (check / spinner / circle / x / minus), number, title, `asks`/`root` tags, right-aligned "Done · 1.2 s"; a finished step collapsed to a one-line summary; an open step shows its output block (hairline box; red for a failed step, dashed for a dry run); a rail joins the markers | a checklist `ListBox` of `AdwActionRow`s (`Row.Subtitle`) beside ONE output `ListView` | rows: `runview.Row` (State, Elapsed, Err, Waiting, Faillock); output per step: **addition A5** (the model groups lines by step) |
| Output lines | mono 12.5 px; badges `::`, `==>`, `->`, `WARN`, `ERR`, `OK`, `DRY`; section rules ("SYSTEM UPDATE ······ 17:26:44"); package tables (repo/name, Installed → New); a download progress bar | raw lines with SGR colours as Pango attributes (dank16 palette) | keep SGR; prefix badges are Q6; tables and progress bars are not done (1.10) |
| Failed step actions | **Run topgrade again**, **Open topgrade.log**, **Copy error** | — | **addition A6** |
| Dry run | header stripes + tag, rows "Reported · 0 ms", blocks "would run: …" with `DRY` badge | `DRY RUN` in the status line, rows "Done" | `QueueStarted.DryRun`; "Reported" replaces "Done" only in a dry run (display text) |

States the mockup doesn't draw, which must still be drawn (`RowState`):
**Waiting for authorisation…** (polkit, with the last-attempt warning),
**Not authorised** (with "account locked until …"), **Stopped** (aborted),
**Not run**, **Skipped**, and the engine-stopped state ("archmaid's engine
stopped unexpectedly" in the header kicker, the footer and a toast). Each
gets a marker and tone: waiting = accent with a lock, not authorised =
warn, stopped = danger outline, not run and skipped = muted minus.

### 1.6 Logs

| Element | Mockup | Today | Data |
|---|---|---|---|
| Runs list, 330 px | per run: time in 24 px condensed, date, category tags, "No logs"; "Dry runs" last | `AdwExpanderRow` per run with category rows | `logview.Runs` (unchanged; read only) |
| File switch | segmented control: Topgrade / Cleanup / AUR audit / Orphan removal | the category rows | `AdwToggleGroup` over the run's `Categories` |
| Header | result icon + "Topgrade completed", stamp, the file path | "Pick a log" title | **addition A7** (entry headers parsed) |
| Search | magnifier button | — | **addition A7** |
| Open folder | button | — | **addition A4** (`gtk.FileLauncher`) |
| Body | collapsible sections ("SYSTEM UPDATE 00:45:36", "FLATPAK USER PACKAGES · Nothing to update.") with badged lines | one plain `ListView` of the log's lines | **addition A7**; the text stays `sanitize.Plain` as today |

### 1.7 Settings

| Card | Mockup | Today | Becomes |
|---|---|---|---|
| Runs → Dry run | switch + help, "Changes apply to the next run." | `AdwSwitchRow` | restyle, same `gui.json` |
| Delete old downloads → Downloads folder | path field + **Choose…** + the resolver help text | `AdwEntryRow` + inline error (`CheckDownloads`) | restyle; Choose… = **addition A8**; the chosen path still goes through `CheckDownloads` before saving |
| Topgrade → Config file | `~/.config/topgrade.toml`, "Created with defaults the first time you open it.", **Open in editor** | — (TUI only) | **addition A9** |
| Logs → Log folder | `~/.local/state/archmaid`, "One folder per run. The newest 5 are kept.", **Open folder** | — | **addition A4** |
| Footer | "Saved in …/gui.json" | same | kept |

### 1.8 Small windows (630×372 must keep working)

Breakpoints (`AdwBreakpoint` on the window):

- **`max-width: 760sp`**: the split view collapses. The sidebar becomes an
  overlay opened from a header button, and the status footer moves back to
  a bottom bar under the content. The Overview is one column (hero, last run,
  then the action cells as rows). The Logs page uses an
  `AdwNavigationSplitView` (runs, then the log, with Back). Settings cards
  stack in one column.
- **`max-height: 480sp`**: the meters are hidden (the footer keeps "free
  …"). The Run header compacts to one line (kicker + elapsed) and keeps the
  segment bar. The hero title drops from 46 to 28 px, and the step list on
  the hero is hidden.
- No fixed heights anywhere: the mockup's `grid-template-rows: 224px` and
  `height: 46px` become minimum sizes. Every dialog list keeps its
  `max-content-height` scroller and **non-focusable** labels, so a short
  window never scrolls a dialog's title away (6df69df).
- The default size stays 960×640. At that size the Overview is two columns
  (the hero spans both); four columns from 1200 px.

### 1.9 Mockup data that is wrong for today's code (don't copy it)

- **Step list and order** (`archmaid-data.js` `stepDefs`): 9 steps in the
  old TUI order, temp files by `find … -mtime +2 -exec rm`, `yay -Sc` as root,
  no hyprpm. The GUI runs under polkit, so the real order is: AUR audit,
  topgrade, orphan probe, (orphan removal, inserted), paccache, cache wipe
  (asks), `pacman -Sc` (clean-uninstalled), tmpfiles, journal, integrity,
  `yay -Sc --aur`, hyprpm (if installed). The root steps are **one** polkit
  call (decision E).
- **"One password prompt, up front"**: under polkit with decision F, the
  first prompt comes when topgrade's sudo or the helper call first needs
  it, not up front.
- **"Delete old downloads: Files in ~/Download older than 30 days"**: the
  folder is resolved (Settings, flag, XDG…), and age is ctime.
- **Disk/RAM totals and the topgrade failure** are invented (the design
  notes say so).

### 1.10 What isn't possible or sensible in GTK4/libadwaita, and the alternative

| Mockup | Problem | Alternative |
|---|---|---|
| Corner registration marks (`<i class="corner">` children, CSS pseudo-elements) | GTK CSS has no `::before`/`::after` content or absolute children | a `.blueprint` class whose `background-image` stacks eight small `linear-gradient`s sized and positioned at the corners (GTK CSS supports multiple backgrounds). Use it only on the hero, primary buttons and dialogs (Q11) |
| Hairline modular grid (1 px gaps over a divider-coloured background) | none | `GtkGrid`/`GtkBox` with 1 px spacing on a divider-coloured background, cells on `@window_bg_color` |
| Right-hand confirm sheet with a scrim | no anchored side sheet that is also a modal dialog; `AdwOverlaySplitView`'s overlay isn't modal (no focus trap, no response semantics) | `AdwAlertDialog` (centred; a bottom sheet in narrow windows automatically) (Q2) |
| Inline sudo card in the timeline | a password entry inside a recycled `ListView` row or header loses its text and its focus on scroll; a prompt arriving while another page is shown would go unseen | the dialog, restyled as the card (Q1) |
| Output as rich rows (package tables with columns, progress bars) | output is an untrusted, sanitized byte stream, and curl's progress is `\r`-redrawn text; parsing it into tables is guesswork that breaks on the next yay/topgrade version | SGR colours as today, plus optional prefix badges (Q6) |
| A 50k-line output split into per-step expandable blocks | an `AdwExpanderRow` per step with labels inside would not virtualize | ONE `GtkListView` over a `GtkFlattenListModel` of per-step `GtkStringList`s (a section model): `SetHeaderFactory` draws each step's timeline header (gotk4 v0.4.1 binds both). Collapsing a step swaps its child model for an empty one. The flood budgets stay as they are |
| Barlow / Barlow Condensed | not installed here (only JetBrains Mono is) | CSS `font-family: "Barlow Condensed", "Adwaita Sans", sans-serif`; Q4 |
| Lucide icons | not in the icon theme | Adwaita symbolic icons in v1 (Q5) |
| Square corners everywhere | libadwaita rounds buttons, dialogs and boxed lists | app CSS `border-radius: 0` on our classes (not global overrides of libadwaita internals beyond `dialog.floating sheet` and `.boxed-list`) |
| Striped dry-run header | none | `repeating-linear-gradient` in GTK CSS on the header's class |

## 2. The minor additions (new functions, data and controls)

| # | Addition | Where | Engine/backend work |
|---|---|---|---|
| A1 | Meters with bars: disk free of total, RAM available of total, package cache size | sidebar | **small backend**: `internal/sysstat.Status` gains byte fields (`FreeBytes`, `DiskBytes`, `RAMAvailBytes`, `RAMBytes`, `CacheBytes`), additive. `StatusUpdated` carries them with no engine API change. Watch `BenchmarkSystemStatus`: bench-compare fails on any rise in allocs (68). Cache bar = cache / disk total (or no bar, only the size) |
| A2 | Plan preview on the Overview (root count, numbered steps) and per-action description and `root` tag | Overview | **GUI-only**: `overview.Plan(env, id)` builds the action's queue with `actions.ByID(id).Steps(env)` (building runs nothing; the HANDOVER "Full Cleanup's order" entry) under the same polkit `Env` as `realdriver.Options()`. Static descriptions in `overview.Entry` |
| A3 | **Dry run first**: one Start as a dry run, without changing the saved setting | hero card | **GUI-only**: `startAction` takes a one-off `DryRun` that goes into that Driver's `engine.Options` (`settings.Apply` + override). The Settings switch is untouched |
| A4 | **Open in Logs** (last run → its run folder), **Open folder** (Logs page and Settings) | Overview, Logs, Settings | **GUI-only**: the run folder from the record's `Started` minute (the folder's stamp) with a fallback to the newest real run; folders open with `gtk.FileLauncher`. Optional, small backend: a `run_dir` field in `RunRecord` (additive, the widget contract allows it) |
| A5 | Output grouped per step, with a one-line summary per finished step | Run | **GUI-only** (`runview`): the model tags each line with its step (`OutputChunk.Index` already says which); the 10k-line cap stays per run and drops from the oldest step first. The summary is the failed step's `Err`, else its last non-empty line, else its `LogLabel` |
| A6 | Failed-step actions: **Run <action> again** (offered when the failed step is topgrade: it starts Run topgrade), **Open <category>.log**, **Copy error** | Run | **GUI-only**: `startAction`, the Logs page at the run and `PlanStep.Category`, `gdk.Clipboard` with the step's `Err` and its last lines (already sanitized). Not while busy |
| A7 | The log viewer: entry headers parsed (`[ts] KIND: label`) into a result line, topgrade's "―― HH:MM:SS - name ――" separators into collapsible sections, and a search bar that filters lines | Logs | **GUI-only** (`logview`, read only as today) |
| A8 | **Choose…** for the downloads folder | Settings | **GUI-only**: `gtk.FileDialog.SelectFolder`; the result is checked by `settings.CheckDownloads` like a typed path |
| A9 | **Edit topgrade config**: Settings → Topgrade → Open in editor | Settings | **small backend**: move `ensureTopgradeConfig`, `configPath` and `defaultTopgradeConfig` out of `internal/tui/tasks.go` into `internal/frontend` (the TUI keeps its behaviour, `coverage_test`'s `editConfig` checks stay). GUI: create with defaults if missing, then `gtk.FileLauncher` on the file (the desktop's default app for it; `SetAlwaysAsk` when none is set), with **Show in folder** (`OpenContainingFolder`) as the fallback. No privilege, no `$EDITOR` (it needs a terminal). An embedded editor (`GtkSourceView`) would need a new cgo binding and GTK dependency, for one file: not worth it (Q7) |
| A10 | Run badge in the sidebar (`02/09`, `done`) and the big elapsed counter | sidebar, Run | **GUI-only** |

Nothing else is new: no new actions, no new engine Commands, no new state
files.

## 3. Widgets and CSS

| Component | Widget |
|---|---|
| Window | `AdwApplicationWindow` → `AdwToastOverlay` → `AdwOverlaySplitView` (sidebar start, `min-sidebar-width` 220, `max-sidebar-width` 248), collapsed by the 760sp breakpoint, with a `sidebar-show-symbolic` toggle in the content header when collapsed. **Not `AdwNavigationSplitView`** for the shell (the design notes suggest it): collapsed, it turns the sidebar into a page with Back buttons, which suits drill-down, not four peer tabs. It is right for the Logs page |
| Sidebar | `GtkBox` (vertical): brand row; a `GtkListBox.navigation-sidebar` with four rows (icon, label, a suffix `GtkLabel` for the badge) that sets the `AdwViewStack`'s visible child; a spacer; the meters (`GtkLevelBar` restyled to a 2 px bar, or a `GtkProgressBar` with CSS); the footer label. `AdwViewSwitcherSidebar` (libadwaita 1.9, bound) only takes integer badges, and the Run badge is text |
| Content | `AdwToolbarView`: top bar = `AdwHeaderBar` (title label, dry-run tag, Stop, sidebar toggle), the stub `AdwBanner`, and in narrow mode a bottom bar with the status label; content = the existing `AdwViewStack` with the page names `overview`, `run`, `logs`, `settings` unchanged (the harness uses them) |
| Overview | `GtkScrolledWindow` → `AdwClamp` (off at wide sizes) → `GtkBox`: hero `GtkBox.card.blueprint`, last-run box, and a `GtkFlowBox` (min 1, max 3 children per line; homogeneous) of action cells (`GtkButton` with a vertical box child, so they are keyboard-activatable). The breakpoints switch the `GtkBox` orientation and `FlowBox` `max-children-per-line` through `AdwBreakpoint` setters |
| Run header | `GtkBox` of labels; the segment bar is a homogeneous `GtkBox` of 4 px `GtkBox`es with a state class each |
| Timeline + output | one `GtkListView` (`NoSelection` over a `GtkFlattenListModel` of per-step `GtkStringList`s); `SetHeaderFactory` → the step header widget (marker, number, title, tags, sub text, the summary or the failed-step buttons). The rail is a `border-left` on the line rows' class and the headers' marker column. Follow-the-bottom works as today (wheel up stops, bottom edge resumes), and fixes `TODO(P6b-4)`: a scrollbar drag or a key also stops following |
| Dialogs | `AdwAlertDialog` throughout, as today. CSS classes for the card look; extra children as listed in 1.3/1.4 |
| Logs | `AdwNavigationSplitView` (sidebar: runs `GtkListBox`; content: `AdwToolbarView` with `AdwToggleGroup` for the categories, a `GtkSearchBar`, Open folder; the body is one `GtkListView` with sections, as on the Run page) |
| Settings | `GtkFlowBox` (max 2 per line) of `GtkBox.card.blueprint` cells, holding the existing `AdwSwitchRow`/`AdwEntryRow` in a `boxed-list` (keeps their accessibility and the harness's handles) |

**CSS**: one `gui/internal/ui/style.css`, embedded with `go:embed` and
loaded by a `gtk.CssProvider` at `APPLICATION` priority in `Main`. Tokyo
Night as `@define-color` tokens (bg `#1a1b26`, surface `#1f2335`, sidebar
`#16161e`, text `#c0caf5`, muted `#737aa2`, accent `#7aa2f7`, divider
`#3b4261`, success `#9ece6a`, warn `#e0af68`, danger `#f7768e`). The
libadwaita named colours (`window_bg_color`, `accent_bg_color`, …) are
redefined from them, and `AdwStyleManager` is forced dark. Fonts: headings
`"Barlow Condensed"` with fallbacks, body the system font, mono `"JetBrains
Mono", monospace`. Class names are prefixed `am-` so nothing collides with
libadwaita's. GTK CSS limits to respect: no `::before`/`::after`, no CSS
grid, no `color-mix()` (precompute the tints as tokens), no `position`.
Transitions are fine but nothing may animate per output line.

## 4. What stays exactly as it is

| Behaviour | Where the design touches it | Rule |
|---|---|---|
| **One password per run** (decision F: the run's first password is kept, su-checked, used for later sudo and polkit prompts, wiped at Finished/Close/KillNow) | 1.3 note text, 1.4 card | Visual only. The dialog still reads the entry as C bytes (`secret.go`), wipes it, sends `SubmitPassword`, and `Cancel` sends `CancelPrompt`. The "kept for this run" line stays. The confirm's sentence must describe F, not "up front" |
| **The polkit flow** (one `full-cleanup` dialog, faillock read first, `Waiting for authorisation…`, `Not authorised`, the lock time, the last-attempt warning, pkttyagent with its fallback note) | 1.5 states | All drawn from `Row.Waiting`, `Row.Faillock`, `RowAuthRefused`, as today. The polkit dialog itself isn't ours to style |
| **Stop** | header button | Visible only while busy, sends `Abort` exactly as now. During a polkit wait it is the same Stop |
| **Close / Stop and Quit** | ✕ | `closeRequest` unchanged: mid-run asks "Stop the run and quit?" (Keep Running default; act on `response`, never `closed`); while polkit's dialog waits, ✕ is Stop and Quit at once; the window hides and the Driver closes off the main thread |
| **Confirmations** | 1.3 restyle, every other confirm | Same `AdwAlertDialog`s, the same `Window.answer` path (a stale ID never reaches the engine), the same defaults (no is the default for a destructive step, yes red), "Skip" vs "Cancel" from `Optional`, the item lists scroll and aren't focusable |
| **Ctrl+C / signals** | — | SIGINT/SIGTERM/SIGHUP = Stop and Quit, a second signal kills at once (`signals.go`): untouched. Ctrl+C inside the window stays GTK's copy (in the log viewer, the output, Copy error); never bind it to Stop |
| **Small window** | 1.8 | 630×372 and 640×370 must show the page title, Stop, the run's current step and every dialog's title and buttons |
| **Main-thread rule and the pump** | A5 | Widgets only from `runview.Pump`'s posts. Output flush p95 ≤ 4 ms, 50k-line floods, the 10k cap with `Dropped` counted: the existing budgets apply to the sectioned list |
| **Stub** | shell | `--stub` only; banner, "(stub)" title, `STUB` in the footer, own app id |
| **Read-only pages** | Logs, Overview | Never create, rotate or delete anything in the state dir; "Open folder" only launches |

## 5. The DMS widget (`packaging/dms/archmaid`)

What it should take from Workbench: the **information layout**, not the
chrome. The bar is styled by DMS (matugen colours, rounded pills), and square
blueprint frames would clash with every other widget.

- Popout sections in Workbench's order and wording, each with a small
  uppercase kicker: **LAST RUN** (action, an outcome tag
  `OK`/`FAILED`/`INCOMPLETE`/`ABORTED`/`CANCELLED`, "Mon 5 Oct, 00:45 · 17 h
  ago"), **UPDATES** (count, checked when), **AUR** (packages, the high
  findings as rows).
- Numbers in a mono font (the GUI's meters style). Tags tinted with DMS
  `Theme.primary`/`Theme.warning`/`Theme.error`. No new data: still only the
  three state files.
- Buttons: **Run Full Cleanup** (primary) and **Log folder**, as now. "Dry run
  first" would need a launch flag for `archmaid-gui`; leave it out.
- Tests: `TestStateJS` (`qmltestrunner`; it isn't in CI, so run it locally)
  and the manual scratch-quickshell load from `docs/TESTING.md`.

## 6. Implementation plan

Three devs work in parallel. No two of them touch the same file.

- **Dev 4** (lead, `gui/internal/ui/**`, `gui/scripts/**`).
- **Dev B** (plain Go: `gui/internal/{runview,overview,logview,settings}`,
  `internal/sysstat`, `internal/frontend`, plus the TUI's
  `internal/tui/tasks.go` for A9 only).
- **Dev C** (`packaging/dms/**`).
- **Dev 5** checks screenshots in the VM.

Each commit passes `scripts/check.sh` and `make gui-test` (`make e2e` too
when the root module changes).

| # | Dev | Commit | Depends on | Tests |
|---|---|---|---|---|
| 1 | 4 | `gui: screenshots from the headless harness` (`ARCHMAID_GUI_SHOTS=<dir>` writes a PNG per scenario through `gtk.WidgetPaintable`; off by default) | — | the harness; the baseline shots of today's GUI for Dev 5 |
| 2 | 4 | `gui: Tokyo Night stylesheet and the am- classes` (embedded CSS, forced dark, no layout change) | — | every existing scenario; shots |
| 3 | 4 | `gui: sidebar shell with breakpoints` (OverlaySplitView, nav list, footer, Stop visible only while busy, narrow bottom status bar) | 2 | all scenarios; `TestGUISmallWindow` extended to 630×372 (sidebar collapsed, title, Stop and dialog title in view) |
| 4 | B | `sysstat: byte totals for the meters` (A1) | — | fixtures; `BenchmarkSystemStatus` allocs unchanged; bench-compare |
| 5 | B | `overview: plan preview, descriptions, root tags` (A2; drop View log) | — | plain Go, under polkit and sudo Env; nothing executed (testenv traps) |
| 6 | B | `runview: output grouped by step, summaries, run header data` (A5, A10) | — | replay `stub.FullCleanup`, floods across the cap (oldest step drops first), `BenchmarkApplyFlush*` within budget |
| 7 | B | `logview: entry headers, topgrade sections, search filter` (A7) | — | fixture logs, including raw escapes (stay plain) |
| 8 | B | `frontend: the topgrade config defaults, shared by the TUI and GUI` (A9 backend) | — | the TUI's `editConfig` tests unchanged; frontend tests |
| 9 | 4 | `gui: meters in the sidebar` | 3, 4 | shots; the `idle` scenario's RAM and first frame within budget |
| 10 | 4 | `gui: Overview as the modular grid; Dry run first` (A3) | 3, 5 | `fullcleanup` from the hero; a new `dry-first` scenario (dry run on the next Driver only, `gui.json` unchanged) |
| 11 | 4 | `gui: Run timeline on a sectioned list view` | 3, 6 | every run scenario; `flood*` budgets with `ARCHMAID_GUI_PERF=1`; follow-the-bottom incl. a scrollbar drag |
| 12 | 4 | `gui: failed-step actions` (A6) | 11 | a failing topgrade shim: Run again starts topgrade, Copy error fills the clipboard, the Open log page shows the run |
| 13 | 4 | `gui: restyled dialogs, the gate's plan rail` | 2, 5 | `fullcleanup`, `uncached`, `wrong-password`, `askpass`, `real-*`, `TestGUIOnePassword`, `TestGUISmallWindow` (dialog title in view) |
| 14 | 4 | `gui: Logs page with categories, sections and search; Open folder` (A4, A7) | 3, 7 | `logs` scenario (the state dir unchanged after); a launcher seam so tests never open a real app |
| 15 | 4 | `gui: Settings cards; Choose…; Open in editor` (A8, A9) | 3, 8 | `settings` scenario; the folder chooser and launcher behind seams (no portal in headless) |
| 16 | C | `dms: popout in Workbench's sections` | — | `TestStateJS` locally; scratch quickshell load |
| 17 | 5 | (no commit) the VM screenshot comparison against the mockup, per screen, after 10, 11, 13, 14, 15; differences go to Dev 4 as a list | each | — |

Dev B starts with 4–8 while Dev 4 does 1–3, so the UI commits find their
data ready. Commits 11 and 13 are the risky ones (the pump and the password
path): give each its own review.

## 7. Open questions for the owner

1. **The password: inline card or dialog?** The mockup asks inline. *Recommend
   the dialog, styled as the card.* An inline entry can be scrolled away or
   missed on another page, and the dialog path is the reviewed, wiped one.
2. **The confirm: side sheet or dialog?** *Recommend the dialog* (centred; a
   bottom sheet when narrow). A side sheet would need a hand-made modal.
3. **"Dry run instead" on the confirm?** Declining the gate is recorded as a
   `cancelled` run, so `last-run.json` and the widget would show a cancelled
   run before every dry run. *Recommend dropping it and keeping "Dry run
   first" on the Overview card.*
4. **Fonts**: Barlow isn't installed. *Recommend the CSS fallback to the system
   font now, and an optdepend on an AUR Barlow package if you want the
   condensed headings* (check the package name first). Bundling fonts would
   need Pango font loading: not worth it.
5. **Icons**: *recommend Adwaita symbolic icons* (already used, recolour with
   the theme). Lucide SVGs could be shipped as resources later.
6. **Output badges**: *recommend only prefix badges* for lines that start with
   `::`, `==>`, `->`, `error:`, `warning:`, over the SGR colours; no package
   tables or progress bars (fragile parsing of untrusted output).
7. **Edit topgrade config**: *recommend the desktop's default app through
   `gtk.FileLauncher`, after creating the defaults*, with "Show in folder" as
   the fallback. Not `$EDITOR` (no terminal), not an embedded editor. Should
   it also open in a dry run? *Recommend yes*: editing a config isn't a run
   (the TUI only logs a DRYRUN line there).
8. **Output colours**: today the output's ANSI colours follow DMS's dank16
   palette. *Recommend keeping that* (it matches your terminal), with Tokyo
   Night for everything else. The alternative is Tokyo Night everywhere.
9. **Meters**: *recommend yes* (needs commit 4; a small, additive sysstat
   change).
10. **Corner marks**: *recommend them only on the hero, the primary buttons
    and the dialogs*: on every cell they become noise at small sizes.
11. **The window's default size**: *recommend keeping 960×640* (two-column
    Overview). 1320×1000 doesn't fit a split screen.
12. **A new run starts with an empty Run page?** (Dev 5's VM test: run 2
    opened under run 1's ~1949 lines. `runview.Model` resets the rows at
    `QueueStarted` but never the output.) *Recommend yes*: clear the output
    when the next action starts (`startAction`, so a discovery's notice
    doesn't appear under the old output either). The previous run stays
    reachable through Logs. One caveat to accept: Logs hold only failures
    and the `AlwaysLog` steps (topgrade, the removals, the audit), so the
    full output of a step that succeeded quietly (`paccache -r`, the
    integrity check) is gone once the next run starts. With the per-step
    timeline (A5) this is the natural behaviour anyway, and it is a fix to
    land before P8 too (`runview`, one commit with a harness check).
13. **"Run topgrade" starts without a confirm.** The Workbench mockup shows a
    confirm only for Full Cleanup ("Confirm run · after clicking Full
    Cleanup"); its other action cells start on a click, as today. In the
    TUI, an uncached sudo password stands in for the confirm before anything
    runs. In the GUI there is no up-front password: the first prompt comes
    only when topgrade's first `sudo` asks, after the AUR audit, and after
    topgrade has already run its user-level steps (flatpak, git pulls,
    oh-my-zsh), which change things without a confirm. *Recommend giving Run
    topgrade a gate* ("Run a system update with topgrade?",
    `actions.Action.Gate`, which the engine already supports, so no engine
    change). It applies to the TUI too (shown when the sudo ticket is cached;
    otherwise the password prompt is the gate, as for Full Cleanup); the
    `topgrade_*` transcript goldens change on purpose. The other actions
    already confirm before they change anything (Clear user cache, Repair,
    downloads and services ask with their lists; the AUR audit is read-only).
