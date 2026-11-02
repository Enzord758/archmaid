# Packages page: the contract (task #230)

Fixed seams so four devs build in parallel. Inputs: the owner's decisions
(2026-11-02, they win), Dev 3's plan #229 (security), mockup variant A.
"(confirm)" marks a decision the lead should confirm. Go stubs that already
exist: `internal/pkgdb/types.go`, `internal/aursearch/types.go`,
`internal/steps/pkgplan.go`. Nobody renames what they hold; additions are fine.

| Who | Owns |
|---|---|
| Dev 2 | `internal/pkgdb`, `internal/aursearch` |
| Dev 3 | helper verbs `plan-install`, `pkg-install`, `plan-remove`, `pkg-remove`, protected list, polkit actions, helper side of the plan event |
| Dev 4 | the page: `gui/internal/pkgview` (plain Go model) and `gui/internal/ui` (gotk4) |
| Dev 1 | helperclient, steps, actions, engine, driver wiring; later `internal/aurbuild` and `pkg-install-local` plumbing |

## 1. Package data types (`internal/pkgdb/types.go`)

`pkgdb.Package` is the one display type for a sync entry, an AUR entry and an
installed one: `Name, Version, Repo` (`"core"`, `"cachyos-v3"`, a mirror's
name, or `pkgdb.RepoAUR`; `""` = installed in no sync DB), `Description, URL,
Licenses, Groups`, `DownloadSize, InstalledSize` (bytes), `Depends, OptDepends,
Conflicts, Replaces, Provides []Dep{Name, Raw, Desc}`, and the installed state:
`Installed, InstalledVersion, Reason (None/Explicit/Dep), RequiredBy,
OptionalFor, Orphan, Foreign, Update`. `Package.AUR *pkgdb.AUR` is set for AUR
only: `PackageBase, Maintainer ("" = orphaned), Votes, Popularity, OutOfDate
(zero = no), LastModified, FirstSubmitted, URLPath, MakeDepends, CheckDepends`.
The page links `https://aur.archlinux.org/packages/<name>`, not `URLPath`.
`pkgdb.View` = `ViewExplicit | ViewDependencies | ViewOrphans | ViewForeign`.

Rules: every string is untrusted and cleaned inside pkgdb/aursearch
(`sanitize.Plain`, names and versions one line) before it is stored, so the GUI
never sanitizes again. `Orphan` is the `pacman -Qdt` rule (dep, nothing requires
or optionally requires it) and `Foreign` is `-Qm`; a test per rule compares them
with a pacman shim's `-Qdtq`/`-Qmq` on the same fixture. `Update` is vercmp of
installed vs sync, informational only: the helper's `-Qu` decides.
Package owner of the types: `pkgdb` (aursearch imports it).

## 2. The search API

**pkgdb (Dev 2), synchronous, in-memory, no context:**

```go
type Paths struct{ Sync, Local, Conf string } // /var/lib/pacman/sync, .../local, /etc/pacman.conf
func Open(Paths) (*DB, error)                  // parses everything; ~100-300 ms, call off the UI thread
func (*DB) Reload() (changed bool, err error)  // cheap: re-reads only if a sync file/local dir mtime changed
func (*DB) Search(q string, max int) []pkgdb.Package // repo hits, ranked; max <= 100
func (*DB) Lookup(name string) (pkgdb.Package, bool) // exact sync-or-installed name, full detail
func (*DB) Installed(v pkgdb.View) []pkgdb.Package   // sorted by name
func (*DB) Count() Counts                            // per view, for the tab badges
```

- Repo order is the `[section]` order of `/etc/pacman.conf` (Include files are
  not followed; the repo name is the section). A name in two repos resolves to
  the first, as pacman does. Missing/unreadable DB file: that repo is skipped
  and `DB.Warnings()` says so; `Open` fails only when nothing can be read.
- Gzip and zstd tar both. zstd needs `github.com/klauspost/compress/zstd`, a new
  go.mod dependency, pure Go, vetted (confirm). No `exec`, ever.
- Ranking (documented in a test table): exact name; name prefix; name contains;
  provides name match; description contains (all words). Ties: repo order, then
  name. Case-insensitive. Empty or 1-rune query: no results (page shows the
  installed list instead). Caps: `Search` returns at most `max`, the GUI asks 50.
