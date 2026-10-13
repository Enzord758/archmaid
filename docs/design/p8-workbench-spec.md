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
change outside the engine is proposed: totals in `sysstat.Status`. The one
engine change in this document comes from one-password, not from the
design: `NeedsPassword.Polkit` and `NeedsPassword.Action`, both additive
(1.4). They are needed so the dialog's kind is never parsed out of the
Requester text.

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
root count, each `Confirm` step by number, and the AUR gate. The exact
strings, before and after one-password, are in 1.11. The mockup's "up
front" is wrong under polkit. "Dry run instead" is dropped (decision Q3).

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
`KeptNote`. The synthetic "Waiting for sudo · asked by …" line in the step
block is display-only (`runview`, never logged).

**Why a dialog, and what an inline card would have to rebuild.** The
password path is the most-reviewed code in the GUI, and its guarantees are
tied to `AdwAlertDialog`'s mechanics:

- **Act on `response`, never on `closed`** (bc5646b). A clicked button closes
  the dialog first and emits `response` after it. Wiping the entry at
  `closed` once sent an empty password for every click on Authenticate. The
  entry is now wiped at `closed` only from an idle, after `respond` has read
  it. Enter in the entry runs `respond("ok")` and then `ForceClose`.
- **The entry is read as C bytes** (`secret.go`), wiped, and the bytes zeroed
  after the `Secret` is built.
- **Every answer goes through `Window.answer`**, which sends only when
  `Model.Answer` claims the prompt ID. An engine withdrawal (`PromptClosed`,
  the engine stopping) is `ForceClose` with no response: the entry is wiped
  and nothing is sent.
- **The harness clicks the real buttons** (`findButton`), which is how both
  ordering bugs were caught.

An inline card in the timeline has no response signal. It would need its
own Authenticate/Cancel/Enter handling, its own wipe on withdrawal, its own
"stale ID" check, and a wipe when the row is recycled or scrolled away (a
`ListView` header is rebuilt on scroll, and a rebuilt header loses the
typed text). It would also need a way to reach a user who is on another
page when the prompt comes. That is a second implementation of the
password path, to be reviewed again, for a look the dialog can carry.

