# DMS bar widget (P7): design

Status: design only. Optional, low priority. Nothing here changes the TUI or helper.

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
- tint: normal (`surfaceText`), warning if last run > 14 days ago (configurable),
  error colour if unacknowledged high AUR findings > 0 or last run failed.
- vertical bar: icon only plus a small badge.

**Popout** (340 x ~320):
- Last run: relative time, outcome, log dir (button "Open log folder" = `xdg-open`).
- Pending updates: repo count (and AUR count only if cached).
- AUR audit: snapshot present/age, N unacknowledged high findings (names, max 5).
- Helper: installed or not (hint text only).
- Button **Run Full Cleanup**: launches the GTK app if `archmaid-gui` is on PATH,
  else `$TERMINAL -e archmaid` (setting: command override). It only *starts a UI*;
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
    "outcome": "ok",            // ok | failed | aborted | dryrun | unknown
    "steps_failed": 0,
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
  `runDirPattern`), `dryrun` separate. Outcome is **not recorded today**; P7a adds a
  tiny `last-run.json` written atomically at run end (0600, same dir). Until then
  outcome = "unknown", time = dir name.
- AUR: `aur-maintainers.json` gives snapshot presence/age/count today. "Unacknowledged
  high findings" is **not persisted**: P7a adds `audit-state.json` written when an
  audit finishes (findings + whether the snapshot was accepted). Corrupt snapshot
  counts as one high finding (matches `snapshotFinding`).
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