- Installed state is merged into every returned package.

**aursearch (Dev 2), asynchronous, cancellable** (`Searcher` interface, real
`aursearch.New(Config)`): `Search(ctx, term)` and `Info(ctx, names)`, as the
stub's doc says. Timeouts: 8 s per request; body cap 8 MiB (same as
`internal/audit`); an explicit `BaseURL` (no default inside a test; zero
`Config` fails); results capped at 40 after ranking; RPC errors, non-200 and
over-cap bodies are errors, never partial lists. Term 2..64 bytes, trimmed,
no control characters, URL-path-escaped by the client.

**The page's search (Dev 4, `pkgview`):**
- The repo search runs on every keystroke after a 120 ms debounce, on a worker,
  against a `*pkgdb.DB` (never blocks the UI thread; a newer query supersedes).
- The AUR search runs only when the term has at least 2 runes, after a 500 ms
  debounce, and cancels the previous request via its context. Search terms leave
  the machine only here. A separate "Search the AUR too" switch is not needed:
  AUR is part of the page, as decided; a Settings toggle "Search the AUR as I
  type" (default on) stays possible later.
- Merge: one list in two sections, "Repositories" (up to 50) then "AUR" (up to
  40). An AUR hit whose name exists in a repo is dropped (repo wins); an AUR
  hit that is installed shows the installed tag. AUR loading and failure
  ("AUR unreachable") show inside the AUR section and never hide repo results.
- The detail pane for an AUR hit calls `Info` once (adds makedepends).

## 3. The plan and run contract (repo install and remove)

**Helper argv** (Dev 3; `hashPattern` `sha256:<64 hex>`, `namePattern`, 1-32 names):

| Verb | Root | argv |
|---|---|---|
| `plan-install` | no (no pkexec) | `plan-install [--asdeps] name...` |
| `pkg-install` | pkexec, action `org.archmaid.helper.pkg-install` | `pkg-install H [--asdeps] name...` |
| `plan-remove` | no | `plan-remove [-n] name...` |
| `pkg-remove` | pkexec, action `org.archmaid.helper.pkg-remove` | `pkg-remove H [-n] name...` |

`-n` and `--asdeps` are the only option tokens, fixed spellings recognised by
the helper right after the verb (plan) or after `H` (root), and each only on its
own verb. Both new actions are `auth_admin`, never keep (decision 4).