**With one-password** the dialog still appears exactly once per run in the
normal case (the first prompt, from topgrade's sudo or from polkit's agent),
with `KeptNote` ("archmaid keeps it in memory for this run only, so the run
doesn't ask again."). Later prompts are answered from the kept password and
show nothing. A kept password that su or polkit refuses is dropped, and the
next prompt asks the user again through the same dialog. The dialog says
what the kept password is for (1.11): any other program that asks gets this
dialog, with its own name in it.

**The dialog's kind comes from data, never from the Requester text.** Under
one-password, polkit's agent prompt arrives as `AskpassRequest{Polkit:
true}`. Today's `NeedsPassword` doesn't carry that flag, and the agent's
`Requester` reads "polkit, for org.archmaid.helper.full-cleanup". A GUI
that picked "polkit" by matching that string could be fooled by a sudo
caller whose binary path contains it. The required change is additive (E0
allows it): `NeedsPassword` gains `Polkit bool` and `Action string` (the
polkit action id, from the agent, which already checks it is an
`org.archmaid.helper.*` action), and the Session copies them from the
`AskpassRequest`. The dialog then picks its title, body and caller line
from `Polkit`:

- **sudo:** the caller line is the verified caller (the binary and pid).
- **polkit:** its own title, and the action id shown verbatim.

Dev 4 is fixing today's title in the GUI, and P8 builds on that.

**The caller line is a hard rule.** "Asked by {Requester}" (the binary and
pid, or "polkit, for {Action}") is the user's only check that the prompt is
genuine. The HANDOVER askpass landmine explains that a build script running
sudo itself shows up as `/usr/bin/bash`, not topgrade or yay. So:

- The caller line is its own `GtkLabel` in the dialog body (the extra
  child), with `use-markup` off. It is never interpolated into the heading
  or into markup.
- It wraps (`wrap = true`, `wrap-mode = word-char`) and never ellipsizes
  (`ellipsize = none`), at every window size down to 630×372 and in the
  floating-dialog form. A long path takes more lines; it is never cut.
- It is never dimmed, never smaller than the body text and never
  low-contrast: no `dim-label`, no `caption`, no opacity. Our CSS gives it
  only a rule that makes it as prominent as the body (3.1).
- The harness checks it: at 630×372 the caller label is visible in full
  (not ellipsized, no `dim-label`) for a requester path of 120 characters.

**Accepted residual.** Anything that can write `~/.config/gtk-4.0/` (the
user's own stylesheet, which GTK loads above ours) can restyle or hide the
caller line. That takes code already running as the user, which can attack
the session in easier ways. It is no worse than the rest of the desktop,
and it is accepted.

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
| A 50k-line output split into per-step expandable blocks | an `AdwExpanderRow` per step with labels inside would not virtualize | ONE `GtkListView` over a `GtkFlattenListModel` of per-step `GtkStringList`s (a section model); `SetHeaderFactory` draws each step's timeline header (gotk4 v0.4.1 binds both). A section is never empty: a pending or collapsed step's child model holds one summary item, because an empty section draws no header. The throughput design and its guards are in 3.2 |
| Barlow / Barlow Condensed | not installed here (only JetBrains Mono is) | CSS `font-family: "Barlow Condensed", "Adwaita Sans", sans-serif`; Q4 |
| Lucide icons | not in the icon theme | Adwaita symbolic icons in v1 (Q5) |
| Square corners everywhere | libadwaita rounds buttons, dialogs and boxed lists | app CSS `border-radius: 0` on our classes (not global overrides of libadwaita internals beyond `dialog.floating sheet` and `.boxed-list`) |
| Striped dry-run header | none | `repeating-linear-gradient` in GTK CSS on the header's class |

### 1.11 User-facing strings: mockup → what ships

The mockup's copy describes the old TUI flow. Every string below is
replaced. Counts and step numbers are computed from the plan
(`QueueStarted`/`NeedsConfirm.Plan`, after `StepInserted`), never fixed. The
**F** column is the text once `feat/gui-one-password` is merged; a dash
means the same as today.

| Where | Mockup | Today (4d3b11d+, polkit, one combined call) | F (one-password) |
|---|---|---|---|
| Hero kicker | "Recommended · 7 of 9 steps as root" | "Recommended · {N} steps · {M} root steps in one authorisation" ({M} counts the root tags, not topgrade's sudo) | — |
| Hero description | "…then trims the package cache, removes orphans, clears old temp files, vacuums the journal and checks package integrity." | "Audits your AUR packages, updates the system with topgrade, then removes orphans (you confirm the list), trims the package cache, clears old temp files, vacuums the journal, checks package integrity and cleans the AUR build cache." | — |
| Confirm kicker | "9 steps, in this order" | "{N} steps, in this order" | — |
| Confirm note (password) | "Seven steps run as root. One password prompt, up front." | "{M} steps run as root, together, through one authorisation; topgrade gets root through its own sudo. You type your password once, the first time a step needs it; archmaid keeps it for this run only (without a kept password: "If topgrade's sudo needs your password, archmaid asks for it in its own dialog.")." | "{M} steps run as root, each through sudo; topgrade gets root through its own sudo. You type your password once, the first time a step needs it; archmaid keeps it for this run only." ({M} counts the rail's `root` tags; topgrade, which runs as the user and calls sudo itself, is tagged `sudo`: task #101, after the VM's "6 steps" beside 7 root tags) |
| Confirm note (asks) | "Step 05 asks again before it wipes the whole package cache." | "Step {n} asks before it deletes the entire package cache." For an inserted orphan removal: "Orphans are listed for you to confirm before anything is removed." | — |
| Confirm note (AUR gate) | — | "If an AUR package changed maintainer or gained a co-maintainer, you're asked before the update." | — |
| Password heading (askpass) | "sudo needs your password" | "sudo Asks for Your Password" (keep) | by `NeedsPassword.Polkit` (data, 1.4), never by the Requester text: sudo → "sudo Asks for Your Password"; polkit → "Authorise archmaid's Root Steps" |
| Caller line (own label, 1.4 rule) | "Asked by /usr/bin/yay (pid 44045) · [sudo] password for enzord" | "Asked by {Requester}." on its own line, then sudo's prompt, sanitized, ≤ 48 runes | sudo: the same; polkit: "Asked by polkit for {Action}" with the action id verbatim (e.g. `org.archmaid.helper.full-cleanup`) |
| Kept-password note | — | — | "archmaid keeps this password in memory for this run only. It answers topgrade's and yay's sudo and archmaid's own polkit prompts. Any other program that asks shows this dialog with its name." (replaces today's `KeptNote` on the follow-ups branch) |
| Password error | "Wrong password · 2 attempts left. After 3, the root steps are skipped." | the engine's `Error` verbatim ("Incorrect password — 2 attempts left", or a lockout notice) and nothing invented after it. The mockup's "after 3, the root steps are skipped" is not what an askpass refusal does (it fails the asking step) | — |
| Up-front password heading | — | the gate text (`NeedsPassword.Gate`) or "Password Required" | — |
| Waiting row | — (not drawn) | "Waiting for authorisation…" / "… · one more failure locks your account" (`Row.Subtitle`) | — |
| Refused row | — | "Not authorised" / "Not authorised · account locked until HH:MM" | — |
| Dry-run tag | "DRY RUN · NOTHING CHANGES" | "DRY RUN · NOTHING ON THE SYSTEM CHANGES" (a dry run still asks the AUR and writes `dryrun/` logs) | — |
| Settings dry-run help | "…Logs everything to dryrun/ and skips sudo." | "…Logs everything to dryrun/ and asks for no password or authorisation." | — |
| Action: Delete old downloads | "Files in ~/Download older than 30 days" | "Files and folders not changed in 30 days, from your downloads folder" | — |
| Action: Run topgrade | "System update, output streamed live" | "Audits your AUR packages, then updates the system with topgrade" | — |
| Run kicker, finished | "Finished with 1 failure" | `runview.FinishedText` ("Finished: failed (1 step)", "Finished: incomplete (n steps not authorised)", …) | — |
| Failed-step buttons | "Run topgrade again" / "Open topgrade.log" / "Copy error" | "Run topgrade" / "Open topgrade.log" / "Copy error" (A6: it starts the action, not the step) | — |

## 2. The minor additions (new functions, data and controls)

| # | Addition | Where | Engine/backend work |
|---|---|---|---|
| A1 | Meters with bars: disk free of total, RAM available of total, package cache size | sidebar | **small backend**: `internal/sysstat.Status` gains byte fields (`FreeBytes`, `DiskBytes`, `RAMAvailBytes`, `RAMBytes`, `CacheBytes`), additive. `StatusUpdated` carries them with no engine API change. Watch `BenchmarkSystemStatusCache100`: bench-compare fails on any rise in allocs (637). Cache bar = cache / disk total (or no bar, only the size) |
| A2 | Plan preview on the Overview (root count, numbered steps) and per-action description and `root` tag | Overview | **GUI-only**: `overview.Plan(env, id)` builds the action's queue with `actions.ByID(id).Steps(env)` (building runs nothing; the HANDOVER "Full Cleanup's order" entry) under the same polkit `Env` as `realdriver.Options()`. Static descriptions in `overview.Entry` |
| A3 | **Dry run first**: one Start as a dry run, without changing the saved setting | hero card | **GUI-only**: `startAction` takes a one-off `DryRun` that goes into that Driver's `engine.Options` (`settings.Apply` + override). The Settings switch is untouched |
| A4 | **Open in Logs** (last run → its run folder), **Open folder** (Logs page and Settings) | Overview, Logs, Settings | **GUI-only**: the run folder from the record's `Started` minute (the folder's stamp) with a fallback to the newest real run; folders open with `gtk.FileLauncher`. Optional, small backend: a `run_dir` field in `RunRecord` (additive, the widget contract allows it) |
| A5 | Output grouped per step, with a one-line summary per finished step | Run | **GUI-only** (`runview`): the model tags each line with its step (`OutputChunk.Index` already says which); the 10k-line cap stays per run and drops from the oldest step first. The summary is the failed step's `Err`, else its last non-empty line, else its `LogLabel` |
| A6 | Failed-step actions: **Run <action> again** (offered when the failed step is topgrade: it starts Run topgrade), **Open <category>.log**, **Copy error** | Run | **Pure frontend, no engine command.** "Run topgrade" is `startAction(actions.IDTopgrade)`: a fresh Driver and an ordinary `engine.Start`, with its AUR audit, gate and password exactly as a click on the action. Only when the failed step maps to a menu action of its own (topgrade → Run topgrade); otherwise the button reads "Run {action} again" and starts the same action. Retrying one step inside a finished run doesn't exist in the engine, and P8 doesn't add it. "Copy error" puts the step's `Err` and its last 20 output lines (already sanitized text from the model) on `gdk.Display.Clipboard()`, after removing URL credentials: every `scheme://user:pass@` and `scheme://user@` becomes `scheme://***@` (a `runview` function with tests: https, git+ssh, several per line, no false hits on `user@host` without a scheme). DMS's clipboard history keeps whatever is copied, and a failing `git pull` prints its remote URL. "Open {category}.log" shows the Logs page at the run and `PlanStep.Category`. All three show only after `Finished` (not while busy) |
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

