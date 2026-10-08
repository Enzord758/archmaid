# DMS bar widget (P7): design

Status: **implemented (P7)** as described in "Implementation as built" below; the
rest of this file is the original design, kept for the data contract (section 3).
Where they differ, the "as built" section wins. Nothing here changes the TUI or helper.

## Implementation as built

The widget **reads the state files directly**; there is no `archmaid status`
subcommand (sections 2 and 3 describe that older design). It reads, never writes,
`$XDG_STATE_HOME/archmaid/` (default `~/.local/state/archmaid/`):

| File | Writer | Fields the widget uses |
|---|---|---|
| `last-run.json` | `internal/logstore` | `v`, `action`, `finished`, `outcome` (`ok failed aborted cancelled incomplete`), `steps_failed`, `steps_refused`, `dry_run` |
| `audit-state.json` | `internal/audit` | `v`, `checked`, `packages`, `high[{pkg,reason}]` |
| `updates-cache.json` | `archmaid-updates-check` | `v`, `count`, `checked` |

Files: `packaging/dms/archmaid/` (`plugin.json`, `ArchmaidWidget.qml`,
`state.js` = all parsing and decisions, no I/O), `packaging/systemd/archmaid-updates-check`
(script) and `packaging/systemd/user/archmaid-updates-check.{service,timer}`.

**Pill**: icon `cleaning_services` plus the pending count (hidden at 0 or unknown).
Tint: error colour (red) for an unacknowledged high AUR finding or `outcome: failed`;
warning (orange) for `incomplete` (root steps refused), `aborted`, `cancelled`, or a last
run older than 14 days; dimmed when no file is readable. **Popout** (the GUI's Workbench
sections, DMS's chrome): **LAST RUN** (the action's name, an outcome tag `OK`/`FAILED`/
`INCOMPLETE`/`ABORTED`/`CANCELLED` tinted primary/error/warning/warning/muted, "Mon 5 Oct,
00:45 · 17 h ago", and the failed, refused or stale note), **UPDATES** (the count, checked
when) and **AUR** (packages audited, up to 5 high findings, "+N more"), numbers in DMS's
mono font; and two buttons: Run Full Cleanup (primary), Log folder.
Bar only: no control-center tile.

**Robustness**: a missing, empty, torn, wrong-`v` or wrongly typed file parses to "no
data", never an error; unknown fields and unknown outcomes are ignored (`unknown`); a
dry-run record is ignored. `last-run.json` is one record overwritten per run, so the
14-day rule measures the age of the last run of any kind; after a failed or incomplete
run the outcome warning stands in for "last successful cleanup" (not recoverable from
one record). A pending count older than 3 days is shown as unknown. Files are re-read
every 60 s for the pill (the writers replace files by rename) and on every popout open,
so the popout always shows the files as they are.

