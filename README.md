# archmaid

Housekeeping for Arch Linux. It cleans package caches, removes orphans,
vacuums logs, updates the system through
[topgrade](https://github.com/topgrade-rs/topgrade), audits your AUR packages
and more, all from one menu. Every destructive step asks first, and
`--dryrun` (or the GUI's Dry run setting) shows what would run without
running it.

It comes in three parts that share one engine:

- **`archmaid`**: a [Bubble Tea](https://github.com/charmbracelet/bubbletea)
  TUI in Tokyo Night colours ([`cmd/archmaid/`](cmd/archmaid/README.md)). It
  gets root through `sudo` and asks for your password inside the TUI, never
  on the raw terminal.
- **`archmaid-gui`**: a GTK4/libadwaita window (`gui/`) with Overview, Run,
  Logs and Settings pages. Root steps go through **polkit** to a small root
  helper, `/usr/lib/archmaid/archmaid-helper`, which is never setuid
  ([`cmd/archmaid-helper/`](cmd/archmaid-helper/README.md)). One password
  prompt covers the run: its polkit request and topgrade's `sudo`.
- **A DankMaterialShell bar widget** (`packaging/dms/`) showing the pending
  update count, the last run's outcome and unacknowledged AUR findings. It
  only reads archmaid's state files, and it opens the GUI (or the TUI in a
  terminal).

## Install

Build both packages from the PKGBUILD and install them in one transaction.
`archmaid-gui-git` requires the exact `archmaid-git` of the same build. You
run the last step yourself, because it needs root:

```sh
git clone https://github.com/Enzord758/archmaid.git ~/archmaid   # or: git -C ~/archmaid pull --ff-only
P=$(mktemp -d) && cp ~/archmaid/packaging/arch/PKGBUILD ~/archmaid/packaging/arch/archmaid.install "$P"/ && cd "$P" && makepkg -s
sudo pacman -U "$P"/archmaid-git-r*.pkg.tar.zst "$P"/archmaid-gui-git-r*.pkg.tar.zst
```

`makepkg` clones `main` from GitHub. A cold build of the GUI takes several
minutes (gotk4's cgo). The two packages:

- **`archmaid-git`**: the TUI, the root helper, the polkit policy, the
  widget and the updates timer. It is a complete install on its own, with
  no GTK.
- **`archmaid-gui-git`**: the GUI.

What the packages install, how to build a local checkout instead, and why
there are two: [`packaging/arch/README.md`](packaging/arch/README.md).

`install.sh` still builds the TUI alone into `~/.local/bin`, with no helper,
no GUI and no widget ([`INSTALL.md`](INSTALL.md)). Don't use it next to the
packages: `~/.local/bin` usually comes before `/usr/bin` on `PATH`, so its
binary would shadow the package's.

## First run

1. **Remove an old `install.sh` build**, if you have one:

   ```sh
   ~/archmaid/install.sh --uninstall && hash -r
   command -v archmaid    # must print /usr/bin/archmaid
   ```

   It removes `~/.local/bin/archmaid` and any stale icon or `.desktop` entry of an older install.sh.
   This matters because a pre-refactor build is harmful, not just stale:

   - It deletes old run folders at every start, `--help` included.
   - It reads the new AUR snapshot (`aur-maintainers.json`) as "no
     baseline" and rewrites it in the old format. That drops the
     co-maintainer records, so a takeover would pass without a warning.
   - It writes none of the state files the widget reads.

2. **Try a dry run**: `archmaid --dryrun`, or in the GUI: Settings → Dry
   run. A dry run changes nothing except the `dryrun/` log folder. It still
   asks the AUR (one request) and runs the read-only orphan query.
3. **For the GUI, polkit's `pkttyagent`** (part of the `polkit` package)
   lets the GUI ask for the root steps in its own dialog. Without it, the
   GUI falls back to your desktop's polkit agent (for example polkit-gnome)
   and says so in its status line; with neither, the root steps can't ask
   and end as "Not authorised".
4. **Optional: the bar widget.** Run
   `systemctl --user enable --now archmaid-updates-check.timer`, then turn
   on "archmaid" under DMS Settings → Plugins and add it to the bar
   ([`docs/dms-widget.md`](docs/dms-widget.md)).

## Menu options

| Option | What it does |
| --- | --- |
| **Full Cleanup** (recommended) | Runs every step in sequence. It audits your AUR packages first: if one changed maintainer or gained a co-maintainer since the last audit, it names it and asks before updating (declining skips just the update). Then it updates the system via topgrade and trims the package cache to 3 versions, with an optional full wipe. It looks for orphaned packages *after* the update, so it also catches the ones the update left behind. When there are any, it asks before removing them, and the confirm gives both the orphan count and the total `pacman -Rns` would remove with their now-unneeded dependencies. Then it cleans the AUR cache, ages out temp files with `systemd-tmpfiles --clean` (by the system's tmpfiles.d rules: on Arch, 10 days in `/tmp` and 30 days in `/var/tmp`), vacuums logs older than 2 weeks and checks package integrity. Last, it updates Hyprland plugins via hyprpm, if installed. hyprpm comes last on purpose: it drops the sudo session on exit. In the GUI, the root steps come after the orphan confirm and the cache-wipe confirm and run as one polkit call |
| Clear user cache | Deletes everything under `~/.cache` |
| Repair broken packages | Finds packages with missing files (`pacman -Qkq`, as you), rechecks them as root and reinstalls the confirmed ones |
| Delete old downloads | Deletes files and folders not changed in 30 days from your downloads folder, listing them first. Age is by ctime, so a back-dated download isn't mistaken for an old one, and a folder goes only if nothing inside it is new. The folder is `--downloads <dir>` (or the GUI's setting), `$ARCHMAID_DOWNLOADS`, your XDG download dir, `~/Downloads` or `~/Download`, whichever comes first, and never `$HOME` itself (see [`cmd/archmaid/README.md`](cmd/archmaid/README.md#downloads-folder)) |
| Run topgrade | Asks first ("Run a system update with topgrade?"; in the TUI the password prompt stands in for it when sudo needs one), then audits your AUR packages, exactly as Full Cleanup does, and runs a system update via topgrade, streaming its output live |
| Disable unused services | Lists systemd services that are enabled but not currently running, and lets you multi-select which to disable |
| Edit topgrade config (TUI) | Opens `~/.config/topgrade.toml` in `$EDITOR`, creating it with sane defaults first if it doesn't exist |
| Audit AUR packages | A read-only check of your foreign packages against the AUR. It flags adopted or maintainer-changed packages and added co-maintainers (compared with your previous audit), removed co-maintainers, orphaned or out-of-date packages, very recent pushes, and packages no longer on the AUR |
| View log | Pick a run and a category (topgrade, orphans, cleanup, AUR audit) and read its log. In the GUI, this is the Logs page |

## Passwords

- **TUI.** One password popup up front covers the run. If something needs
  to re-authenticate mid-run, the popup comes back and names the program
  that asked. Before anything reaches `sudo`, archmaid checks the password
  with `su`, so a typo costs no `pam_faillock` attempt. The password is
  never stored or logged. The TUI drops its sudo ticket on exit (`sudo -n
  -k`, however it ends): your next manual `sudo` in that terminal asks
  again.
- **GUI.** A run asks for your password once, in the GUI's own dialog,
  and keeps it in memory for that run only (an mlocked page, never written
  anywhere, wiped when the run ends however it ends, and after 60 minutes
  at most). It then answers topgrade's and yay's `sudo` prompts and the
  one polkit request of Full Cleanup's root steps: archmaid registers
  polkit's own text agent (`pkttyagent`) for the GUI, so the desktop's
  polkit dialog doesn't appear for archmaid's own calls. Anything else that
  asks (a `sudo` from an AUR package's build, say) gets the dialog with its
  name, never the kept password. Every answer is checked with `su` first,
  so a wrong password costs no `pam_faillock` attempt. The GUI reads the
  faillock state first: on a locked account it doesn't ask, and when one
  more failure would lock the account it warns you. If topgrade's `PATH`
  has a `sudo`, `su`, `pkexec`, `doas`, `run0`, `please` or `sudo-rs`
  (or a relative entry) before `/usr/bin/sudo`, the GUI keeps nothing
  that run: every prompt asks you, and the run's output and topgrade log
  name the file. The details and the trust model are in
  [`docs/HANDOVER.md`](docs/HANDOVER.md) ("The GUI keeps the run's
  password", "archmaid doesn't trust PATH for privilege").
- **Both.** archmaid itself runs `su`, `sudo`, `pkexec`, `pkttyagent` and
  `topgrade` from `/usr/bin`, never from your `PATH`, so a program of that
  name earlier on `PATH` is never handed your password.

## Logs and state

Each run gets a directory, `~/.local/state/archmaid/<YYYYMMDD-HHMM>/`, with
one file per category (`topgrade.log`, `orphans.log`, `cleanup.log`,
`audit.log`). Ordinary steps log only failures. topgrade, the AUR audit and
the steps that remove or overwrite something log their outcome even on
success. A dry run logs to a reused `dryrun/` directory. The newest 5 runs
are kept. The oldest goes when a real run writes its first entry, never at
startup.

Next to the run directories are:

- `last-run.json`: how the last action ended;
- `aur-maintainers.json`: the AUR snapshot the audit compares against;
- `audit-state.json`: the high findings not yet accepted;
- `updates-cache.json`: the pending update count, written by the timer.

The GUI's settings are in `~/.config/archmaid/gui.json`. Everything is
private (0700/0600).

## Safety and trust

This project was written almost entirely by an AI agent (Claude Code). A
human directed the design, reviewed the behaviour and made every decision
that matters: what runs as root, what gets deleted, what ships.

- **Tests.** The suite runs under the race detector, with transcript and
  rendered-frame goldens, VHS end-to-end tapes and a headless GUI harness.
  None of it can reach a real `sudo`, `pacman` or your state folder
  ([`docs/TESTING.md`](docs/TESTING.md)).
- **Design.** The reasoning behind the risky parts, and the residual risks,
  is in [`docs/HANDOVER.md`](docs/HANDOVER.md) ("Decisions and landmines").
- **Security review.** What becomes root-reachable and what you accept is
  in [`docs/security-review-merge.md`](docs/security-review-merge.md).

Read the code with the same skepticism you'd apply to any code, and run a
dry run first to see exactly what it would do to your system.