### 3.1 CSS: layered over the user's DMS theme

DMS themes every GTK4 app through the user stylesheet:
`~/.config/gtk-4.0/gtk.css` is `@import url("dank-colors.css")`, a
Matugen-generated file of `@define-color` overrides for libadwaita's named
colours (`window_bg_color`, `view_bg_color`, `headerbar_bg_color`,
`sidebar_bg_color`, `card_bg_color`, `dialog_bg_color`, `popover_bg_color`,
`accent_bg_color`/`accent_fg_color`, `error_*`, `destructive_*`, and the
backdrop ones). GTK loads it at `GTK_STYLE_PROVIDER_PRIORITY_USER` (800).
For any property, or any named colour, that a higher-priority provider sets,
that provider wins whatever the selector's specificity. archmaid's CSS must
sit under that, not on top of it:

- **One provider, `GTK_STYLE_PROVIDER_PRIORITY_APPLICATION` (600)**:
  `gui/internal/ui/style.css`, embedded with `go:embed`, added once with
  `gtk.StyleContextAddProviderForDisplay` in `Main`. It is above the theme
  (200) and settings (400) and below the user (800), so the user's DMS file
  still has the last word. Never `USER` or higher, and never a second
  provider per widget.
- **Scoped to archmaid.** The window root gets the CSS class `archmaid`
  (`win.AddCSSClass`), and every selector in `style.css` starts with
  `.archmaid` (for example `.archmaid .am-step-header`). Our own classes are
  prefixed `am-`. No bare element selectors (`button`, `window`,
  `headerbar`). Every `AdwAlertDialog` gets the `archmaid` class too: a
  dialog that doesn't fit the window is shown as its own floating window,
  outside the main window's tree.
