# Installing archmaid

`archmaid` is the Bubble Tea TUI (`cmd/archmaid/`). The installer builds it and registers
it as a launchable app — everything lands under `$HOME`, no root required.

## Requirements

- **Go** (`pacman -S go`) — to build the binary.
- **A terminal emulator** — the app is a TUI, so the desktop launcher opens it
  inside one. Defaults to `kitty`.
- **rofi** (or any launcher) to launch it by name — optional; you can also just
  run `archmaid`.

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
| icon | `~/.local/share/icons/hicolor/scalable/apps/archmaid.svg` |
| desktop entry | `~/.local/share/applications/archmaid.desktop` |

Then launch it from **rofi** as *archmaid*, or run `archmaid` in a terminal.

If `~/.local/bin` isn't on your `PATH`, the installer says so — add it (e.g. in
your shell rc): `export PATH="$HOME/.local/bin:$PATH"`.

## Options

- **Different terminal** — the `.desktop` uses `kitty` by default. Override it:
  ```sh
  ARCHMAID_TERMINAL="alacritty -e" ./install.sh
  ```
- **Different prefix** — installs under `~/.local` by default:
  ```sh
  PREFIX="$HOME/.local" ./install.sh
  ```

## Uninstall

```sh
./install.sh --uninstall
```

Removes the binary, icon, and desktop entry.

## Logs

Logs go to `~/.local/state/archmaid/` — no setup needed. Each run gets a
directory `<YYYYMMDD-HHMM>/` with one file per category (`topgrade.log`,
`orphans.log`, `cleanup.log`, `audit.log`); `--dryrun` uses a single reused
`dryrun/` directory. The newest 5 run directories are kept: the oldest goes when
a real run writes its first entry, never at startup.
