# TESTING — handoff for another Claude instance

You are picking up the Bubble Tea rewrite of a gum maintenance script (in this
`tui/` directory). Your job: **build out the test suite and run it.** This doc
gives you the architecture, a layered test plan, and the tools to ask the human
for. Work in this directory; the module is `package main`.

## Ground rules

- **Always test with `--dryrun`** for anything that drives real actions. Dry run
  stubs every mutating command (logs "would run" instead of executing), skips
  sudo, and writes to `~/.maintenance_dryrun.log`. Never run the real
  destructive paths (paccache -rk0, rm -rf, systemctl disable) in a test.
- Read `README.md` first for what each screen/action does.
- Don't weaken a test to make it pass. If a test fails, it found a bug — fix the
  code or report it.

## Architecture (where the logic lives)

- `main.go` — the model/update/view loop. Screens: `screenMenu`, `screenPassword`,
  `screenConfirm`, `screenRunning`, `screenSummary`, `screenLog`, `screenNotice`,
  `screenDiscovering`, `screenServices`. `Update` is the single source of state
  transitions — most unit tests drive it directly.
- `tasks.go` — `runStep` (streaming command exec), the sudo commands, the
  dynamic-action discovery cmds (`discoverRepair`/`discoverDownloads`/
  `discoverServices`), and `editConfig`.
- `log.go` — `appendLog(kind, label, body)`, `logPath()` (dry-run aware).
- `main_test.go` — a **seed** suite already covering: dry-run non-execution,
  dry-run logging, sudo three-strikes, discovery→notice, discovery→services,
  broken-package parsing. Extend it.

## Key testable behaviours (assert these)

1. **Dry-run safety**: no mutating command ever executes under `--dryrun`
   (already seeded — keep it green, it's the most important test).
2. **Sudo flow**: `checkSudoCached` cached→`startQueue`; uncached→`screenPassword`.
   Wrong password increments attempts and shows the count; after `maxSudoAttempts`
   it sets `sudoUnavailable`, runs the queue, and each sudo step is logged
   `SKIP: … (sudo authentication failed)` and marked `statusSkipped`.
3. **Confirm flow**: an optional step's "no" → `statusSkipped` + advance; a
   non-optional "no" → aborts to `screenSummary`.
4. **Streaming**: `runStep` (non-dry) emits every output line in order followed
   by exactly one `done` message carrying the exit error; a failing command sets
   `statusFailed`, increments `errs`, and logs `FAIL:` with the captured output.
5. **Discovery routing**: empty result → `screenNotice`; services list →
   `screenServices` with `serviceOn` sized to the list; repair/downloads with
   work → `beginPending` (sudo probe or queue).
6. **Services multi-select**: space toggles `serviceOn[cursor]`; enter with a
   selection builds a `systemctl disable --now …` step; enter with none → menu.
7. **Parsers**: `brokenPackages` and the `servicesPipeline` produce clean,
   trimmed, de-duplicated lines and don't panic when the underlying grep exits 1
   (no matches).

## Test layers

### 1. Unit (extend `main_test.go`) — no extra tools

Drive `model.Update(msg)` directly, type-assert the returned `tea.Model` back to
`model`, and assert on fields/screen. For cmds, call the returned `tea.Cmd()` to
get its message and feed it back. Set `t.Setenv("HOME", t.TempDir())` whenever a
test may write a log. Cover every item in "Key testable behaviours".

### 2. Integration (add `teatest`)

Use `github.com/charmbracelet/x/exp/teatest` to run the whole `tea.Program`
headlessly, send `tea.KeyMsg`s, and assert on the rendered frames (golden files
via `teatest.RequireEqualOutput`). Good for: menu navigation, the password
screen masking, the services checklist rendering, the running-screen checklist
transitions. Run the program with `dryRun = true` set in the test.

### 3. End-to-end (VHS, optional but ideal)

Use [`vhs`](https://github.com/charmbracelet/vhs) `.tape` scripts to drive the
built binary in a real headless pty and capture text/GIF output — this is the
reliable version of the flaky `script`/`printf` pty automation (which does *not*
deliver keystrokes reliably; don't rely on it). Always launch `./tui --dryrun`.
Assert by checking `~/.maintenance_dryrun.log` contents after the run, and/or the
captured frame text.

Example `run.tape`:
```
Output run.txt
Set Width 1200
Set Height 800
Type " "        # any key past intro if needed
Enter           # Full Cleanup
Sleep 500ms
# … navigate, then:
Type "q"
```

## Tools to request from the human

The human offered to install debugging tools. Ask for:

- **`vhs`** (with `ttyd` and `ffmpeg`, its runtime deps) — headless TUI driver
  for reliable e2e tests and visual capture. `pacman -S vhs ttyd ffmpeg` (vhs may
  be AUR: `yay -S vhs`). **This is the one worth having** — it replaces flaky
  pty scripting.
- Network access for `go get github.com/charmbracelet/x/exp/teatest` (a Go module,
  not a system package — you add it yourself, but it needs to download once).
- Optionally **`expect`** (`pacman -S expect`) as a lighter-weight scripted-pty
  fallback if vhs isn't wanted.

Nothing else is needed for the unit layer — `go test ./...` works today.

## Running

```sh
go test ./...            # unit + integration
go test -run TestDryRun  # a subset
go build ./... && ./tui --dryrun   # manual e2e; then inspect ~/.maintenance_dryrun.log
```

## Definition of done

- Every behaviour in the list above has a passing test.
- `go test ./...`, `go vet ./...`, and `gofmt -l .` are all clean.
- At least one teatest golden covering the menu and one covering the services
  multi-select.
- A short note back to the human on anything the tests uncovered.