**The `plan` event** reuses the helper's `planEvent` (`v:1, t:"plan", targets,
items, total, hash`). `items` stay `"name version"` lines (the same shape as
`remove-orphans`' cascade, so `helperclient` patterns apply); `total =
len(items)`. Additive optional fields (the client ignores unknown fields, so
`v` stays 1): `mode` (`"install"|"remove"`), `with_config`, `asdeps`, `rows`
(`[{name, version, old, repo, target, dl, inst}]`, same order as `items`;
`dl`/`inst` bytes; remove rows have `repo:""`), `warnings []string`. The hash is
`"archmaid pkg plan v1"` + mode + `-n`/`--asdeps` flags + sorted targets +
sorted `repo name version` lines (remove: repo `-`); sizes and `old` are not
hashed. Client mirror: `helperclient.PkgPlanHash` / `CheckPkgPlan`, tied to the
helper's by a test, as `PlanHash`. Decoded into `steps.PkgPlan`.

**The `result` event** gains two optional fields, `reason` (a
`steps.RefuseReason` string) and `names []string` (and `count` for updates
pending). A refusal is a normal `result{ok:false}` with an exit code below;
a stale root run emits the new `plan` event before its result.

**Exit codes** (existing constants, no new ones): `0` done; `1` a root command
failed; `2` bad input (bad name, >32, duplicate: `bad-request`; name not in a
sync DB: `not-found`; not installed: `not-installed`); `3` the situation is not
as confirmed or not ready: `stale` (hash differs, nothing run, new plan
emitted) and `updates-pending` (`pacman -Qu` not empty; also when an item is an
upgrade); `4` refused for this request: `protected`, `conflict` (an item
replaces or conflicts with an installed package, owner decision 1), `required-by`
(remove: pacman would refuse); `5` `busy` (db.lck, another helper, pacman
running). Refusals are decided in the plan verb already, so the sheet is never
shown for a request the root verb would refuse; the root verb repeats every
check under lock.

**`steps` representation.** The verb constants `steps.VerbPlanInstall`
(`plan-install`), `VerbPkgInstall`, `VerbPlanRemove`, `VerbPkgRemove`,
`VerbPkgInstallLocal` are added by Dev 3 together with the helper verbs
(`TestStepVerbsAreHelperVerbs` fails a constant without a verb). One root step per run:
`Step{ID: steps.IDPkgInstall|IDPkgRemove, Verb: steps.VerbPkgInstall|VerbPkgRemove,
Items: plan.Targets, PkgPlan: &plan, NeedsPriv: true, Category: logstore.Cleanup,
AlwaysLog: true, Title: "Installing packages…"}`; `helperclient.verbArgs` builds
`[H, flags..., names...]` from `PkgPlan` only (checking hash, names, that Items
equal Targets), as `orphanArgs`. Under sudo (TUI) the package steps do not exist:
the Packages page is GUI-only and polkit-only (confirm). No Stop after the
helper's start line (landmine); the sheet says so.

**actions / engine.** A Packages run is not a menu entry (it has parameters):

- `actions.Env.PlanPackages func(ctx, steps.PkgRequest) (steps.PkgPlan, error)`
  (the error is `*steps.PkgRefusal` for a refusal), `helperclient.PlanPackages`
  runs `plan-install`/`plan-remove` as the user, like `PlanOrphans`.
- `engine.Session.StartPackages(req steps.PkgRequest) Output` and the Command
  `engine.StartPackages{Req}`, additive. It enters `PhaseDiscovering` ("Checking
  the packages…"), runs the plan, then emits one of the events below.
  Menu IDs are untouched; `actions.Packages(env, plan) []steps.Step` builds the
  queue.
- Events (additive; contract-checked by `enginetest.CheckContract`):

| Event | Meaning |
|---|---|
| `PackagesPreview{Prompt, Plan steps.PkgPlan}` immediately followed by `NeedsConfirm{ID: Prompt, Kind: ConfirmPackages, Default: false (Cancel), Destructive: remove, YesLabel: "Install"/"Remove"}` | the sheet; answered with `Confirm(Prompt, yes)`, as any confirm |
| `PackagesRefused{Req, Refusal *steps.PkgRefusal}` then `Finished{Outcome: OutcomeNotice}` | refused with reason (conflict, protected, not-found, required-by, busy, bad-request); nothing changed |
| `PackagesUpdatesPending{Count int}` then `Finished{OutcomeNotice}` | `updates-pending` from the helper; the page shows the banner |
| `PackagesStale{Req, Plan}` then `Finished{OutcomeNotice}` | exit 3 `stale` on the root run: "the list changed, review again"; the page re-issues `StartPackages(Req)` on the button |
| `QueueStarted`, `StepStarted`, `OutputChunk`, `AuthWaiting/Resolved`, `StepDone`, `Finished`, `RunReport` | running and done, unchanged: the Run page then the existing summary (which reads `pacman.log`) |

"Protected" is `PackagesRefused` with `Reason == RefuseProtected` and
`Names` (target or cascade members); the page greys the row with the reason
from `Detail`. A cancelled/denied polkit dialog is the existing
`AuthResolved`/`StepDone{AuthRefused}` path; faillock is read before the
prompt as for every helper step. One authorisation per run (decision 4) holds:
one run is one `pkg-install` (or `pkg-remove`) call.

## 4. The updates-pending gate

Decided in two places, the helper authoritative:
1. **GUI, advisory, instant.** `pkgview` reads `updates-cache.json` (v3, the
   existing reader the widget model shares; add `overview.ReadUpdates` if
   there is none). If `checked` is within 24 h and `repo > 0`, Install and
   "Add to list" confirm are disabled and the page shows "N updates are
   waiting" with **Update system first**. AUR-only pending (`aur`, `aur_held`)
   does not block a repo install. A stale/missing cache never blocks by
   itself; the helper decides.
2. **Helper, authoritative.** `plan-install` runs `pacman -Qu` (local sync DB
   only, no `-Sy`) and refuses with exit 3, `updates-pending`, `count`. The
   engine turns it into `PackagesUpdatesPending`, the page shows the same
   banner. `pkg-install` repeats the check under lock.
3. **Update system first** sends the existing `engine.Start{Action:
   actions.IDTopgrade}`: topgrade's own gate confirm, askpass and Run page.
   When that run finishes, `engine.RefreshUpdates` already updates the cache; the
   page re-reads it (file watch/poll 2 s while visible) and re-enables. The
   install list is kept in the GUI, not cleared. Remove is never gated.

Known gap (document, don't fix): the cache is `checkupdates`' private fresh sync,
the helper's `-Qu` uses the local sync DB; they can disagree until topgrade has
synced. Disagreement is safe: the helper decides.

## 5. The AUR install contract (built later; the seams are fixed now)

New package **`internal/aurbuild`** (Dev 1): user-side only, no root, no gotk4.
Imports: stdlib, `pkgdb`, `aursearch`, `steps`, `proc` (Query/Runner), `sanitize`.
Not `engine`, not `audit` (the audit gate is wired by `frontend`/`actions`).

**Resolve** (`aurbuild.Resolve(ctx, req, db, Searcher) (Plan, error)`): names
exact via `Info`; dependencies from the RPC `depends`/`makedepends`/`checkdepends`
(re-verified against the fetched `.SRCINFO`, parsed in Go, never sourced). Each
dependency is satisfied by an installed package (also through `provides`: such
a dependency is skipped, not refused), a sync package (via `pkgdb.Lookup`/
provides) or an AUR package by exact name; only an unmet virtual dependency
(a provides name nothing installed or in a sync DB satisfies) or a missing name
is refused, with the dependency named (decided by the lead). Output: repo deps (`pkg-install --asdeps`), AUR packages in build
order (topological; cycles refused).

**Fetch and build step** (user, `Argv` steps via the Driver, `NeedsPriv: false`):
- Dir: `$XDG_CACHE_HOME/archmaid/aur/<pkgbase>` (0700; any symlink in it or in
  the path below the cache root refuses the run). `pkgbase` matches
  `namePattern`. Never `$HOME`-relative guesses: `os.UserCacheDir`.
- Git, not snapshot: updates need history for the diff. `git clone --depth 50
  --no-recurse-submodules https://aur.archlinux.org/<base>.git` (or `git fetch`
  + fast-forward) with `GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null
  GIT_TERMINAL_PROMPT=0 -c core.hooksPath=/dev/null -c protocol.allow=never -c
  protocol.https.allow=always`. The checkout is then pinned: `git checkout
  --detach <commit>` where `<commit>` is the one the user reviewed.
