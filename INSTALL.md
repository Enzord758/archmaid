# Installing archmaid

`archmaid` is the Bubble Tea TUI (`cmd/archmaid/`). The installer builds it
and puts it in `~/.local/bin` — everything lands under `$HOME`, no root required. The TUI
has no launcher entry or icon; run `archmaid` from a terminal.

## Requirements

- **Go** (`pacman -S go`) — to build the binary.
- **A terminal emulator** — the app is a TUI; run `archmaid` in it.

## Install

```sh
git clone https://github.com/Enzord758/archmaid.git ~/archmaid
cd ~/archmaid
./install.sh
```

This installs:

| What | Where |
| --- | --- |
| binary | `~/.local/bin/archmaid` |

Then run `archmaid` in a terminal. The TUI has no launcher entry and no icon (the GUI has its own).

If `~/.local/bin` isn't on your `PATH`, the installer says so — add it (e.g. in
your shell rc): `export PATH="$HOME/.local/bin:$PATH"`.

## Options

- **Different prefix** — installs under `~/.local` by default:
  ```sh
  PREFIX="$HOME/.local" ./install.sh
  ```

## Uninstall

```sh
./install.sh --uninstall
```

Removes the binary, and any stale icon or desktop entry an older install.sh wrote (only archmaid's own).

## Logs

Logs go to `~/.local/state/archmaid/` — no setup needed. Each run gets a
directory `<YYYYMMDD-HHMM>/` with one file per category (`topgrade.log`,
`orphans.log`, `cleanup.log`, `packages.log`, `audit.log`); `--dryrun` uses a single reused
`dryrun/` directory. The newest 5 run directories are kept: the oldest goes when
a real run writes its first entry, never at startup.
