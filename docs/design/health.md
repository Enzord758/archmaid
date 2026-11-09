# System health: the backend contract (#269)

For Dev 4's GUI. Package `internal/health`, pure Go, read-only, no root, no
password, nothing that changes state. The Overview cell "System health" shows
`Report.Attention()`; its sheet shows one row per check; the Run topgrade and
Full Cleanup confirm sheets show `UnreadNews`.

## API

```go
type State int // health.OK, health.Look, health.Unknown

type Check struct {
    ID     string // see the table below
    Title  string // the row's name (below)
    Group  string // "system", "packages" or "hardware": the sheet's section
    State  State
    Detail string // one plain-text line, sanitized, at most 200 runes
    Copy   string // a command for the user to paste ("" = no action button)
    Link   string // "https://archlinux.org/..." only, or "" (no link button)
}

type Report struct {
    Checks []Check   // always 11, grouped, in the order of the table below
    At     time.Time // Deps.Now() when Run finished
}
func (r Report) Attention() int // the number of Look checks (Unknown is not counted)

func DefaultDeps() Deps
func Run(ctx context.Context, d Deps) Report

type NewsItem struct{ Title, Link string; Date time.Time }
type UnreadResult struct {
    Items     []NewsItem    // newest first, at most 30
    Since     time.Time     // the last full system update; zero = none found
    FromCache bool          // offline: Items come from an older cached feed
    CacheAge  time.Duration // that cache's age (0 unless FromCache)
}
func UnreadNews(ctx context.Context, d Deps) (UnreadResult, error)
```

`Deps` holds every seam: `Query` (the command runner, `proc.Query`), `Now`,
`PacmanLog`, `ModulesDir`, `CacheDir`, `RunningKernel`, `Exists`, `FS`
(statfs), `HTTPGet`, `NewsURL`. `DefaultDeps()` has the real ones, honouring
`ARCHMAID_PACMAN_LOG` and `ARCHMAID_MODULES_DIR` like the run report. A nil
`Query`/`RunningKernel`/`FS`/`HTTPGet` makes its check Unknown rather than
reaching the machine.

Helpers the GUI may use for wording of its own: `health.Ago(d)` ("5 min",
"3 h", "2 d") and `health.ShortBytes(n)` ("118 G", "412 M", "4.2 G").

### Calling it

- `Run` is safe to call from a goroutine; it spawns its own. Call it off the UI
  thread, never from a draw handler.
- Typical time: 0.3-1 s (`systemctl` x2, `timedatectl`, `coredumpctl`,
  `busctl`, the end of pacman.log read twice, the local package DB read for
  the AUR check, a process scan, statfs; news from the cache). Network
  checks run in parallel, each with its own budget: news up to 10 s (1 GET),
  AUR up to 10 s (the RPC, in requests of 150 names), mirror up to 5 s (1 GET). Hard limit for the whole `Run`: 15 s
  (or the ctx you pass, if shorter); a check still running then is Unknown
  "timed out" while the others keep their results. Show the sheet as soon
  as it opens with a spinner per row, or wait for `Run`; there is no
  per-check callback.
- Cancel with ctx (the sheet closing). Nothing is left running.
- A cell refresh of the Overview should call `Run` at most when the sheet opens
  or on "Check again"; the news check is cheap on repeat (3 h cache).
- `UnreadNews` is for the confirm sheets: it makes the same single request (or
  uses the same cache) but runs none of the other checks. Time: cache hit
  ~10 ms, else up to 10 s. Call it when the sheet opens and fill the news
  block when it returns; on error show no block.

## The checks

States: **OK** (green tag "OK"), **Look** (tag "LOOK", counts in "N things need
your attention"), **Unknown** (neutral tag, "?" or "--"; never counted; the
Detail is the short reason). Titles are the row names.

`Report.Checks` is ordered by group; draw a section header whenever `Group`
changes (or filter by `Group`). Section names for the GUI: System, Packages,
Hardware.