- Nothing sources the PKGBUILD before the sheet is accepted (no
  `--printsrcinfo`, no `makepkg -o`); `.SRCINFO` is read as text.
- Build: `makepkg --clean --cleanbuild --noprogressbar --noconfirm` in the dir,
  as the user, `PKGDEST=<dir>/out SRCDEST=<dir>/src BUILDDIR=<dir>/build`; the
  user's makepkg.conf is honoured. **Never** `-s`, `-i`, `-r`, `-d/--nodeps`,
  `--skipinteg`, `--skippgpcheck`, `--skipchecksums`, and never under sudo.
  Missing build dependencies fail the build with makepkg's message (repo ones
  were installed first).
- The result of the build step: `Result.InsertNext` carries the install step
  (below), so the file list and hash exist only after the build.

**Review payload** (what the sheet shows; `aurbuild.Review`, produced after
fetch, before the sheet): per package base `{Base, Names, Version, Commit,
Prev (last reviewed commit or ""), Files []{Path, Kind: pkgbuild|install|patch|
other, Size, Text (text files <= 128 KiB, else ""; sanitized), Sha256},
Diff (unified, vs Prev, capped 256 KiB), Sources []{URL, Checksum bool},
Flags []string, Audit []AuditFinding, Deps}`. `Flags` is computed from the
`.SRCINFO` and file list, no heuristics beyond: VCS source (`git+`, `svn+`:
"builds the upstream's moving head, not what you reviewed"), `SKIP` checksum,
non-https source, an `.install` script (runs as root at install), a symlink or
binary in the repo. Reviewed commits are remembered in
`$XDG_STATE_HOME/archmaid/aur-reviewed.json` (base -> commit), user state, written
only after a successful install. The engine event: `PackagesPreview` with
`Plan.AUR []steps.AURPlan` (additive field on `PkgPlan`) and a `Reviews`
payload; confirm kind `ConfirmPackages` as repo.