**Run Full Cleanup** starts `archmaid-gui --confirm fullcleanup` (the GUI opens on Full Cleanup's confirm dialog, nothing runs before Proceed) if it is on PATH; otherwise `$TERMINAL -e
archmaid` (`$TERMINAL` may carry arguments, split on spaces, never a shell); if
`$TERMINAL` is empty, `xdg-terminal-exec archmaid`; if that is missing too, the popout
says "Set $TERMINAL ..." and nothing starts. Note that DMS only sees `$TERMINAL` if it is
in the session environment (for example `~/.config/environment.d/` or the compositor's
env), not only in a shell rc. Authentication happens in the app, never in the widget.

**Pending updates**: `archmaid-updates-check.timer` (systemd USER timer, no root, no
sudo) runs `archmaid-updates-check` 3 min after the session starts and then every hour
(+ up to 10 min random delay, Nice 19, idle IO). The script runs `checkupdates`
(pacman-contrib; own temporary db, needs `fakeroot` but no root) and writes
`updates-cache.json` (`{"v":1,"count":N,"checked":"<RFC3339>"}`, 0600, atomic) **in the
archmaid state dir**: one directory for the widget and the same name this document
planned. When `checkupdates` fails (offline) the old file stays.

**Install** (package): the widget goes to `/etc/xdg/quickshell/dms-plugins/archmaid/`,
the system plugin directory DMS 1.6.2 reads (`PluginService.systemPluginDirectory`,
found in the dms binary's embedded QML; a same-id plugin in
`~/.config/DankMaterialShell/plugins/` shadows it, which is how to develop a change).
Units go to `/usr/lib/systemd/user/`, the script to `/usr/lib/archmaid/`.

**Enable (owner)**, after installing the package:

1. `systemctl --user enable --now archmaid-updates-check.timer` (for the count).
2. DMS Settings, Plugins: enable "archmaid" (DMS rescans the directory by itself; if it is
   not listed, `dms ipc call plugins reload archmaid`).
3. DMS Settings, Dank Bar: add the "archmaid" widget to a bar section.

Disable: remove the widget from the bar and `systemctl --user disable --now
archmaid-updates-check.timer`. Without a package, for a trial: copy `packaging/dms/archmaid/`
to `~/.config/DankMaterialShell/plugins/archmaid/`.

**Checked**: the load test runs the real QML in a scratch quickshell (`qs -p`, own
config dir, stub `qs.*` modules, no window; see docs/TESTING.md); the DMS-internal APIs
(`PluginComponent`, `PopoutComponent`, `Proc`, `DankButton`) are matched against the DMS
1.6.2 source embedded in `/usr/bin/dms`, but the widget was **not** shown in the live
bar, so look at it once after enabling.

## Decisions (owner, 2026-10-04)

These settle the open questions below and override any alternative the rest of
this document still mentions.

1. **State files: yes.** archmaid writes `last-run.json` and `audit-state.json`
   under `~/.local/state/archmaid/`, so the widget can show the last run's
   outcome and unacknowledged high AUR findings. Writers land with the
   engine/logstore work, ahead of the widget, so the data exists when it ships.
2. **Pending updates come from a systemd user timer** that runs `checkupdates`
   read-only and writes `updates-cache.json` (a later, "could" item). Not from a
   cache the TUI/GUI write as a side effect.
3. **"Run Full Cleanup" opens the GTK app when installed, otherwise the TUI in
   `$TERMINAL`** (not a hard-coded kitty).
4. **Staleness warning after 14 days** without a cleanup.
5. **The package installs the widget** so the user only enables it in DMS. **Bar
   only** — no control-center tile.

## 1. How DMS plugins work on this machine

Found: dms-shell 1.6.2-1, quickshell 0.3.1-1.1. Installed plugins in
`~/.config/DankMaterialShell/plugins/`: `hueManager` (user's own),
`dankKDEConnect`, `dankLauncherKeys`. Docs: <https://danklinux.com/docs/dankmaterialshell/plugin-development>.

- **Manifest** `plugin.json`: `id`, `name`, `description`, `version`, `author`,
  `type: "widget"`, `component` (QML entry), optional `settings` (QML),
  `icon` (Material symbol name), `requires_dms` (e.g. `">=1.4.2"`), `permissions`
  (`settings_read`, `settings_write`, and per the docs `process`, `network`).
  hueManager only declares the two settings permissions and still runs processes,
  so `process` is not enforced in 1.6.2; declare what we use anyway.
- **Entry**: a `PluginComponent` (`import qs.Modules.Plugins`) with
  `horizontalBarPill` / `verticalBarPill` Components, `popoutContent` Component,
  `popoutWidth/Height`, and injected `pluginData` (persisted settings),
  `pluginId`, `iconSize`, `barThickness`, `closePopout()`.
- **Settings**: a `PluginSettings { pluginId: ... }` root with `StringSetting`,
  `ToggleSetting`, `SelectionSetting` etc., auto-persisted to `plugin_settings.json`.
- **Processes**: `Proc.runCommand(id, argvArray, (stdout, exitCode) => ..., debounceMs, timeoutMs, owner)`
  for one-shots (hueManager uses it for `which`, `openhue get`); `Process` + `SplitParser`
  for long-lived streams. Polling is a plain `Timer` in the plugin.
- **Theming**: `Theme` singleton (`Theme.surfaceText`, `Theme.spacingXS`,
  `Theme.fontSizeSmall`, ...), widgets from `qs.Widgets` (`DankIcon`, `StyledText`,
  `StyledRect`). Never hardcode colours; matugen drives them.

## 2. Widget design

**Pill** (icon + one number; icon tint carries state):
- icon `cleaning_services`; text = pending updates count (hidden when 0 or unknown).
- tint: normal (`surfaceText`), warning if last run > 14 days ago (configurable)
  or the last run was `incomplete`, error colour if unacknowledged high AUR
  findings > 0 or last run failed.
- **`incomplete` never clears the staleness warning.** It means steps were
  refused privilege (wrong passwords, a refused helper), so the cleanup did not
  happen. `last-run.json` only holds the latest run, so the widget cannot tell
  how long ago the last complete cleanup was. It therefore shows the warning
  tint as soon as it reads an `incomplete` record, however recent
  `finished` is, and keeps it until a later run ends `ok`. The popout says
  "Last run incomplete: N steps not run (authentication refused)", with N taken
  from `steps_refused`.
- vertical bar: icon only plus a small badge.

**Popout** (340 x ~320):
- Last run: relative time, outcome, log dir (button "Open log folder" = `xdg-open`).
- Pending updates: repo count (and AUR count only if cached).
- AUR audit: snapshot present/age, N unacknowledged high findings (names, max 5).
- Helper: installed or not (hint text only).
- Button **Run Full Cleanup**: launches `archmaid-gui --confirm fullcleanup` (the
  GTK app opened on Full Cleanup's confirm) if `archmaid-gui` is on PATH, else
  `$TERMINAL -e archmaid` (setting: command override). It only *starts a UI*;
  all authentication happens there.
- Button Refresh.

**Refresh**: on bar load, then every 10 min (setting, min 60 s), plus on popout open
if data is older than 60 s. One in-flight call at a time; a 3 s timeout; on any
failure show "unknown" state, never stale numbers as fresh.

**Never**: no sudo, pkexec, polkit, askpass; no writes to archmaid state or logs;
no network of its own; no shell strings (argv arrays only); no `pacman -Sy`;
never parse logs itself (the CLI is the only contract). Acknowledging findings and
running anything privileged happens in the TUI/GUI.

Skeleton (lints clean with `qmllint`; `qs.*` imports are only resolvable inside DMS,
so lint verifies syntax, not API):

```qml
PluginComponent {
    id: root
    property var status: null
    property int refreshSec: Number(pluginData.refreshSec || 600)
    function refresh() {
        Proc.runCommand("archmaid.status", ["archmaid", "status", "--json"], (out, code) => {
            if (code !== 0) { root.status = null; return; }
            try { const s = JSON.parse(out); root.status = (s.v === 1) ? s : null; }
            catch (e) { root.status = null; }
        }, 0, 3000, root);
    }
    Timer { interval: root.refreshSec * 1000; running: true; repeat: true
            triggeredOnStart: true; onTriggered: root.refresh() }
    // horizontalBarPill / verticalBarPill / popoutContent as above
}
```

An unknown `v` is treated as "archmaid too new/old", shown as a dimmed pill, not an error.

## 3. Data contract: `archmaid status --json`

Read-only, no auth, no TUI start, exit 0 even if data is missing (fields null);
exit 2 only on bad flags. Budget < 50 ms: reads a few small files, no forks except
optional `checkupdates` (see below), no network.

```json
{
  "v": 1,
  "generated": "2026-10-04T18:02:11+02:00",
  "last_run": {
    "time": "2026-10-04T17:48:40+02:00",
    "outcome": "ok",            // ok | failed | incomplete | aborted | dryrun | unknown
    "steps_failed": 0,
    "steps_refused": 0,
    "dry_run": false
  },
  "log_dir": "/home/enzord/.local/state/archmaid",
  "aur": {
    "snapshot": { "present": true, "readable": true, "packages": 27, "age_days": 3 },
    "high_unacked": 0,
    "high": []                  // up to 10: {"pkg":"foo","reason":"maintainer changed"}
  },
  "updates": {
    "count": 12,                // null when unknown
    "source": "cache",          // cache | checkupdates | none
    "checked": "2026-10-04T17:55:00+02:00"
  },
  "helper": { "installed": true, "path": "/usr/lib/archmaid/archmaid-helper" }
}
```

Rules: `null` means unknown, never 0. Unknown fields must be ignored by clients; adding
fields keeps `v:1`, removing/renaming bumps `v`. Flags: `--json` (default off prints
a human one-liner), `--refresh-updates` (runs `checkupdates`, which uses its own temp
db and needs no root; ~1-3 s, so the widget never passes it; the TUI/GUI or a future
user timer does and writes the cache).

**Where the data comes from (gaps in today's code):**
- last run: newest run dir `YYYYMMDD-HHMM` under the log dir (name pattern
  `runDirPattern`), `dryrun` separate. The outcome comes from `last-run.json`
  (written by `logstore.WriteLastRun` at the end of every queue: temp + fsync + rename,
  0600, state dir 0700), one record, overwritten each run:
  `{"v":1,"action":"fullcleanup","started":"<RFC3339>","finished":"<RFC3339>",
  "outcome":"ok|failed|incomplete|aborted|cancelled","steps_failed":0,"steps_skipped":0,
  "steps_refused":0,"dry_run":false}`.
  `action` is the menu id (`fullcleanup`, `usercache`, `repair`, `downloads`,
  `topgrade`, `services`, `auraudit`). `cancelled` = nothing ran (declined gate or
  password prompt, sudo gave up with every step skipped); `aborted` = stopped part-way
  (ctrl+c, or a declined required step); `incomplete` = nothing failed, but some steps
  never ran because privilege was refused (sudo gave up after three wrong passwords
  part-way, a refused helper). `steps_skipped` counts every step that didn't run;
  `steps_refused` is the part of it refused privilege (an additive v1 field: a record
  written before it existed reads as 0, and such a record never says `incomplete`).
  Precedence when several apply: `cancelled` (nothing ran), then `aborted`, `failed`,
  `incomplete`, `ok`. A dry run never touches this file: it writes
  `dryrun/last-run.json` instead (`dry_run:true`), so the widget reads the top-level file
  only. `status --json` maps `last_run.time` = `finished`, `steps_failed`,
  `steps_refused`, `dry_run`, and `outcome` straight through (`unknown` when the file is absent; `dryrun` is never
  reported from the real file). Absent file = "no record yet", not an error.
- AUR: `audit-state.json` (state-dir root, **implemented**) is the source for
  `aur.high*` and the snapshot flags; the snapshot's age comes from the mtime of
  `aur-maintainers.json`. Real fields (`internal/audit.State`, 0600, atomic):

  ```json
  {
    "v": 1,
    "checked": "2026-10-04T17:48:40+02:00",   // last COMPLETED audit
    "packages": 27,                            // foreign packages checked
    "high": [ {"pkg": "foo", "reason": "maintainer alice → mallory"} ],
    "snapshot_present": true,
    "snapshot_readable": true
  }
  ```

  `high` is every unacknowledged high finding (count it for "N AUR warnings");
  reasons are sanitized and short ("maintainer a → b", "adopted by x",
  "co-maintainer bob added", "snapshot unreadable" for a corrupt snapshot).
  Written when an audit completes, rewritten empty once the findings are
  accepted (gate "yes" or `a`); a declined gate leaves them. A failed audit
  (AUR unreachable) leaves the file untouched, and a dry run never touches it.
  A missing file means unknown, not zero.
- updates: `updates-cache.json` (count + time), written by the TUI/GUI after its
  status refresh; the CLI only reads it.
- helper: `os.Stat` of the fixed helper path.

**Mapping to the engine**: the engine's `StatusUpdated` event (one Driver ticker) carries
the live equivalents (pending updates, helper presence, last-run info). Both consumers
share one `sysstat`/`status` package that builds a `Status` struct; `status --json` is
its JSON encoding with `v:1`, so the GUI and widget cannot drift. The JSON-lines
protocol of the helper is separate and unrelated.

## 4. Implementation plan

| Commit | Content | Tests | Size |
|---|---|---|---|
| P7a | `archmaid status [--json]` in cmd/archmaid; status struct; `last-run.json`, `audit-state.json`, `updates-cache.json` writers/readers (atomic, 0600, size-capped reads) | Go table tests with temp log dirs: empty, corrupt, partial, old/new; golden for the JSON; timing test < 50 ms; test that no exec/network happens without `--refresh-updates` | M |
| P7b | `packaging/dms/` plugin: `plugin.json`, `ArchmaidWidget.qml` (pill, Timer, Proc call, version check), `qmldir`, settings (refresh interval, thresholds, launcher command) | `qmllint` on all QML in `scripts/check.sh` (skipped if missing); Go test that feeds the example JSON to a tiny jq/QML-free schema check; manual: copy to a scratch DMS config dir | M |
| P7c | popout: last run, audit list, updates, buttons, vertical pill, error/unknown states | qmllint; manual checklist (states: no data, v:2, CLI missing, timeout) | M |
| P7d | install path + docs: PKGBUILD installs to `/usr/share/archmaid/dms/` plus `archmaid dms-install` (or documented `cp -r` to `~/.config/DankMaterialShell/plugins/archmaid`), uninstall note | install.sh dry test in tmp HOME | S |

`quickshell --check` is not a verified option on 0.3.1; use `qmllint` (installed at
`/usr/bin/qmllint`) and a manual load in a scratch profile.

## 5. Risks

- **Plugin API churn**: `qs.*` module paths, `Proc.runCommand` signature and
  `PluginComponent` properties have changed between DMS releases. Mitigate: set
  `requires_dms`, keep QML thin (all logic in the CLI), isolate DMS calls in one file,
  re-test on each DMS bump.
- **Polling cost**: 10-minute interval, one process, <50 ms, `checkupdates` never from
  the widget. Multiple bars/monitors instantiate the pill several times; hold the
  data in a shared singleton (`qmldir` singleton) so it is one call, not N.
- **Theming**: use only `Theme.*` and `qs.Widgets` components; no custom colours;
  verify light/dark and vertical bars.
- **Stale-data trust**: showing "0 high findings" when the state file is missing would
  be a false all-clear; hence null = unknown and an explicit "unknown" pill state.
- **Security**: the widget runs with user rights and spawns only `archmaid status` and
  a user-configured launcher; the launcher setting is an argv, never a shell string.