| Group | ID | Title |
|---|---|---|
| system | `failed-units` | Failed services |
| system | `restart` | Restart recommended |
| system | `time-sync` | Clock sync |
| system | `crashes` | Crashes |
| system | `pacman-lock` | Package lock |
| packages | `pacnew` | Config files to merge |
| packages | `aur` | AUR packages |
| packages | `news` | Arch news |
| packages | `mirror` | Mirror |
| hardware | `disk` | Disk space |
| hardware | `disk-health` | Drive health |

### 1. failed-units

Runs `systemctl --failed --plain --no-legend --no-pager` and the same with
`--user` (argv, no shell, read-only). Output lines that are not unit names are
ignored, so are repeats. If one manager can't be asked (no user bus in a root
session) the other still counts; if neither answers, Unknown.

| State | Detail | Copy |
|---|---|---|
| OK | `No failed services` | |
| Look | `1 failed: cups.service` / `3 failed: a.service, b.timer, c.service (user)` (first four names, then ` +N more`; user units end in ` (user)`) | `journalctl -b -u cups.service` ; user units `journalctl --user -b -u c.service`; both kinds joined with ` && `; up to 20 units per command, a name with odd characters is single-quoted |
| Unknown | `can't ask systemd` | |

"How long ago" is not shown: it needs a `systemctl show` per unit.

### 2. restart

The running kernel (`/proc/sys/kernel/osrelease`) has no modules folder in
`/usr/lib/modules` any more while other kernels have: an update replaced it.
Same logic as the run summary (`report.KernelCheck`). With several kernel
packages installed, "installed" is the newest release of the running kernel's
flavour (the text after the last `-`, `cachyos` in `7.2.10-1-cachyos`), else
the newest of any.

| State | Detail |
|---|---|
| OK | `Running 7.2.12-1-cachyos` |
| Look | `running 7.2.10-1-cachyos · installed 7.2.12-1-cachyos` |
| Unknown | `kernel release unknown` |

