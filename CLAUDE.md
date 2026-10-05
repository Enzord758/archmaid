# Working on archmaid as an agent

archmaid runs as root-adjacent tooling on its owner's own machine, and the
agents that work on it run there too. Three incidents set these rules: an agent
ran a real `su` with a wrong password to see what happens, and another ran
`archmaid --help` to inspect a build, which pruned one of the owner's real
run folders. Both did something real "just to check". In the third, an
unshimmed GUI test ran the owner's real `topgrade -y`, which outlived the
test and twelve minutes later had yay call the real `sudo pacman -S -y`,
raising a password pop-up on the owner's desktop and costing a faillock
failure.

**Rule zero: find out how the code behaves through tests, the e2e harness or by
reading it. Never by running the product or a privileged tool on the owner's
machine.**

## Hard rules

1. **Never start archmaid against the real environment.** That covers
   `archmaid`, `archmaid-gui` and `archmaid-helper`, with any flags, `--help`,
   `--version` and `--dryrun` included, whether from `go run`, a built binary
   or `~/.local/bin`. Any start may write or prune `~/.local/state/archmaid`.
   If a binary must run, run it the way `cmd/archmaid/e2e/run.sh` does: under
   a scratch `HOME`, `XDG_STATE_HOME`, `XDG_CONFIG_HOME` and `XDG_CACHE_HOME`
   from `mktemp -d`. To learn what a flag does, read `parseArgs`. To inspect a
   build, use `go list -deps`, `go version -m`, `nm` or `ls -l`, not execution.
2. **Never run privileged or system-changing commands.** That means `su`,
   `sudo`, `pkexec`, `runuser`, `faillock`, `pkttyagent`, `pacman -S/-R/-U/-Syu`,
   `paccache`, `systemd-tmpfiles --clean`, `journalctl --vacuum-*`, and system
   `systemctl`, not even with a harmless-looking argument. When something
   needs a live check, write the exact steps as a guide in your report; the
   owner runs it. Tests may never reach the real `topgrade`, `yay`, `pacman`
   or any other privileged or system-changing tool, not even a read-only call:
   shim it, and `testenv.Run` traps them all and fails the package if one is
   reached.
3. **Write only inside your worktree and temp dirs** (`mktemp -d`,
   `t.TempDir()`). Never create, change or delete anything else under `$HOME`,
   in particular `~/.local/state/archmaid`, `~/.config/DankMaterialShell`,
   `~/.config/gtk-4.0` and the Downloads folder.
4. **Leave the owner's session alone.** Don't kill or restart quickshell or
   dms, don't `systemctl --user enable/start` anything, and don't open
   windows on the live desktop (run GUI tests headless). The owner works on
   this machine while you do: wrap any load generator, stress loop or
   long-running helper in `timeout` (at most 60 s, at most 8 workers), and
   before reporting confirm with `pgrep` that nothing you started is still
   running. (An unbounded 32-way busy loop once outlived the agent that
   started it.) Nothing a test starts may outlive it: `testenv.Run` kills
   every process the package's tests left running and fails the package.
5. **Tests follow `docs/TESTING.md`'s ground rules.** Every test package runs
   under `testenv.Run`, which traps the privileged and system-changing
   binaries, isolates `HOME` and kills (and fails on) leftover processes.
6. **Hand in through the gate.** `./scripts/check.sh` and `make e2e` must pass
   (they fail when the real state folder changed). Your report gives
   `git status` (clean), `git log --oneline origin/refactor/p0..HEAD` and
   `git rev-parse HEAD`.
7. **Say it first.** If you broke a rule, or aren't sure whether something
   touched the owner's real state, stop and put it at the top of your report:
   the exact command and the time. Owning it right away is expected.
8. **Git:** no push, merge or force-push. Chain shell commands with `&&` only
   (a `;` chain once committed broken code) and quote glob arguments
   (the shell is zsh).

## The test VM: the one place real privilege is allowed

`~/archmaid-vmtest` (outside this repo, see its README) installs the
owner's machine in QEMU from `~/dotfiles/install` 01-04, under its own
hostname `amvm-archmaid-test`, and stops before anything personal: no backup
restore, no keys, no secrets. Inside that VM, and only there, archmaid may
run for real: `sudo`, `pkexec`, `pacman`, `topgrade`, the GUI and live
Full Cleanups, through `~/archmaid-vmtest/vm.sh` (ssh to 127.0.0.1:2223).

- Only the designated VM tester (Dev 5, approved by the owner on
  2026-10-05) and the lead drive the VM, and never both at once: there is
  one VM, and two agents in it would test each other's state. Any other dev
  who needs a live check writes the steps in the report; the VM tester runs
  them and reports to the lead, who decides.
- Everything goes through `vm.sh`. A command that would run on the host
  itself still falls under the hard rules above, VM or not.
- Never copy anything personal into the VM (keys, `~/.ssh`, the age key,
  backup data, `~/.local/state/archmaid`), and never run the dotfiles
  harness's restore step against it. If something personal gets in, stop,
  `~/archmaid-vmtest/run.sh --purge`, and say it first (rule 7).
- `run.sh --reset` rolls the VM back to its freshly installed `base`
  snapshot; do it before each live run, so every run starts from the same
  machine.

## Where things are

- `docs/HANDOVER.md`: the code map, landmines and open items. Read the
  landmines before touching privilege, downloads, audit or the step queue.
- `docs/TESTING.md`: how the suite stays safe, the recipes, the coverage map
  and the gate.