- **Never redefine libadwaita's or DMS's colour names.** Our
  `@define-color` (or `--am-*` custom properties: GTK 4.16+ supports `var()`
  and `color-mix()`; this machine has 4.22) only defines `am_*` tokens. The
  first draft's "redefine the named colours and force dark" would fight
  DMS, and DMS's USER-priority definitions would win anyway. Drop it, and
  leave `AdwStyleManager` on the system colour scheme.
- **Surfaces follow DMS.** Window, sidebar, header, cards, dialogs and the
  accent are left to libadwaita's named colours, so DMS colours them. Our
  tokens refer to those names (`@define-color am_rail alpha(@window_fg_color, 0.15);`,
  `am_accent` = `@accent_bg_color`, tints with `color-mix()` from them).
- **The design's own colours are confined to state.** The Tokyo Night
  hues, green done, yellow warning, red failed and blue running (`#9ece6a`,
  `#e0af68`, `#f7768e`, `#7aa2f7`), go only on the timeline markers, the
  segment bar, the badges and the tags, where libadwaita has no named colour
  DMS sets (DMS sets `error_*` and the accent; success and warning are
  libadwaita's own). Whether the surfaces should be Tokyo Night instead of
  DMS's palette is the owner's call: it is the same decision as Q8 for the
  output. Switching is one token block in `style.css`, not a code change.
- **The password dialog's caller line is exempt from styling** (1.4). No
  `.archmaid` rule may dim, shrink, ellipsize, hide or lower the contrast
  of the label with class `am-caller`. Its one rule makes it as prominent as
  the body text (`font-size` and `color` inherited from the body, weight
  600). A review checks every change to `style.css` against this.
  Accepted residual: the user's own `gtk.css` loads above ours and could
  restyle it. Whatever can write `~/.config/gtk-4.0` already runs as the
  user, so this is no worse than the rest of the desktop.
- **Structure is ours.** Square corners, hairlines, the corner marks, type
  sizes, the condensed heading font and spacing. DMS's file sets no
  structural properties, so nothing conflicts.

**When DMS's colours change at runtime** (a new wallpaper rewrites
`dank-colors.css`), GTK owns reloading the user stylesheet. archmaid adds no
file monitor of its own and must not cache a resolved colour. Every colour
in `style.css` is a symbolic reference, resolved when the style is
computed, so whatever restyles other libadwaita windows restyles archmaid's.
Two things are not CSS and don't follow:

- **The output's ANSI palette.** `runview.LoadPalette` reads DMS's
  `dms-colors.json` once, when the window starts. Re-read it at every
  `startAction`: a small, GUI-only change (an addition to A5's commit). A
  run in progress keeps the colours it started with.
- **Pango attributes already bound to visible output rows.** They are
  rebuilt as rows rebind; there is no need to force a refresh.

Dev 5 checks this in the VM: change the DMS scheme with the GUI open, and
the surfaces and accent must follow while the state colours stay.

Fonts: headings `"Barlow Condensed", "Adwaita Sans", sans-serif`, body the
system font, mono `"JetBrains Mono", monospace` (Q4). GTK CSS limits that
remain: no `::before`/`::after` content, no CSS grid, no `position`.
Transitions are fine, but nothing may animate per output line.

### 3.2 Per-step output under the 50k-line budget

The design keeps every throughput property the current pane has:

- **Structure.** One `GtkListView` over `GtkNoSelection` ←
  `GtkFlattenListModel` ← a `GListStore` with one `GtkStringList` per step.
  `GtkFlattenListModel` is a `GtkSectionModel` (each child model is a
  section) and `SetHeaderFactory` draws the step headers. Only the visible
  rows and headers exist as widgets, as today.
- **The model stays the authority.** `runview.Model` keeps the 10k-line
  `OutputCap` across the whole run, not per step. `Apply` returns the diff
  per step (`OutRemove` from the oldest step with lines, `OutAppend` to the
  running step), so a flush is still one `Splice` on the running step's list
  (plus one on the oldest step's when the cap trims). The flattened
  model's `items-changed` is that splice offset into the whole list: no
  rebuild, no per-line signal. Pump, `FlushLines` = 512, `MaxPending` and
  `Dropped` are unchanged.
- **Never-empty sections.** A pending or collapsed step's child list holds
  one summary item. Collapsing or expanding a step is one splice of that
  step's list, never a rebuild of the others.
- **Follow-the-bottom** stays one `ScrollTo` per frame from a tick callback.
- **Colour** stays raw text in the list, with Pango attributes built at bind
  for the ~30 visible rows.

The guards that prove it (they run on the dev machine, not in CI: CI runs
the GUI suite without the budgets):

- `ARCHMAID_GUI_PERF=1 gui/scripts/headless.sh`, which is
  `TestGUIThroughput` over `flood`, `flood-paced`, `flood-default` and
  `flood-colour`, with its budgets: ≥ 5k lines/s unpaced with nothing
  dropped, main-thread work per flush p95 ≤ 4 ms, nothing dropped at 10k
  lines/s paced, RSS after 50k lines < 200 MB. Plus `TestGUIFirstFrameAndIdleRAM`
  (< 400 ms, < 125 MB, raised from 120 in row 13 with measurements).
- **New with commit 11:** a `flood-steps` scenario, 50k lines over the
  10-step plan with steps finishing mid-flood (several sections, the cap
  trimming across a section boundary, collapsed steps), under the same
  budgets.
- In plain Go, `BenchmarkApplyFlush` and `BenchmarkApplyFlushColour`
  (`gui/internal/runview`, ~80 µs for a 512-line colour flush today), plus a
  new `BenchmarkApplyFlushSections`. Each is compared before and after commit
  6 with `go test -bench` and benchstat by hand, because
  `scripts/bench-compare.sh` covers the root module only.

A fallback, if `flood-steps` misses the budget, keeps the visual: one flat
`GtkStringList` (today's pane) with the step headers as ordinary rows
interleaved by the model. Collapse then hides a step's lines by splicing
them out and back. It costs a little more model code and no extra widgets.

## 4. What stays exactly as it is

| Behaviour | Where the design touches it | Rule |
|---|---|---|
| **One password per run** (decision F: the run's first password is kept, su-checked, used for later sudo and polkit prompts, wiped at Finished/Close/KillNow) | 1.3 note text, 1.4 card | Visual only. The dialog still reads the entry as C bytes (`secret.go`), wipes it, sends `SubmitPassword`, and `Cancel` sends `CancelPrompt`. The "kept for this run" line stays, saying what the password answers (1.11). The kind (sudo or polkit) comes from data, never from parsing the Requester, and the caller line follows 1.4's hard rule. The confirm's sentence must describe F, not "up front" |
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
| 2 | 4 | `gui: app stylesheet layered over DMS, the archmaid scope and am- classes` (3.1: APPLICATION priority, `.archmaid` on the window and dialogs, state colours only; no layout change) | — | every existing scenario; shots; a scratch `XDG_CONFIG_HOME/gtk-4.0/gtk.css` with distinctive named colours must win over ours on surfaces (headless) |
| 3 | 4 | `gui: sidebar shell with breakpoints` (OverlaySplitView, nav list, footer, Stop visible only while busy, narrow bottom status bar) | 2 | all scenarios; `TestGUISmallWindow` extended to 630×372 (sidebar collapsed, title, Stop and dialog title in view) |
| 4 | B | `sysstat: byte totals for the meters` (A1) | — | fixtures; `BenchmarkSystemStatusCache100` allocs unchanged; bench-compare |
| 5 | B | `overview: plan preview, descriptions, root tags` (A2; drop View log) | — | plain Go, under polkit and sudo Env; nothing executed (testenv traps) |
| 6 | B | `runview: output grouped by step, summaries, run header data` (A5, A10) | — | replay `stub.FullCleanup`, floods across the cap (oldest step drops first), `BenchmarkApplyFlush*` within budget |
| 7 | B | `logview: entry headers, topgrade sections, search filter` (A7) | — | fixture logs, including raw escapes (stay plain) |
| 8 | B | `frontend: the topgrade config defaults, shared by the TUI and GUI` (A9 backend) | — | the TUI's `editConfig` tests unchanged; frontend tests |
| 9 | 4 | `gui: meters in the sidebar` | 3, 4 | shots; the `idle` scenario's RAM and first frame within budget |
| 10 | 4 | `gui: Overview as the modular grid; Dry run first` (A3) | 3, 5 | `fullcleanup` from the hero; a new `dry-first` scenario (dry run on the next Driver only, `gui.json` unchanged) |
| 11 | 4 | `gui: Run timeline on a sectioned list view` | 3, 6 | every run scenario; `flood*` budgets with `ARCHMAID_GUI_PERF=1`; follow-the-bottom incl. a scrollbar drag |
| 12 | 4 | `gui: failed-step actions` (A6) | 11 | a failing topgrade shim: Run again starts topgrade, Copy error fills the clipboard with `://user:pass@` redacted (the redaction unit-tested in `runview`), the Open log page shows the run |
| 13 | 4 | `gui: restyled dialogs, the gate's plan rail` | 2, 5 | `fullcleanup`, `uncached`, `wrong-password`, `askpass`, `real-*`, `TestGUIOnePassword`, `TestGUISmallWindow` (dialog title in view; the caller label shown whole at 630×372 for a 120-character requester: wrapped, not ellipsized, no `dim-label`); a sudo requester whose path contains "polkit" still gets the sudo dialog (the kind comes from `NeedsPassword.Polkit`) |
| 14 | 4 | `gui: Logs page with categories, sections and search; Open folder` (A4, A7) | 3, 7 | `logs` scenario (the state dir unchanged after); a launcher seam so tests never open a real app |
| 15 | 4 | `gui: Settings cards; Choose…; Open in editor` (A8, A9) | 3, 8 | `settings` scenario; the folder chooser and launcher behind seams (no portal in headless) |
| 16 | C | `dms: popout in Workbench's sections` | — | `TestStateJS` locally; scratch quickshell load |
| 17 | 5 | (no commit) the VM screenshot comparison against the mockup, per screen, after 10, 11, 13, 14, 15; differences go to Dev 4 as a list | each | — |

Dev B starts with 4–8 while Dev 4 does 1–3, so the UI commits find their
data ready. Commits 11 and 13 are the risky ones (the pump and the password
path): give each its own review.

What the decisions (section 7) add to the plan:

- **Q12, a clean page per run:** row 6 makes a new queue start with empty
  output and adds `runview.Model.Clear`. Dev 4's row 11 calls
  `w.redraw(w.model.Clear())` in `startAction`, so an action that ends in a
  notice doesn't show the last run's output either. It has a harness check:
  the second run's page holds none of the first run's lines. **Changed in
  row 13** (Dev 5's VM round: a declined gate after a failed step's Run
  topgrade blanked the failed run and timed the gate): the page clears when
  the new run's queue starts (`QueueStarted`), not in `startAction`. An
  action that ends before its queue (a declined gate, a notice) keeps the
  last run's page, header and elapsed time; a toast says how it ended.
  `TestGUIClearRerun` and `TestGUIRealDriver/failed-decline` check both.
- **Q13, a gate for Run topgrade:** `actions.Action.Gate` on Run topgrade,
  plus the TUI's `topgrade_*` transcript goldens, as a separate commit by
  **Dev 3** (`internal/actions`, `internal/tui/testdata`). No P8 row touches
  those files.
- **Q9, the meters:** row 4 goes ahead.
- **Q3:** no "Dry run instead" button; "Dry run first" stays on the hero
  (row 10).
- **Q7:** "Open in editor" also works in a dry run (row 15).
- **Q8:** the output keeps DMS's dank16 palette, and the timeline's state
  colours are Tokyo Night (3.1).
- **Q4, Q5, Q6, Q10, Q11:** as recommended (system font with an optional
  Barlow; Adwaita symbolic icons; prefix badges only; corner marks on the
  hero, primary buttons and dialogs; 960×640).

## 7. Decisions (owner, 2026-10-05)

The owner accepted every recommendation below as written; each question
stands as the record of what was decided and why. With that the plan in
section 6 is final.

1. **The password: inline card or dialog?** The mockup asks inline. *Recommend
   the dialog, styled as the card.* An inline entry can be scrolled away or
   missed on another page, and the dialog path is the reviewed, wiped one.
   1.4 has the full reasoning: response-only handling (bc5646b), the wipe
   on withdrawal, and one-password's kept-password note.
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