No Copy (a restart is the user's decision). A missing or unreadable modules
folder is OK, as in the run summary: nothing is claimed that wasn't seen.

### 3. pacnew

`.pacnew` and `.pacsave` files pacman.log warned about
(`warning: <file> installed as <file>.pacnew`, parsed by
`report.ParseLog`) that still exist (`os.Lstat` on each path, absolute paths
only). Only the last 32 MiB of the log is read.

| State | Detail | Copy |
|---|---|---|
| OK | `No .pacnew or .pacsave files` | |
| Look | `1 file: /etc/pacman.conf.pacnew` / `5 files: a, b, c +2 more` (full paths, first three) | `sudo pacdiff` |
| Unknown | `no pacman log` or `can't read the pacman log` | |

### 4. news

Arch Linux news since the last full system update.

**Last full system update**, `health.LastFullUpgrade` over pacman.log: the time
of the first `[ALPM] transaction completed` after a
`[PACMAN] starting full system upgrade` line (`pacman -Syu`/`-Su`, also yay and
topgrade), with nothing in between that ends it: a `[ALPM] transaction failed`,
a new `[PACMAN] Running ...`, or another `starting full system upgrade`
(aborted, failed, declined at the prompt). The last such time wins. A plain
`pacman -S foo` is not a full upgrade; a `-Syu` that found nothing to do (no
transaction) does not move the date, which can only make the check say LOOK more
often, never less.

**Feed**: one `GET https://archlinux.org/feeds/news/` (RSS 2.0). 10 s timeout,
body capped at 1 MiB, User-Agent `archmaid`, redirects only to the same host
(at most 3), any status but 200 is a failure. The raw feed is cached at
`$XDG_CACHE_HOME/archmaid/arch-news.xml` (`~/.cache/...`): folder 0700, file
0600, written to a temp file then renamed. A cache younger than **3 hours** is
used with no request. Items: title, link, pubDate only. Title: plain text
through `sanitize.Field`, at most 120 runes (never render it as markup). Link:
kept only when it is `https://archlinux.org/...` (no user info, no port, ASCII,
at most 300 bytes), else `""` and the title stays. An item with an empty title,
an unparsable date, or a date more than a day in the future is dropped. At most
30 items, newest first.

| State | Detail | Link |
|---|---|---|
| OK | `Nothing new since your last update` | |
| Look | `1 new since your last update: <title>` / `2 new since your last update, newest: <title>` | the newest unread item's link |
| Unknown | `No full update found in the pacman log` (the feed was fine, there is no reference date) | |
| Unknown | `Can't reach archlinux.org` / `Arch news timed out` / `Can't read the Arch news feed` (fetch or parse failed, no usable cache) | |

Offline with an older cache (any age; a cache that parses, is a regular file,
at most 1 MiB, and isn't dated in the future): the cache is used. Unread items
make it Look as usual with ` (offline, cached 5 h ago)` appended; with nothing
unread it is Unknown with `Can't reach archlinux.org; nothing new 5 h ago` (a
stale "all clear" is not OK).

`UnreadNews` returns the same items for the confirm sheets: `Items` newer than
`Since`; when no full update is found (`Since` zero) there are no items (and
no error), so a sheet shows no news block. An error (no feed and no cache) means: show no news block. The sheets
show `Title` as plain text, newest first, each with its date, and a link
button only where `Link != ""`; show "(cached 5 h ago)" when `FromCache`.

### 5. disk

Free space for the user (`statfs` Bavail) on `/`, and on `/boot` only when it
is a separate mount (a different `st_dev` than `/`; a missing `/boot` is
skipped).

| State | Rule |
|---|---|
| Look | `/` has less than 10 GiB free, **or** a separate `/boot` has less than 100 MiB free (absolute limits only; no percentage) |
| OK | otherwise |
| Unknown | `can't read disk space` (statfs of `/` failed) |

Detail: `/ 118 G free · /boot 412 M free` (the `/boot` part only when separate).
No Copy.

### 6. time-sync (system)

`timedatectl show -p NTP -p NTPSynchronized` (read-only).

| State | Detail |
|---|---|
| OK | `Clock is synchronized`; or `Waiting for the first sync` (NTP on, not yet synced, uptime 10 min or less) |
| Look | `Automatic time sync is off` (NTP=no); `Clock isn't synchronized` (NTP on, not synced, uptime over 10 min) |
| Unknown | `can't read the clock state` (no timedatectl, a failure, unexpected output); `can't read uptime` |

No Copy.

### 7. crashes (system)

`coredumpctl list --since=-24h --no-pager --no-legend -q`. Exit 1 with no
output is OK. Only the first 256 KiB / 200 lines of output are read.

| State | Detail | Copy |
|---|---|---|
| OK | `No crashes in the last 24 h` | |
| Look | `3 crashes in 24 h: firefox, steam` (up to 3 distinct executable names, then ` +N more`; `200+` when capped) | `coredumpctl list --since=-24h` |
| Unknown | `can't read the crash list` (no coredumpctl, other failures) | |

Run as the user, coredumpctl lists the coredumps the user may read.

### 8. pacman-lock (system)

`/var/lib/pacman/db.lck` exists while no process named `pacman`, `yay`,
`paru`, `makepkg` or `pamac*` is in /proc (the `comm` names, exact match).

| State | Detail | Copy |
|---|---|---|
| OK | `No lock file`; or `pacman is running` (lock present, a pacman process alive) | |
| Look | `pacman's lock is left over from a run that stopped` | `sudo rm /var/lib/pacman/db.lck` |
| Unknown | `can't list processes` | |

### 9. aur (packages)

The installed packages that are in no sync database (`pacman -Qm`), read from
the local DB with `internal/pkgdb` (default paths, `/etc/pacman.conf`), asked
about in one `aursearch` multi-info call (its client, User-Agent and limits;
10 s for the check). `-debug` packages (made by makepkg next to an AUR package)
and illegal names are not asked about; at most 300 names (sorted) are.

| State | Detail |
|---|---|
| OK | `No AUR packages`; `12 AUR packages, all current on the AUR` |
| Look | `not in the AUR anymore: a, b +2 more`; `flagged out of date: c`; both joined with ` · ` |
| Unknown | `Can't reach the AUR`; `AUR check timed out`; `can't read installed packages` |

A hand-built package that was never on the AUR also shows as "not in the AUR
anymore". No Copy.

### 10. mirror (packages)

The first active `Server = ...` line of `/etc/pacman.d/mirrorlist`
(`https://host/archlinux/$repo/os/$arch`); the base is everything before
`/$repo`. One GET of `<base>/lastsync` (only that path), which holds a unix
time. http or https only (a LAN or http mirror is fine, user info, queries
and other schemes are refused), 5 s timeout, 64-byte body cap, redirects only
to the same host. The time must be plain digits between 2000 and 2200.

| State | Detail |
|---|---|
| OK | `mirror.example.com synced 2 h ago` (up to 24 h; a time up to 1 h in the future counts as now) |
| Look | `mirror.example.com synced 31 h ago` (over 24 h behind) |
| Unknown | `can't read the mirrorlist`; `<host> has no lastsync` (404, offline); `<host> has no usable lastsync`; `<host> lastsync is in the future` (over 1 h) |

No Copy.

### 11. disk-health (hardware)

One read-only call: `busctl --system --json=short --auto-start=no --timeout=5
call org.freedesktop.UDisks2 /org/freedesktop/UDisks2
org.freedesktop.DBus.ObjectManager GetManagedObjects`. It needs no root and
no polkit (it reads the properties udisksd already holds); `--auto-start=no`
means it never starts udisksd. The JSON (capped at 4 MiB) is parsed per drive
object (those with `org.freedesktop.UDisks2.Drive`):

- ATA (`Drive.Ata`): data counts once `SmartUpdated` is non-zero and
  `SmartSupported`. Look when `SmartFailing`, or `SmartNumAttributesFailing`
  > 0, or `SmartNumBadSectors` > 0.
- NVMe (`NVMe.Controller`): data counts once `SmartUpdated` is non-zero. Look
  when `SmartCriticalWarning` (an array of strings: spare, temperature,
  degraded, readonly, ...) is non-empty. udisks 2.11 does **not** expose
  "percent used" as a property (only through the `SmartGetAttributes` method,
  which asks polkit), so it is not shown.

| State | Detail |
|---|---|
| OK | `SMART is fine: Samsung SSD 870, Samsung 990 PRO` |
| Look | `WD Blue: SMART says it's failing` / `8 bad sectors` / `2 SMART attributes failing` / `critical warning: spare, temperature`; several drives joined with ` · ` |
| Unknown | `udisks2 isn't running` (busctl failed or udisksd not running); `no SMART data yet` (no drive has been read yet, or none supports SMART, e.g. a VM); `can't read drive data`; `drive data too large` |

No Copy.

## Errors and robustness

- One check failing, panicking or timing out never touches another; it is
  Unknown with a short reason (`check failed`, `timed out`).
- Every Detail is run through `sanitize.Field` where it holds outside text
  (unit names, paths, news titles).
- No shell anywhere: `systemctl` is started by argv. The `Copy` strings are for
  the user's terminal; archmaid never runs them. Unit names go into `Copy`
  only after a strict character check (single-quoted otherwise).
- Nothing is written except the news cache. `busctl` is started with `--auto-start=no` so that asking never starts a service.

## Security notes (for review)

The feed is untrusted: size cap before parsing, `encoding/xml` (no external
entities, no DTD expansion), only three fields read, title sanitized, link
allow-listed, redirects pinned to the request's host, cache 0700/0600 and
atomic, a symlinked or oversized or future-dated cache file is ignored. Tests:
`internal/health/news_test.go` (`TestParseFeedHostile`, `TestHTTPGet`,
`TestNewsCache`). The busctl JSON, the mirrorlist and lastsync are untrusted
too: `internal/health/more_test.go` (`TestDiskHealth`, `TestFirstMirror`,
`TestParseLastSync`, `TestMirror`) cover garbage, huge values, wrong types,
negative and future times, and non-http schemes.