**Audit gate:** the existing `audit.Run` through `frontend.AuditConfig` with
`AuditOptions.Foreign` = installed foreign packages plus the AUR targets and
their AUR deps (so the snapshot is not shrunk; the new names are a baseline
"first seen", not high). A high finding makes the confirm a gated one
(`steps.Result.Check`/`ConfirmAuditGate` style: "accept the findings" checkbox
before the sheet's Install enables). The snapshot commit happens on accept, as
topgrade's. The audit runs while the review is fetched (`Prefetch`).

**`pkg-install-local H <file>...`** (root, Dev 3's design, own security review,
polkit action `org.archmaid.helper.pkg-install-local`, `auth_admin`):
- Files are given as `<base>/out/<name>.pkg.tar.zst` **relative to the invoking
  user's `~/.cache/archmaid/aur`**; the helper takes the home from the passwd
  entry of `PKEXEC_UID` (never an env var or argument), walks every component
  with `openat(O_NOFOLLOW|O_DIRECTORY)`, opens the file `O_NOFOLLOW`, requires
  a regular file, <= 2 GiB, owned by that uid, name matching `^[A-Za-z0-9@._+-]+
  -[^-/]+-[0-9]+-[A-Za-z0-9_]+\.pkg\.tar\.(zst|xz)$`.
- It copies each into `/run/archmaid/staging` (root 0700, created per run, removed
  after) while hashing, then compares to `H`. `H` = sha256 over `"archmaid pkg
  local v1\n"` and sorted `file <name> <size> <sha256hex>\n` lines, computed by
  the GUI-side step (`helperclient.PkgLocalHash`) after the build from the very
  files it names. Mismatch: exit 3 stale, nothing installed.
- It prints `.PKGINFO`, `.INSTALL` and the file list from the staged copy (into
  the log and Run page), then `pacman -U --noconfirm --color always -- <staged>...`
  under pacmanLock and `pacmanIdle`. No `--overwrite`, no `-dd`. One call can take
  several files (pacman orders a `-U` set). Protected/conflict checks as for
  `pkg-install`, run on the staged set (`.PKGINFO` conflicts/replaces).
- The sheet's explicit consent text: "this runs the package author's code as
  root". Cancel is the default.

**Ordering and prompts** (decided by the lead). (1) Missing repo dependencies
and makedepends: `pkg-install --asdeps` (own plan, own prompt). (2) All AUR
bases are built in topological order; AUR-to-AUR *runtime* dependencies are
supported and installed together with their dependents in the one final
`pkg-install-local` (files in build order). AUR-to-AUR *makedepends* are refused
in v1 (they would need an install between two builds). So an AUR run has at
most two password prompts, and the sheet says so up front: "2 password
prompts: dependencies, then the built packages" (one prompt when no repo
dependency is missing).

## 6. Package and import boundaries

- `internal/*` stays gotk4-free; only `internal/tui` may import charmbracelet;
  `check.sh` enforces both and stays unchanged.
- New: `internal/pkgdb` (stdlib, `sanitize`, zstd); `internal/aursearch`
  (stdlib, `pkgdb`, `sanitize`); later `internal/aurbuild` (above);
  `gui/internal/pkgview` (plain Go, no gotk4: search state, debounce, install
  list, sheet and banner state; imports `pkgdb`, `aursearch`, `steps`, `engine`);
  gotk4 only in `gui/internal/ui`.
- The helper (`cmd/archmaid-helper/...`) imports none of `pkgdb`, `aursearch`,
  `aurbuild`: root code reads pacman through pacman, with its own patterns,
  tied to the client's by tests (as `namePattern`). `helperclient` imports
  `steps` only.
