# Merging `refactor/p0` into `main`: record

This file was the readiness review and go/no-go checklist for the merge
(written 2026-10-05 against `refactor/p0` at 6e6f70e and `main` at
5585358). The merge has happened, so it is now a short record. The review
text is in git history (`git log -- docs/merge-to-main.md`; the last version
before this record is at 4fdc62b). The security side is in
`docs/security-review-merge.md`.

## What was merged, and when

`refactor/p0` was merged into `main` as fast-forwards: `main` was an
ancestor of `refactor/p0`, so there were no conflicts and no merge commit.
The owner pushed `main` in steps as the integration branch advanced. What
git's record of `origin/main` shows (local push times):

| Push of `main` | Tip |
|---|---|
| 2026-10-09 06:40 | 336dad6, the first `refactor/p0` commits on `main` (376 commits past 5585358) |
| 2026-10-09 to 2026-10-16 | ten more pushes, each a fast-forward to the then tip of `refactor/p0` |
| 2026-10-16 21:13 | 516fc7b |
| 2026-10-17 07:39 | e29f315 |
| 2026-10-17 19:32 | **4fdc62b**, `main` = `refactor/p0` (554 commits past 5585358) |

`main` before all of this was 5585358 (2026-10-04). The release is marked
`v2.0.0` (`CHANGELOG.md`); the package version stays `rN.sha` from
`pkgver()`.

## The checklist, as it was ticked

**Before the merge**

- [x] CI green on every job that runs (image, core, e2e, bench, gui; core
      and gui come in two variants, ghcr with credentials or ECR without,
      and exactly one of each runs). The `TestStepCannotReadTheTerminal`
      fix (4d3b11d) passed; the earlier gui/e2e failures were flakes or
      fixed. CI was green on 4fdc62b.
- [x] `feat/gui-one-password` (decision F) was reviewed and integrated into
      `refactor/p0` (`internal/privilege/kept.go`,
      `gui/internal/polkitagent`). `./scripts/check.sh`, `make e2e` and
      `make gui-test` pass on every integrated tip.
- [x] Live checks of the final code: Dev 5 in the VM (tasks #173 and #177:
      one GUI Full Cleanup with one polkit dialog and one password, a
      cancelled dialog marking every root row "Not authorised", a TUI Full
      Cleanup, the systemd colour check) and the owner on the real machine.
- [x] `CHANGELOG.md` refreshed to the tip (and released as 2.0.0).
- [x] GUI decision: `archmaid-gui-git` is installed together with
      `archmaid-git` (`HANDOVER.md` → Open items).

**The merge**

- [x] Fast-forward of `main` to `refactor/p0`, pushed by the owner (above).
- [x] CI green on `main`.

**After it**

- [x] The old `install.sh` build was removed from the owner's machine
      (`install.sh --uninstall`), so `/usr/bin/archmaid` is the one on `PATH`.
- [x] The state folder was backed up before the first run of the new code.
- [x] Both packages were built from `main` and installed (r608 on
      2026-10-17, the lead's word; `packaging/arch/README.md`).
- [x] The `~/archmaid-live` worktree is removed, and so are the merged agent
      worktrees. `~/archmaid-vmtest` (the VM harness) stays.
- [x] The widget and the updates timer are the owner's choice and are
      documented in `docs/dms-widget.md` → "Enable (owner)".

`refactor/p0` stays as the integration branch: agents branch from it and the
lead integrates there; `main` follows it by fast-forward when the owner
pushes.

## Procedures worth keeping

Build and install from `main` (the same recipe as README → Install; local
checkouts and scratch builds are in `packaging/arch/README.md`):

```sh
git -C ~/archmaid pull --ff-only
P=$(mktemp -d) && cp ~/archmaid/packaging/arch/PKGBUILD ~/archmaid/packaging/arch/archmaid.install "$P"/ && cd "$P" && makepkg -s
sudo pacman -U "$P"/archmaid-git-r*.pkg.tar.zst "$P"/archmaid-gui-git-r*.pkg.tar.zst
```

Check an install:

- `command -v archmaid` prints `/usr/bin/archmaid` (an old
  `~/.local/bin/archmaid` is gone: `~/archmaid/install.sh --uninstall && hash -r`).
- `pkaction | grep -c '^org\.archmaid\.helper\.'` prints 9.
- `stat -c '%A %U' /usr/lib/archmaid/archmaid-helper` prints `-rwxr-xr-x root`.
- Back up the state folder before the first run after a big upgrade:
  `cp -a ~/.local/state/archmaid ~/archmaid-state-backup-$(date +%Y%m%d-%H%M)`.

Known residual risks from the review that still stand: PKGBUILD `check()`
runs only the helper's, the widget's and `runview`'s tests, and makepkg and
namcap never run in CI; the repo has no license (`LicenseRef-unknown` in the
PKGBUILD); the GUI's speed budgets are measured by hand
(`docs/TESTING.md` → GUI measurements).