- `pkgdb` and `aursearch` are read-only and never start a process. Searching is
  not a Driver run: the GUI calls them directly; only install/remove go
  through the engine. `realdriver` wires `*pkgdb.DB`, `aursearch.New`, and
  `Env.PlanPackages = helperclient.PlanPackages`.

## 7. Test seams

- **pkgdb:** `internal/pkgdb/pkgdbtest` (Dev 2) writes fixtures from Go
  structs into a temp dir: `WriteSync(t, dir, repo, gzip|zstd bool, pkgs...)`
  makes `<repo>.db` as a tar, `WriteLocal(t, dir, pkgs...)` makes
  `local/<name>-<ver>/desc` (and `files`). No binary blobs are committed. A
  pacman shim (testenv style) answers `-Qdtq`/`-Qmq`/`-Qu` for the same fixture to
  tie the orphan/foreign/update rules.
- **aursearch:** `httptest.Server` serving RPC v5 JSON; caps, timeout, cancel,
  malformed, oversized, "Too many results", sanitised fields. Production code
  has no default URL used by tests; testenv traps no network, so a test that
  forgets `BaseURL` fails (empty BaseURL is an error).
- **helper:** `fakeRunner` argv matching (nothing executes) and the pacman shim
  with fixtures for `-Si/-Sp/-Qu/-Rp/-Qq` that fails on any `-S`/`-R` without
  `-p` (plan #229, section 6). Tests pin: the argv builders, the name table,
  exit code and `reason` per refusal, stale re-plan, hash order-independence,
  the hash tie test with `helperclient.PkgPlanHash`, `policy_test`.
- **GUI:** `pkgview` tests take a fake `pkgdb` (a fixture DB) and a fake
  `aursearch.Searcher` (scripted results, delays, errors) and a fake clock for
  the debounce. The fake driver is `internal/engine/stub` with a new
  `PackagesCatalog` (scripts: preview then done, refused-conflict, protected,
  updates-pending, stale, running with output); `--stub` plays them;
  `make gui-test` runs headless with the same pacman shim. `enginetest.CheckContract`
  checks every new event sequence.
- **e2e:** one install and one stale scenario (Dev 1, after the plumbing).
- **VM only (Dev 5):** the plan #229 checklist, plus a real AUR build/install.

## 8. Network scope (HANDOVER, applied at integration)

Replace the sentence "It is archmaid's only own network call (...)" in
"The AUR audit diffs against a snapshot" with:

> Network: archmaid's own code talks to `aur.archlinux.org` in three places,
> always as the user, never as root and never from the helper: the AUR audit
> (RPC `info`, on the audit), the Packages page's AUR search and info
> (RPC `search`/`info`, only when the user types a term of at least 2
> characters, debounced, cancellable), and the AUR install (a `git
> clone`/`fetch` of the package's repo, and the sources `makepkg` downloads from
> the PKGBUILD's own URLs, only after the user reviewed and confirmed). The
> programs archmaid starts (pacman, topgrade, yay) and the optional
> `archmaid-updates-check` timer use the network as they always do. The root
> helper makes no network call of its own; pacman, run by it, downloads
> packages as it always does.

Also add the new rows to the HANDOVER code map and a landmine entry "Packages
page: root never reads a user-writable path twice" when the plumbing lands.

## Lead to confirm

1. zstd decoder dependency `github.com/klauspost/compress` in go.mod (pure Go).
2. `--asdeps` as a fixed token on `plan-install`/`pkg-install` (needed so AUR
   build/runtime repo deps are not marked explicit).
3. Exit mapping: 3 = stale and updates-pending, 4 = protected/conflict/required-by.
4. Updates cache freshness window 24 h; only `repo` count blocks.
5. (decided) AUR deps by exact name; a dependency an installed package provides is skipped; only an unmet virtual dependency is refused.
6. (decided) AUR-to-AUR runtime deps supported in v1; AUR-to-AUR makedepends refused; at most two prompts, stated on the sheet.
7. One sheet before the build, no second confirm after it; the helper logs
   `.PKGINFO`/`.INSTALL` of the staged package instead.
8. Git (with history for the diff) over snapshot tarballs for the AUR fetch.
9. The Packages page is GUI- and polkit-only; no TUI and no sudo path.
10. Search runs outside the Driver (direct `pkgdb`/`aursearch` calls in the GUI).
