# Changelog

All notable changes to archmaid are recorded here, in the style of
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Each bullet ends with
the commit or issue that made the change; the ranges are `5585358..v2.0.0` (554 commits, tip 4fdc62b) `v2.0.0..v2.1.0` (31 commits, tip 3af0f37 before the release notes), `v2.1.0..v2.1.1`, `v2.1.1..v2.2.0` (9 commits, tip 508bf63 before the release notes) `v2.2.0..v2.3.0` (33 commits, tip ae47482 before the release notes), `v2.3.0..v2.4.0` (6 commits, tip 3952cc5 before the release notes), `v2.4.0..v2.4.1` and `v2.4.1..v2.5.0` (10 commits besides the release notes, tip ae61e00), `v2.5.0..v2.5.1`, `v2.5.1..v2.5.2`, `v2.5.2..v2.5.3`, `v2.5.3..v2.5.4`, `v2.5.4..v2.5.5`, `v2.5.5..v2.6.0`, `v2.6.0..v2.6.1` and `v2.6.1..v2.7.0`.

## [Unreleased]

## [2.7.0] - 2026-11-12

It was all merged into `main` (= `refactor/p0`) and passed the local gate and the test VM CI (`run.sh ci`: core, e2e, gui, bench).

### Added

- **`archmaid -fc` / `archmaid --full-cleanup` starts the TUI on Full Cleanup** (#281). Both spellings are one flag (a single token: `-f` and `-c` alone stay unknown options). Full Cleanup's gate shows first, exactly as after picking it in the menu, and nothing runs before you confirm; declining lands on the menu. It combines with `--dryrun` and `--downloads` in any order. The DMS widget's "Run Full Cleanup" terminal fallback (`$TERMINAL -e archmaid`, when archmaid-gui is not installed) now passes `--full-cleanup`, so it opens the same gate as the GUI's `--confirm fullcleanup`.

- **Held updates in the GUI** (#280). The Updates card's "N held" is now a link ("4 held ›", accent colour, always underlined, tooltip "Show held updates"); it shows only while something is held, either by the cooldown (a current check lists AUR updates younger than 72 h) or by an AUR audit. It opens a "Held updates" sheet: "Cooldown · newer than 72 h" with a `held 2d4h` tag, the package, `from → to · free in 20 h` ("free soon" when the check could not read its age, which still counts as held) and a "Review and update" button, and "Held by the AUR audit" with the finding and an "Open audit" button (the existing Audit AUR packages action). An empty section is hidden, and the list scrolls beside its scrollbar, never under it. "Review and update" is the Packages page's AUR install for exactly that package: the plan, the PKGBUILD review box you have to tick, then the root helper's install, with the same password rules; it never skips the review, never touches the page's own list, waits for repository updates like any install, and checks for updates again when the run is over so the list follows.
- **paru works as well as yay** (#278). archmaid picks the AUR helper the way topgrade does: the `arch_package_manager` in your topgrade config when it is paru or yay and installed, else the first installed of paru, yay. Full Cleanup's AUR cache step runs it (`paru -Sc --aur --noconfirm` in the GUI, `paru -Sc --noconfirm` in the TUI, the same flags as yay's), the run summary measures paru's clone folder, the widget's update check lists `paru -Qua` (paru has no age tags, so all its AUR updates count as waiting), and paru may ask for the kept password in the GUI as yay does, only at `/usr/bin/paru`. With neither installed the step says so and does nothing. The package suggests both as optional dependencies.

### Changed

- **Packaging: `udisks2` is an optional dependency of archmaid-gui-git, and the widget's optional dependency is named `dux-shell`.** System health's Drive health row reads SMART from udisks2 and shows unknown without it; DMS is now packaged as `dux-shell`, which replaces and provides `dms-shell`, so the old name still resolved.

### Fixed

- **System health: the mirror check looks at the mirror pacman uses** (#283). It took the first `Server =` of `/etc/pacman.d/mirrorlist`, but pacman uses the first server of `[core]` in `pacman.conf`'s order, so a `Server =` line written in `[core]` before its `Include` (a LAN mirror) was never the one checked. It now asks `pacman-conf --repo core Server` (read only) and checks that server's `/lastsync`, and names it ("192.168.1.189:8099 synced 20 min ago"); without pacman-conf it falls back to the mirrorlist's first. `TestMirrorFollowsPacmanConf`.
- **System health: AUR packages held on purpose are not an alarm** (#283). A package flagged out of date whose update is waiting in the 72 h cooldown (the maintainer has published the fix) and one the AUR audit holds no longer make the AUR row say "look"; the row mentions them ("1 update held (cooldown)", "1 held by the AUR audit"). Packages gone from the AUR, and flagged ones with no update pending, still do. With paru nothing is held by the cooldown. `TestAURHeldIsNotAnAlarm`.
- **"Edit topgrade config" no longer writes a key topgrade rejects** (#278). The default file had `aur_command = "yay"` in `[linux]`, which topgrade does not have (its parser refuses unknown keys there), so a new config made topgrade fail to start. It names the installed helper with `arch_package_manager` now, or leaves the choice to topgrade.
- **The kept password stays off when a helper's own config picks its sudo or pacman** (#282). A paru, yay or topgrade whose config (paru.conf `Sudo`/`Pacman`/`PacmanConf`, yay's `sudobin`/`pacmanbin`/`pacmanconf`, topgrade's `sudo_command`) names something other than the system's own is no longer answered from the password archmaid keeps for the run: such a config could be written by a package's build script. The run then asks at each prompt and logs which file did it. Ordinary configs change nothing.

## [2.6.1] - 2026-11-10

It was all merged into `main` (= `refactor/p0`) and passed the local gate and the test VM CI (`run.sh ci`: core, e2e, gui, bench).

### Changed

- **A run in which nothing ran no longer replaces the Overview's Last run** (#276). Cancelling the Full Cleanup or Run topgrade confirm, closing the GUI on it, cancelling a password or polkit dialog, or giving up on authorisation all ended as `cancelled` and wrote that to `last-run.json`, so Last run and the DMS widget said "CANCELLED" and your last real run was gone from view. Now `last-run.json` keeps the last run that did something (aborted, failed, incomplete, ok, nothing-found are still recorded). A cancelled attempt that logged only its first lines no longer takes one of the five kept run folders from a real run. `TestLastRunNothingRanKeepsThePreviousRecord`, `TestStubRunsNeverEvictRealRuns`, `TestServicesPolkitCancel` and `TestAuditPrefetchCancelled` cover it.

### Fixed

- **System health sheet: the scrollbar no longer sits on the buttons, and "Copy" says what it copies** (#275). When the sheet's list scrolls, its scrollbar now has a strip of its own beside the rows instead of floating over their right edge, where it covered the buttons. The Copy button is "Copy command", with the command in its tooltip (cut with "…" only when very long); clicking it says `Copied “<command>”. Paste it in a terminal.` (a very long command is cut in the toast; the clipboard always gets all of it). The news row's button is "Read on archlinux.org".

## [2.6.0] - 2026-11-09

It was all merged into `main` (= `refactor/p0`) and passed the local gate and the test VM CI (`run.sh ci`: core, e2e, gui, bench).

### Added

- **The System health checks** (#269, #271). Eleven read-only checks, run as you with no password and nothing changed: failed services (system and user), restart recommended (the running kernel is gone), clock sync, crashes in the last 24 h, a pacman lock left over with no package manager running, `.pacnew`/`.pacsave` files still to merge, AUR packages that were in the AUR before (by the AUR audit's maintainer snapshot) and are gone from it, or flagged out of date (your own local builds never count), Arch news since your last full system update, your first Arch mirror's `lastsync` (more than 24 h behind), free space (under 10 GiB on `/`, under 100 MiB on a separate `/boot`) and drive health (SMART through udisks2, which is not started for it). A check that can't run (offline, udisks2 not running) is shown as unknown and never counts. Three of them use the network: the Arch news feed (https://archlinux.org/feeds/news/, kept in `~/.cache/archmaid/arch-news.xml` and fetched at most every 3 hours), the AUR info lookup for your foreign packages, and the mirror's `/lastsync`; each has a short timeout and a size cap and follows no redirect off its host. The contract is `docs/design/health.md`.
- **System health on the Overview** (#270, the GUI half of the health check). A "System health" cell takes the place of "Run topgrade" in the grid. When something needs attention it is tinted warn, with a thin warn edge, a warn icon, a `look` tag and a bold line "N things need your attention"; calm, it looks like the other cells, and Unknown checks never count. Clicking it opens a sheet: the rows that need attention first under "Needs your attention", then System, Packages and Hardware (unknown before OK), each row with its detail on one line and a Copy (a command to paste, never run) or Read/Open (an archlinux.org page only) button, and "Check again". The checks run at start, every 30 minutes, when the Overview is shown with a result over 5 minutes old, and after every run, off the main thread and never two at once. "Run topgrade" is now always on the Updates card (it was the Overview's only other way in), off while a run goes. The Run topgrade and Full Cleanup confirms open with a "NEWS · Read before updating" callout when there are Arch news posts since your last update (the newest title, "and N more", when it was posted, a link); it never blocks Run and is left out if the feed isn't ready within 1.5 s. The TUI is unchanged.

### Changed

- **The Packages update banner says "repository updates"** (#268). It read "4 updates are waiting. Update the system before installing." while the Overview said "10 updates waiting" (repository and AUR) for the same state. Only repository updates block an install, so the banner, the detail's note, the review sheet's note and its "Update the system first" message now say "4 repository updates are waiting" ("1 repository update is waiting"). The Overview's count is unchanged.
- **A Packages run has its own log, `packages.log`** (#268). Its install, removal and AUR steps (fetch, build, collect, install, review record) logged into `cleanup.log`, so Last run said "Packages" over `cleanup.log`. They now write `packages.log`, which the Logs page lists as "Packages", the Overview's Last run names, and the TUI's View log picker offers. Run folders from before still hold their entries in `cleanup.log`, which opens as ever. `TestPackagesStepsLogToPackages`, the engine's install transcript and `TestRunsListPackagesLogAndKeepOldCleanupLogs` cover it.

## [2.5.5] - 2026-11-09

It was all merged into `main` (= `refactor/p0`) and passed the local gate and the test VM CI (`run.sh ci`: core, e2e, gui, bench).

### Fixed

- **Settings starts at the top, and a scrolled page has no dark band in glass** (#266). Settings came back at the offset it was left at; it now starts at its top whenever it is shown (the other pages keep their position, as before). In the glass look a scrolled page drew the scrolled window's shade under the header, a darker band over the translucent window; it is off there. The Appearance card alone in the third row of the two-column grid is left as it is. `TestGUISettingsScroll` scrolls Settings, reads the pixels under the header, and comes back.
- **Packages: each tab keeps its own selection, and the status moves on when the review sheet opens** (#266). The Installed tab showed the Search tab's package (with its Install button) until you picked an installed one; it now shows its own pick, or "Pick a package", and Search gets its package back. The status line stayed at "Checking the packages…" under the open review sheet; it says "Waiting…" there, as the other confirms do. The Run badge's "done" is the same for every run (it stays until the next one starts), so it is unchanged. `TestGUIPackagesTabsAndStatus` covers both.
- **"Check for updates" no longer takes a result from before the click** (#266). The first check after a GUI start said "Up to date" while 4 updates were waiting; the second said "4 updates waiting". The timer's own check starts a few minutes after login, and `systemctl start` does nothing for a unit that is already running, so the click joined that run, which finished with a count from before the packages changed, and the card took it. The check now uses `systemctl --user restart --no-block`, which stops a running check and starts a new one, and the card accepts only a result file written at or after the click. `TestUpdatesCardIgnoresAResultWrittenBeforeTheClick` uses real files with set times.
- **The Overview's Last run age moves on with the clock** (#266). It said "just now" for over ten minutes and showed "13 min ago" only after a restart; the Updates card's "checked N min ago" moved only when something else redrew it. One ticker (every 30 s while the Overview shows, and when it is shown again) now redraws both. `TestGUIAgeTick` runs it with a run that ended 88 s ago.

## [2.5.4] - 2026-11-08

It was all merged into `main` (= `refactor/p0`) and passed the local gate and the test VM CI (`run.sh ci`: core, e2e, gui, bench).

### Fixed

- **The Full Cleanup plan's two columns have a gap again** (#264). Taking the flow box child padding off the plan in #262 took the gap between its columns with it: a "root" tag at the end of the left column ended 2 px from the next column's step number (8 px before). The plan now has an 8 px column gap and 6 px between rows, and keeps its text inset and its right edge. The other flow boxes that lost their child padding in #262 (the Packages hero's tags, the hero's, Updates' and the package detail's button rows) set their own spacing and are unchanged. `TestGUIOverviewHero` now measures the gap between a tag and the next column's number, and between the columns, on a real nine-step plan. Settings > Appearance's help text now says what Auto does (the compositor offers blur through ext-background-effect, or Hyprland).

## [2.5.3] - 2026-11-08

It was all merged into `main` (= `refactor/p0`) and passed the local gate and the test VM CI (`run.sh ci`: core, e2e, gui, bench).

### Fixed

- **Text, buttons and the Logs page line up to the pixel** (#262). The Full Cleanup hero's text started 6 px further in than the text of the cards and cells below it, in both looks, and its description ended 7 px short of theirs; the hero's horizontal padding is now theirs (the vertical padding and the larger title are as they were). The hero's plan and buttons start at its text, not 3 px in (a flow box child's own padding). On the Packages page, a package's tags, Install and Add to list start on the stat grid's edge (they were 3 px in, 1 px left of it in glass) and its hero's text starts where the cells' does. In glass the rest of the detail column (the notes, the flag, the link, the Dependencies and Optional dependencies lists with their tags) was 4 px wider on each side than the stat grid; it is on the grid's edges now. `TestGUIGlassShots` measures every block of the detail, top to bottom, for a repository and an AUR package, in both looks. On the Logs page the log's text started 2 px right of the header's title and its time stamps ended 2 px short of the header's buttons (a list row's padding); both are flush now. `TestGUIOverviewHero`, `TestGUIGlassShots` and the new `TestGUILogsAlign` measure each of these in both looks, dark and light, wide and narrow.

## [2.5.2] - 2026-11-04

It was all merged into `main` (= `refactor/p0`) and passed the local gate and the test VM CI (`run.sh ci`: core, e2e, gui, bench).

### Fixed

- **In the glass look the hero lines up with the blocks under it** (#261). The Full Cleanup hero stuck out 4 px on each side of the cells and the Last run and Updates cards, which sit inside the grid's 1 px border and the cells' 3 px padding. It now takes that as margin and gives it back in padding, so its outer edges are theirs, wide and narrow. A package's hero on the Packages page was off its stat grid the same way, and is fixed too. `TestGUIOverviewHero` measures the hero against the cells and cards at 1920, 1440, 1280, 900, 700 and 500 px in both looks, dark and light; the Packages case is in `TestGUIGlassShots`.

## [2.5.1] - 2026-11-04

It was all merged into `main` (= `refactor/p0`) and passed the local gate and the test VM CI (`run.sh ci`: core, e2e, gui, bench).

### Fixed

- **The Overview's top row lines up with the cell grid** (#260). In the glass look the Last run and Updates cards stuck out 3 px on each side of the cells below, and the gap between them was 1 px against the cells' 8 px; they now take the cells' edges and gap. "Open in Logs" is a raised button like the Updates buttons (it was flat, with its label inset), the Updates buttons start where their card's text starts (a flow box child added 3 px), and the two cards' buttons are on one row. This was also so in the default look. `TestGUIOverviewAlign` measures the edges, the gap, the buttons' start and their row in both looks at 1280, 1440 and 1920 px.

## [2.5.0] - 2026-11-04

It was all merged into `main` (= `refactor/p0`) and passed the local gate and the test VM CI (`run.sh ci`: core, e2e, gui, bench).

### Added

- **An opt-in glass look for the GUI** (#254). Settings > Appearance has a "Glass look" switch (off: the look is as it was) and a "Blur behind window" row (Auto, On, Off), both kept in `gui.json` and applied at once, with no restart. The skin is the owner's "Clear" one: a translucent window (about 60 % opaque on a light theme, 76 % on a dark one), softer corners, light edges, a faint sheen, sheets about 72 to 78 % opaque (about 94 % unless the page under them is blurred, see below), and the Run output, the logs and the PKGBUILD text about 93 % opaque. It has no corner marks and lays the page out exactly as the default look does. The colours still follow libadwaita and DMS, and the light theme keeps its darker state colours. The blur is the compositor's (Hyprland's `decoration:blur`), and the window asks for it: GTK 4.24 sends the compositor a blur region only for a window with a CSS `backdrop-filter`, and Hyprland treats a window without one as "no blur", so the blur skin has one. Auto is translucent when the compositor advertises the ext-background-effect protocol (or under Hyprland); anywhere else, or with blur Off, the same skin is nearly opaque (window 96 %, sidebar 90 %). While a dialog is open the page under it is blurred (5 px) on a GPU renderer; archmaid-gui's default cairo renderer and the nearly opaque form skip that.
- **The DMS widget follows DMS's blur** (#254). It has no glass switch of its own. When DMS blurs its layers (`Theme.blurLayersActive`), the hero gets an edge and a faint sheen in DMS's primary colour (they show on a white hero too), DMS's corner radius and no corner marks, and its fill goes through `Theme.foregroundColor`, so it is as translucent as DMS's own foreground layers: opaque with DMS's defaults, translucent when DMS's foreground layer transparency is lowered. With DMS's blur off but its popup transparency under 85 %, the unfolded update list gets an opaque backing, so the desktop doesn't show through the rows. With DMS's blur off and the popup opaque, the widget is exactly as before.

### Fixed

- **The window has a minimum height, and resizing it no longer floods the log** (#255). Dragging it small logged "AdwToastOverlay … exceeds AdwApplicationWindow height: requested 508 px" at every step. The sidebar with its meters asks for up to 508 px, and the meters hid only under 480 px, so a window between the two over-requested; they now hide under 540 px. The window is no shorter than 340 px, which the narrow layout fits (the 630x372 half-screen window still works), and its minimum width is 300 px, as narrow as the pages go. The sidebar now scrolls when the window is shorter than its rows (a larger font or text scale made it ask for 588 px at every height on the VM, #258), so it no longer sets the window's minimum. `TestGUIMinHeight` also runs with a larger font and text scale. Known leftover: on the test VM's fonts, a window dragged under about 474 px high still logs the warning a few times (4 lines in a sweep from 700 to 300 px, down from 8 or 9); it is only stderr noise.
- **The sidebar's selection always equals the visible page** (#255). Dragging the window under 760 px wide and then maximising it left Packages highlighted while the header and the page said Overview: the sidebar changing form moves the focus into its list, which selects the focused row. The selection is now the page's alone: one the page didn't ask for is put back, and a click still changes the page. `TestGUINavSync` resizes narrow and wide and forces a drift.
- **A live light/dark switch now changes the state colours too** (#255). On KDE, switching the colour scheme turned the window dark at once, but the light theme's darker state colours stayed: the sudo and root tags, the Last run "ok" tag and the Updates count were dark amber and green on dark grey. GTK reads a `@define-color` inside `@media (prefers-color-scheme)` when the stylesheet is parsed and does not redo it when the scheme flips. The light set (`style_light.css`) and the glass skin's dark tokens (`glass_dark.css`) are now their own stylesheets, added and removed when the style manager's dark flag changes. `TestGUIColourSchemeLive` flips the scheme light, dark, light, dark on a running window and reads the resolved colours.

## [2.4.1] - 2026-11-03

### Changed

- **The Overview's Updates card sits beside Last run** (#253). Last run spans the first two columns, and the Updates card is as wide as the action cells' third column, so its edges line up with "Delete old downloads" below. In a window too narrow for three columns of cells (under about 1264 px), the card sits under Last run at full width, as before. Its two buttons wrap to two lines when the column is narrow.

## [2.4.0] - 2026-11-03

It was all merged into `main` (= `refactor/p0`) and passed the local gate and the test VM CI (`run.sh ci`: core, e2e, gui, bench).

### Added

- **A "Check for updates" button on the Overview** (#252). An Updates card under Last run reads `updates-cache.json`, the file the timer and the DMS widget use. It says "N updates waiting", "Up to date", "Not checked yet" or "Unknown (check is N days old)", with the held count and when it was checked. The button runs the same check as the timer (`systemctl --user start --no-block archmaid-updates-check.service`, else the installed script), as you, with no root and no password. The card shows "Checking…" with a spinner until the file changes, then the new count, or "Check failed" with the reason if nothing changed in 2 minutes or the check could not start. "Run topgrade" appears when updates are waiting. Both buttons are off while a run goes.

### Fixed

- **A Packages removal now counts in `last-run.json`'s `freed_bytes`**, so the DMS widget shows "2.9 MiB freed" for it like the run page's "frees 2.9 MiB"; an install adds nothing (#251).
- **GitHub code-scanning alerts closed.**
  - Both workflows declare least-privilege `permissions`. `ci.yml` gets `contents` and `packages` read only. `ci-image.yml` gets `contents: read`, and its build job keeps `packages: write`.
  - The GUI's ANSI colour parser reads the colon-form colour numbers with `strconv.ParseUint(…, 8)`, so a value over 255 is refused before any conversion.
  - The parser already refused such values, so nothing changes in use. A test now covers the colon form.
- **The GUI status no longer says "DRY RUN" for a real action that follows a dry run.** Every Start now sets the status's dry flag from its own request, as the Packages page already did, so the discovery and plan phases of Full Cleanup, Clear caches, Repair and the others are clean (#250).
- **Stop is hidden while any action's gate, confirm or pick is up.** Full Cleanup's gate, Repair's and Clear caches' confirms and the caches pick showed a dimmed Stop before anything ran, as the Packages sheet no longer does. The password and polkit dialogs and running steps keep Stop (#250).
- **The DMS widget's light-theme state colours now equal the GUI's.** ok `#3d6114`, warn `#75510c` and danger `#a91d36` (at least 4.5:1) replace `#4e7d1a`, `#9a6a10` and `#c4223f`, which missed it in places; dark is unchanged, and a test reads both files so they cannot drift apart (#250).

## [2.3.0] - 2026-11-03

It was all merged into `main` (= `refactor/p0`) and passed the local gate, the test VM CI (`run.sh ci`: core, e2e, gui, bench) and two live checks of the Packages page in the test VM. Those checks did real repo and AUR installs and removals, and tried tampering with the built files.

### Read this first

- **Install the polkit policy with the package.** The Packages page adds three polkit actions (`org.archmaid.helper.pkg-install`, `pkg-install-local` and `pkg-remove`) and six helper verbs. The GUI and the helper must come from the same build, so install `archmaid-git` and `archmaid-gui-git` together, as the README says.
- **Installing from the AUR needs `git` and a build toolchain (`base-devel`) on your machine.** They are optional dependencies of `archmaid-gui-git`, so the packages don't pull them in. Without `git`, the fetch step fails with git's own message and nothing is built.
- **The Packages page is GUI-only.** The TUI has no package page and no `sudo` path for it.

### Added

- **A Packages page in archmaid-gui** (#230–#249). It searches, installs and removes packages from the repositories and the AUR. The design is in `docs/design/packages-contract.md`, and the user guide is the wiki's Packages page.
  - **Search and details.**
    - One box searches the repositories as you type, and the AUR after a short pause. The AUR needs at least 2 characters, and only then does the term go to aur.archlinux.org.
    - Results come in two groups: Repositories (up to 50) and AUR (up to 40). A repo package hides an AUR package of the same name.
    - A package opens in a detail pane (a drill-in below 760 px). It shows repo, version, sizes and dependencies, and for the AUR also maintainer, votes and dates.
    - A dependency that an installed package provides (a soname such as `libcap.so`, or a virtual such as `sh`) counts as installed.
    - The package lists are read from pacman's databases directly, and the page never refreshes them. It re-reads them after its own runs, after topgrade or Full Cleanup, and when it is shown, so changes made with pacman outside archmaid appear.
  - **The install list and the confirm sheet.**
    - "Install" goes straight to the sheet. "Add to list" gathers packages first (up to 32 names), and the sidebar badge shows the count.
    - A list holds either installs or removals, and either repo or AUR packages, never a mix. "Clear the list and add this" swaps it in one click.
    - The sheet lists everything that will be installed, dependencies included, with download and disk sizes. Cancel is the default.
    - The run ends with a summary: "Packages installed" or "Packages removed", the count, and "uses N MiB more" or "frees N MiB". `last-run.json` gets `summary.installed` and `summary.removed`.
  - **What blocks an install.**
    - Pending updates: "N updates are waiting", with an **Update system first** button that starts Run topgrade.
    - A package that replaces or conflicts with an installed one: "remove vim first, then install gvim."
    - An item that would be an upgrade.
    - archmaid never runs `-Sy`, `--overwrite` or `-dd`.
  - **Remove.**
    - It runs `pacman -Rs`. The box "Also delete config files" makes it `-Rns`; the plan is read with `-Rs`, because pacman refuses `-Rns --print`.
    - Never `-Rc` or `-Rdd`.
    - A refusal names the packages: "X is still needed by Y."
    - The helper keeps a protected list and refuses those packages as a target and anywhere in the cascade, checking before it asks pacman. The list is `base`, `filesystem`, `glibc`, `systemd`, `pacman`, `sudo`, `polkit`, the kernels and bootloaders, the owner of every installed `/usr/lib/modules/*/pkgbase`, what `base` depends on, and anything named `archmaid*`.
    - The Installed view greys the same list, read with the new password-free `plan-protected` verb, with "System-critical: archmaid does not remove it."
    - Orphans are listed, but Full Cleanup removes them.
  - **The Installed view.** Explicit, Dependencies, Orphans and Foreign, with a filter. The search start shows "Recently installed".
  - **AUR packages.**
    - They are built as you with `makepkg` in `~/.cache/archmaid/aur/<package base>`: never as root, never with `-s` or `-i`.
    - **Before anything is built**, the sheet shows:
      - the PKGBUILD and the other files of the repo;
      - the diff against the commit you last reviewed ("Changes");
      - hints: version-control or unchecked sources, non-https, an `.install` script, symlinks, binaries, a download piped to a shell, `sudo`, base64;
      - the maintainer audit's findings.
    - Install enables only after you tick "I read the PKGBUILD". When the audit has a high finding, you must also tick "I've read the findings and accept them"; accepting commits the audit snapshot, as Full Cleanup's gate does.
    - Nothing sources the PKGBUILD before you accept.
    - An AUR run stops at the first step that fails, and every later step is skipped. "Recording what you reviewed" happens only after a successful install.
  - **Passwords.**
    - There are three new polkit actions, all `auth_admin` and never kept, so every run asks:
      - `pkg-install` (repo);
      - `pkg-install-local` (AUR; its message says the packages are unsigned and their install scripts run as root);
      - `pkg-remove`.
    - An AUR install with missing repo dependencies has two root steps. The sheet says "You're asked for your password once" when the GUI keeps the password for the run, otherwise "up to twice".
    - A dry run asks for nothing.
    - Stop is hidden while the sheet, the prompt and a root step that can't be stopped are up.
  - **The helper's new verbs.**
    - `plan-install`, `plan-remove` and `plan-protected` run without root.
    - `pkg-install`, `pkg-remove` and `pkg-install-local` run as root. A root verb recomputes the plan under the helper lock and runs nothing if it differs from the one you confirmed ("The list changed. Review again").
  - **`pkg-install-local` reads your files once.**
    - It walks `~/.cache/archmaid/aur/<base>/out/` with `openat(O_NOFOLLOW)` from your passwd home, and checks owner, mode and size on the open file.
    - While hashing, it copies the file into a root-only staging folder (`/var/cache/archmaid/staging/run-*`, mode 0700, removed on every exit) and compares the hash to what you confirmed.
    - pacman reads the staged copy (`-Qip`, `-Qlp`, logged), and `pacman -U` runs on the staged copy only.
    - It refuses a home or `~/.cache` that is group-writable (mode 0775).
  - **New state, cache and staging.**
    - `~/.local/state/archmaid/aur-reviewed.json`: the commit you last reviewed per package base, written only after a successful install.
    - `~/.cache/archmaid/aur`: the fetched repos and builds.
    - `/var/cache/archmaid/staging`, as root.
  - **Network.**
    - archmaid's own code talks to aur.archlinux.org as you, never as root and never from the helper: the AUR search and info, the audit, and the git clone or fetch of a package you chose.
    - `makepkg` downloads the sources the PKGBUILD names, after you have confirmed.
    - pacman, run by the helper, downloads packages as it always does.
  - **A dry run** (Settings) installs and removes nothing. The root steps only print their command, and the AUR build steps skip. The plan is still read for real, and the AUR repos are still fetched into `~/.cache/archmaid/aur`.

### Changed

- **The light theme's tags and state text are darker.** The tags (`root`, `sudo`, `asks`, `OK`, `FAILED`, `installed`, …) and the state text (summary headlines, counters, log outcomes) are now readable: ok `#3d6114`, warn `#75510c`, danger `#a91d36` and running `#2952b2`, each at least 4.5:1 on every light surface (#247). Bars and markers keep the bright Tokyo Night colours, and the dark theme is unchanged.

### Fixed

- **The polkit password dialog says what it authorises.** It showed only the bare action id. It now shows the policy's message plus the id, for Repair, Full Cleanup and the other existing actions too (#244).

## [2.2.0] - 2026-11-02

Everything here is merged into `main` (= `refactor/p0`). It passed the local gate, the test VM CI (`run.sh ci`: core, e2e, gui, bench) and three live checks of the widget in the test VM's DMS (top and bottom bar, light theme, keyboard).

### Read this first

- **Update the package and reload the widget together.** The updates check now writes `updates-cache.json` schema `v:3`. An older widget can't read it and shows no updates until DMS reloads the plugin: `dms ipc call plugins reload archmaid`, or restart DMS. The new widget still reads `v:1` and `v:2` files.
- **The update list appears after the next check.** Until the timer runs again (every 30 min) or a run with topgrade ends, the file is still the old one without a list, and the Updates row has no chevron. To get it at once: `systemctl --user start archmaid-updates-check.service`.

### Changed

- **The DMS widget's popout is redesigned** (#221, the owner's pick of six drafts):
  - A hero box with corner marks says the state in words, in a state colour: "Everything is up to date", "Restart recommended", "Last run failed", "No cleanup for 16 days". Under it are the run's other attention items (a headline is never repeated as a line), then the run's numbers ("47 upgraded · 3.1 GiB freed").
  - Three compact rows follow, Last run, Updates and AUR, each ending in an outlined tag (`OK`, `FAILED`, `10 NEW`, `UP TO DATE`, `STALE`, `NO HIGH`, `1 HIGH`). Text is cut short with "…" instead of wrapping.
  - The state colours are fixed Tokyo Night colours, with a darker set on a light DMS theme. Everything else uses the DMS palette.
  - The update count excludes AUR updates held by the 72 h cooldown, which are shown apart: "9 repo · 1 AUR · 2 held", `10 NEW`.
  - The pill and the two buttons are unchanged.

### Added

- **The list of pending updates in the widget** (#220, #221):
  - Click the Updates row (or press Enter or Space when it has focus) to unfold the list under it.
  - The list groups Repo, AUR and "Held · cooldown 72 h", shows "old → new" per package, and tags `restart` (the kernel, systemd, mesa, nvidia) and `held 1d9h`.
  - It is at most 8 rows high and scrolls beyond that. It says when it was checked and how many were not listed, and folds again when the popout closes.
  - It is read-only: nothing in it opens or runs anything. It shows only while the count is fresh (3 days).
- **`archmaid-updates-check` writes the packages it found** into `updates-cache.json` (`v:3`, `packages`, at most 200), next to the unchanged counts (#220).
  - Only names and versions that pass a strict character allow-list and length cap are listed. Anything else (odd AUR text, colour codes, quotes) is left out of the list but still counted, so the JSON can't break.
  - The widget checks every entry again before showing it.
  - If the list can't be built, the counts are still written.

### Fixed

- GitHub CodeQL can now parse the widget's `state.js`: its QML-only first line `.pragma library` is gone. The file is plain JavaScript, and a test keeps it that way (#221).

## [2.1.1] - 2026-11-01

### Changed

- **archmaid is now licensed under GPL-3.0-or-later** (`LICENSE`; the PKGBUILD's `license` and the metainfo's `project_license` say so too).
- The docs are brought in line with the public repo and the new wiki (https://github.com/Enzord758/archmaid/wiki): the merge-time security review is marked as history (the wiki's Security-Model page is current), the 72 h wording says archmaid holds nothing back, HANDOVER names the updates-check timer's network use, INSTALL and the packaging README are corrected, and the original widget design in `docs/dms-widget.md` is marked superseded (#216).

### Fixed

- **The updates check and the DMS widget use the same state folder as the app** (#216): `archmaid-updates-check` wrote `updates-cache.json` under `$XDG_STATE_HOME/archmaid` and the widget read from there, while archmaid itself always uses `~/.local/state/archmaid`. With `XDG_STATE_HOME` set elsewhere the widget missed the last run. All three now use `~/.local/state/archmaid`; a test runs the script with a different `XDG_STATE_HOME` and fails if it is written to.
- **Clear caches: the Overview cell's "Measuring the caches…" line goes away when the measuring ends** (#211): with the pick open it showed the cell's normal lines again (the status line already said "Waiting…"); it stayed until the pick was answered.

## [2.1.0] - 2026-10-22

Everything here is merged into `main` (= `refactor/p0`, r647) and was tested by the owner; it passed the local gate and the test VM CI (`run.sh ci`: core, e2e, gui, bench).

### Read this first

- **Upgrading from 2.0.0 is the usual PKGBUILD build** (README → Install): rebuild and install `archmaid-git` and `archmaid-gui-git` together. Nothing to migrate.
- **`last-run.json` stays schema `v:1`.** The new `summary` object is additive; older files simply lack it, and old history records with `usercache` or `wipecache` keep their old names.
- **The DMS widget may need a restart of DMS to show the new LAST RUN line** ("47 upgraded · 3.1 GiB freed" and the warning line); until then it keeps showing the old one.
- **The menu changed:** "Clear caches" replaces "Clear user cache" and "Delete entire package cache", and Full Cleanup no longer wipes the package cache (it still trims it to 3 versions).
- `archmaid --version` now exists, and the GUI shows the version under Settings, About.

### Added

- **The end-of-run summary (variant C) in the TUI, the GUI and the widget, and `summary` in `last-run.json`** (#188, #191; 0446a13, cfba0d4, 56a99fd, 0e9045b, 3aeaea1, 13055c7, 2806249):
  - **TUI and data (#188).** The TUI's summary screen now shows what the run changed: packages upgraded (with how many came from the AUR), installed and removed, the space each cleaning step freed and the total, what the package cache still holds (and that "Clear caches" in the menu frees it), and a "Needs your attention" list: a restart (the running kernel was replaced, or systemd, mesa or the NVIDIA driver were upgraded), new `.pacnew`/`.pacsave` files, a high AUR audit finding. Package changes come from `/var/log/pacman.log` since the run started (another pacman run at the same time counts too); sizes are measured as the user before and after each step, and a folder it can't read says "unknown". A new package `internal/report` makes it, the Driver adds it (`engine.RunReport`, opt-in through `Deps.RunReport`; the TUI and, since #191, the GUI set it). AUR updates held back by the 72 h yay cooldown are not shown: no reliable source after the run (`docs/HANDOVER.md`, "The run report").
  - `last-run.json` gets a `summary` object (`upgraded`, `aur`, `freed_bytes`, `attention`) when the run has something to report; the schema stays `v:1`, older files simply lack it. `state.js` reads it, and the DMS widget's popout shows it (below).
  - **The GUI's Run page shows the run report (#191).** When a real run ends with a report, the finished page opens with a hero card ("NEEDS YOUR ATTENTION" or "ALL QUIET", one headline, "47 packages upgraded · 3.1 GiB freed · 14 m 52 s", and each thing that needs you with its detail), then Packages and Space freed side by side (stacked in a narrow window), the AUR audit line when it found something, and "Steps and output" folded below, one click away. A .pacnew or .pacsave row has a "Copy pacdiff command" button that copies `sudo pacdiff`; nothing in the summary runs a command. A dry run, an action without a report and a report without data keep the page as it was.
  - **The DMS widget's LAST RUN shows the run report's numbers (#191):** "47 upgraded · 3.1 GiB freed", and a warning line ("⚠ Restart recommended · 1 new .pacnew") only when the run left something that needs you. An older `last-run.json` without `summary` shows nothing new.
  - `testenv` also traps `pacman-conf` and points `ARCHMAID_PACMAN_LOG` and `ARCHMAID_MODULES_DIR` at nothing, so no test reads the machine's pacman log.
- **`archmaid --version` and an About line in the GUI's Settings (#204).** `archmaid --version` prints `archmaid 2.0.0-3-g6ba8744` and exits 0 like `--help`: nothing is read from or written to the state folder, nothing is rotated (`TestStartupDeletesNothing` covers it). The GUI's Settings page ends with "About: archmaid <version>". The version comes from a new `internal/version` package: `Version`, set at link time (`-ldflags -X`) by the PKGBUILD and `install.sh` from `git describe --tags --dirty` (`rN.sha` when git has no tag), else the commit Go stamped into the binary (`vcs.revision`, `-dirty` if modified), else "unknown". `pkgver()` is unchanged. The gate's check that the packaging sets no link-time variable now allows exactly this one. `archmaid-helper` has no `--version`: its argv is a protocol the root helper parses, and it is left as it is.

### Changed

- **"Clear caches" is one menu action, replacing "Clear user cache" and "Delete entire package cache" (#199, 485d8d9).** The package cache was briefly its own menu action (#186, 380bce7) before it was folded in here, so it is said once, as it ended up: (owner's decisions, 2026-10-18). Full Cleanup already trims the package cache to the 3 newest versions, so it has one step and one confirm fewer: 8 steps in the TUI (9 with hyprpm), and in the GUI the root call covers 5 steps, not 6. Clear caches first measures both caches as you, read-only (`~/.cache` with a bounded walk, the package cache with `paccache -dk0`), then opens a pick, like "Disable unused services": two rows, nothing ticked, "~/.cache — 2.3 GiB" and "Entire package cache — 7.3 GiB (1398 files)" with the warning that you can't reinstall or downgrade those packages without internet on the row. A row with nothing in it, or that couldn't be measured, is disabled and says why. The pick is the confirm: only what you tick runs, `~/.cache` as you, the package cache as root (sudo in the TUI, the existing `paccache-wipe` helper verb through polkit in the GUI, so the polkit policy is unchanged). The password is asked only after the pick and only if the package cache is ticked; `~/.cache` alone, an empty pick or a cancel ask none. The run report headline is "Caches cleared"; Full Cleanup's cache line now says "Clear caches" in the menu frees it. Old `last-run.json` and history records with `usercache` or `wipecache` still show their old names. The log category "Package cache check" (`pkgcache`) now holds this action's measuring record.
- **The Overview cells are short** (at most 40 characters; owner's decision, 2026-10-18): "Pick ~/.cache, the package cache or both", "Reinstalls packages with missing files", "Files untouched for 30 days", "AUR audit, then a full update", "Enabled services that aren't running", "Read-only check against the AUR". Six cells and the Full Cleanup hero again. Full Cleanup's hero text is unchanged.
- `archmaid-helper full-cleanup` no longer takes `--wipe` (it exits 2 as for any unknown flag); `paccache-wipe` stays a verb of its own.
- **Clear caches with both rows ticked clears `~/.cache` first and asks for the password only before the package cache (#204, TUI).** The TUI used to ask for the sudo password before `~/.cache`, the GUI after it. The Session now runs a pick's steps that need no root first and settles root (probe, faillock read, prompt, keep-alive) only when the first root step is next (`lateRoot`, `internal/engine/queue.go`); only this one queue shape is touched: a pick-built queue of two or more steps whose first needs no root, with no gate and no confirmed root step (today: Clear caches, both ticked). Everything else keeps its flow: the wipe alone (probe up front), `~/.cache` alone, Full Cleanup, every other action, dry runs and polkit. A cancelled late prompt skips the rest and ends the run aborted, with `~/.cache` reported as done; three wrong passwords refuse the wipe as before (incomplete run).
- **Clear caches with both caches empty is a popup, not the Run page (#207, owner finding on r643).** The GUI answers with an Adw dialog over the page the action was started from, "No cache to clear" with the line "~/.cache and the package cache are both empty.", and stays there: no Run page, no "Discovery" step, no toast. Clear caches no longer takes the Run page while it measures or while its pick is open either: the window stays where it was (status line "Measuring the caches…") and switches to Run when the queue of a non-empty pick starts; a cancelled or empty pick leaves the user there with the toast. The TUI's notice says the same ("No cache to clear: ~/.cache and the package cache are both empty."; it was "Nothing to clear. ~/.cache: Empty: nothing to clear. Package cache: Empty: nothing to delete."), still a notice screen. A look that failed keeps the Run page's failed Discovery and the TUI notice with each cache's reason (now starting "No cache to clear."). The engine's records are unchanged and honest: last-run.json says `nothing-found`, the discovery log has its CHECK line, no run record. Every other action's "nothing found" notice is unchanged (they report what a check found, on the Run page, with a log to read; only this one is a question the user answers with OK).
- **The Clear caches pick's `~/.cache` row says "Hidden files are kept" (#204; owner's decision: dot entries stay).**
- **The GUI's status bar says "Waiting…" behind a pick or a confirm (#204),** not the discovery's "Measuring the caches…" (it also did for a step's confirm inside a running queue, where it said "Running"). The destructive pick's "Clear" button is no longer the dialog's default response: Cancel is (Dev 3's review of #200).
- A pick whose `Rows` don't match its `Items` in length counts every item as disabled in the engine (Dev 3's review of #200); a pick with no `Rows` (services) is unaffected.
- **The TUI's summary says what the action did (#204):** "✔ Caches cleared" (and the same short outcome for Repair, downloads, services and the AUR audit) in place of "Cleanup completed successfully"; Full Cleanup, topgrade and anything else unchanged. The wording is one map, `actions.DoneHeadlines`, shared with the GUI's hero.
- **A step's title row is the target on the finished Run page (#207, owner finding on r643).** Hover and keyboard focus used to land on the line under a folded step's title (its summary, "[warn] valent 0 votes"), which does nothing, and not on the title row that folds and opens the step. Now the title row takes the hover highlight and the focus ring, Enter or Space folds or opens it, Up/Down/Home/End move between steps' rows, and Tab into the list lands on one; the summary line is neither focusable nor activatable, and no output line is activatable.
- **CI moves off GitHub (#209).** `ci.yml` and `ci-image.yml` keep only `workflow_dispatch` (no push, pull request or schedule), so nothing runs or bills unless started by hand. CI of record is the test VM, `~/archmaid-vmtest/run.sh ci <sha>` (core, e2e, gui, bench on a clean Arch install), run after the local gate; `docs/TESTING.md`, `docs/merge-to-main.md`, `docs/HANDOVER.md` and the README say so.

### Fixed

- **The TUI's Full Cleanup no longer lets sudo prompt on the terminal in the AUR cache step (#194).** `yay -Sc` runs `sudo pacman -Sc` itself, but its step got neither the askpass nor the sudo wrapper, so with a sudo ticket that couldn't be reused (`timestamp_timeout=0`, or one that had lapsed) sudo asked for the password at the foot of the TUI screen, hung until someone typed, and the run ended failed (found live in VM round #192; a reusable ticket hid it before). The step is now `NeedsPriv` in the TUI, like topgrade: its sudo asks through archmaid's popup. Under polkit (the GUI) it stays the plain user step `yay -Sc --aur`. The sweep found one more: `hyprpm update` elevates itself with sudo by name, so it gets the same askpass and wrapper through a new `Step.SudoByName` (no up-front password, no root tag, may still run last); nothing else the TUI starts could reach sudo without it. A static test now fails when a step whose program can reach sudo has neither flag.
- **The measuring cap counts walking time, not time since the report was created (#193, 02f475e).** The run report's bounded measuring of a step's folder could be used up before the walk began.
- **The confirm or pick comes before the password (#196, #204; 3293869, 42cf829, 0987d62).** A queue of one confirmed root step with no gate defers the sudo probe and password until after a yes, so a no costs no password and no faillock count. The security reviews (#197, 66cfe7a; 0987d62) pinned the predicate: `NoTicket` after steps ran ends aborted, and the TUI's late password keeps the run behind it after a wrong try.
- **The finished Run page's hero fits the action (#195, 7b19329):** a clean non-update action says DONE and what it did instead of ALL QUIET / "Everything is up to date"; Full Cleanup and topgrade are unchanged.
- **The GUI redraws its status line when a prompt opens or is answered, so "Waiting…" shows behind the pick (9067610, #204).**
- Test flakes fixed, the coverage-exempt line shifts followed (310a708, 0987d62), and `check.sh`'s `-X` guard rejects a second `-X` on the allowed line (0987d62).

## [2.0.0] - 2026-10-18

The refactor/p0 work, merged into `main` (fast-forward, `main` = `refactor/p0` = 4fdc62b). The package version stays `rN.sha` from `pkgver()`; the tag `v2.0.0` marks this release.

### Read this first

- **The TUI is new underneath.** It now runs on the engine and the real Driver, and the old queue logic is deleted. It behaves the same (all 31 transcript goldens, the view goldens and the 9 e2e checks are identical), and it has been run live, by the owner and in the test VM (`docs/first-live-run.md`) (4a2fb23, 1fe2148, 7652f35).
- **Install both packages from `main`, together:** `archmaid-git` (the TUI, helper, polkit policy, widget and updates timer; a complete install on its own) and `archmaid-gui-git` (the GUI, which needs the exact `archmaid-git` of the same build). Build and install: README → Install, `packaging/arch/README.md`. Both are installed on the owner's machine.
- **Only if you still have an old `install.sh` build:** remove `~/.local/bin/archmaid` with `~/archmaid/install.sh --uninstall && hash -r` (README → First run). It comes first on `PATH`, prunes run folders at every start and rewrites the new AUR snapshot in the old format. A fresh install needs nothing of this.
- **One password per GUI run is in this release** (`internal/privilege/kept.go`, `gui/internal/polkitagent`): the first password you give is kept for the run and answers the later sudo and polkit prompts; the dialog and the Proceed sheet say so, or say why it is not kept. Under polkit, Full Cleanup's root steps are one `full-cleanup` helper call with one dialog.
- **The GUI (`archmaid-gui`) has been run live**, on the owner's desktop and in the test VM. Only the GUI shows in the app launcher; the TUI has no desktop entry and no icon. The DMS widget opens the GUI when it is installed.
- After updating, restart the widget's timer: `systemctl --user restart archmaid-updates-check.timer`. On a first install enable it (`systemctl --user enable --now archmaid-updates-check.timer`) and add the widget in DMS (`docs/dms-widget.md`).
- If the engine stops unexpectedly (an internal panic), the TUI now shows "archmaid's engine stopped unexpectedly: ..." and any key quits; the full reports go to stderr. Ctrl-C always gets you out, also from a password check that never answers (5de7e0c, 73b22cd).
- `archmaid --help` now prints help and exits 0, and unknown options now exit 2 instead of being ignored (13eb21a).
- History rotation now keeps 5 runs including the current one, instead of 5 old ones plus the current one (420733b, 13eb21a).

### Security

- Output from topgrade, yay and makepkg is stripped of terminal escapes (title changes, OSC 52 clipboard writes, cursor moves) before it reaches the pane and the logs; older logs are shown plain too (5fa1b8b).
- The sanitizer also drops bidi overrides, zero-width marks, SGR conceal, U+2028/2029 and Hangul fillers (9809765, 9a9cda0).
- The AUR audit fails closed: an unreadable or corrupt `aur-maintainers.json` is a high finding instead of a silent "first audit", and the bad file is kept (4265bb7).
- The AUR snapshot is written atomically (0600 temp file, fsync, rename) under a lock (4265bb7).
- AUR RPC replies are capped at 8 MiB, results for packages nobody asked about are dropped, and maintainer names are sanitized before display and logging (fb691b5, 9809765).
- A newly added AUR co-maintainer now triggers the gate, not just a changed maintainer (0ed25f2).
- `SUDO_ASKPASS` goes only to steps that use sudo, lives in a private 0700 directory under `XDG_RUNTIME_DIR` (askpass is off without it), and sudo steps run `sudo -n` (f03acdf).
- The askpass popup answers only the running sudo step, after checking the connecting process (uid, start time, executable, sudo parent, descendant of the step), and names the step and sudo's caller (09c192e, 48fa52e).
- A password typed into a mid-run askpass popup is checked with `su` before it reaches sudo, so two typos no longer burn the pam_faillock budget (deny=3) (d71ef0f).
- The `su` check no longer starts the login shell (`su -s /usr/bin/true`), so `~/.zshenv` is not read (d71ef0f).
- Logs are written 0600 in 0700 directories; looser permissions from older versions are tightened on the next write (c2bbb80).
- Package and unit names are passed after `--`, so a name starting with `-` cannot become a pacman or systemctl flag (dcbcef1).
- "Delete old downloads" refuses `$HOME`, `/`, any ancestor of `$HOME`, and the passwd home even when `$HOME` points elsewhere or is unusable (ab7ceec, ef94e67).
- "Delete old downloads" deletes through `os.Root`, so a swapped-in symlink cannot escape the folder (a7a8478).
- "Delete old downloads" never crosses a mount point (USB disk, sshfs, bind mount) below the folder, and re-checks every node's age as it removes it (002567f, ef94e67).
- `--downloads <value>` starting with `-` is an error (exit 2) instead of silently swallowing the next flag, e.g. `--dryrun` (ef94e67).
- sudo is refused when it could prompt on the terminal: `proc` only starts a sudo whose first argument is `-n` or `-A`, and a sudo without an explicit environment does not inherit `SUDO_ASKPASS` (0e1be6e).
- The askpass popup answers only a request that passed `Verify`; any other request is refused before `su` runs, and only the askpass server can create a request (1832e53).
- Passwords containing a line break are refused by `privilege` itself, and su's lockout line is sanitized and clipped (c423d4d).
- Safe defaults: `sanitize` defaults to plain (SGR only on request), `logstore` writes only its four categories and refuses a missing or relative state dir, AUR error text is sanitized and capped, an unparsable euid reads as unknown rather than root (c423d4d).
- The downloads home guard always includes the process's own `$HOME` and the passwd home, even when the caller passes none; with no home known, `--downloads` is refused (7de100c).
- The AUR audit records unacknowledged high findings in `audit-state.json` for the widget (f5e4def).

### Fixed

- Aborting a running step leaked three goroutines and a pipe per step (15 after 5 aborts); they are now released (e46021c).
- Every finished action or editor return started another permanent 4 s status loop, and the sudo keep-alive doubled the same way; there is now one of each (c974117).
- Opening View log no longer creates an empty run directory that pushed real history out of the kept runs (c2bbb80).
- Declining the AUR gate no longer "spends" the alarm: the snapshot is saved only when you accept, so the same change gates again next time (b258af4).
- Late output from an aborted step can no longer appear in, or finish, the next step (caf81b3).
- An aborted AUR audit now stops at once instead of running on for up to 15 s per request (ff6a515).
- "Delete old downloads" deleted the Downloads folder itself once it was a month old; the folder is never a candidate now (a7a8478).
- Downloads age uses ctime, so a fresh download that `wget`, `curl -R` or `unzip` stamped with an old mtime is no longer deleted (a7a8478).
- The downloads folder is found when it is `~/Download` or `XDG_DOWNLOAD_DIR` is `$HOME/` (ab7ceec).
- An abort during the downloads deletion is still written to `cleanup.log` (62b7e2c).
- Services discovery no longer mistakes the leading "●" of a not-loaded unit for a unit name (bdf4027).
- A line over 1 MiB no longer leaves the child blocked on a full pipe (e46021c).
- A step whose child left a background process holding its output pipe no longer stays "running" after it exits; the pipe is read for at most 2 s more (d7c3eb4).
- Orphans are found after topgrade, so ones the upgrade leaves behind are included and the count in the confirm is current; Enter no longer freezes 130-200 ms (eeba326).
- Repair's scan timeout no longer reads as the audit's 20 s deadline, and notices honour the output tail (32c01a8).
- The helper's `disable-unit` no longer refuses bluetooth or avahi-daemon because of their D-Bus aliases (614f49b).
- The helper's `plan-orphans` accepts orphans with uppercase names (acad283).
- `archmaid --help` used to start the TUI and, through the startup rotation, delete the oldest real run folder. Nothing is deleted at startup now; `TestStartupDeletesNothing` guards it (13eb21a).
- The history rotation never deletes the run folder of the run that is writing, even if the clock was set back (420733b).
- The log store is safe to use from any goroutine, and tests wait for every audit prefetch they start (d4e9935, 6c7918f).

### Changed

- The DMS widget's pending-updates check now counts AUR updates too and runs every 30 min (was hourly): `archmaid-updates-check` runs `yay -Qua` (as the user, no sudo, stdin from /dev/null, 120 s timeout) next to `checkupdates` and writes `updates-cache.json` schema v2 (`count`, `repo`, `aur`, `aur_held`; `aur` is null when yay is missing or fails). AUR updates modified within 72 h (`audit.Cooldown`; archmaid only counts them apart and holds nothing back: a 72 h hold is up to your own yay setup, e.g. a yay hook, which archmaid does not ship) are left out of the count and shown as held in the popout ("9 repo · 3 AUR (2 held, <72h)"). The widget still reads v1 files. Restart the timer after updating: `systemctl --user restart archmaid-updates-check.timer`.
- GUI: the six Overview action cells now light up in the accent colour on hover (a little stronger while pressed, and on keyboard focus), so they read as buttons; the last-run cell and the sidebar are unchanged.
- Packaging: only the GUI shows in the app launcher. `archmaid.desktop` (the TUI) now has `NoDisplay=true`; the `archmaid` command still starts the TUI.
- Packaging: the TUI now has no desktop entry at all (it was hidden before). The package no longer installs `archmaid.desktop`, `install.sh` no longer writes one and removes a stale one of its own (on install and `--uninstall`), and `ARCHMAID_TERMINAL` is gone. Only the GUI has an entry; run `archmaid` from a shell. `scripts/check.sh` guards it.
- Packaging: the TUI has no icon either. Nothing used the icon name `archmaid`, so the package and `install.sh` no longer install `apps/archmaid.svg` (the GUI keeps its app-id copy); `install.sh` removes an older copy on install and `--uninstall`, only if it is a regular file identical to `packaging/archmaid.svg`. `scripts/check.sh` guards it.
- Docs: the GUI speed budgets (first frame, idle RSS, throughput, zero drops) are measured by hand on the owner's desktop with `ARCHMAID_GUI_PERF=1`, CI checks correctness only; the merge checklist no longer counts "four jobs" (core and gui come in pairs, one of each runs).
- `archmaid --help` (or `-h`) now prints the usage and exits 0. Before, it started the full TUI. An unknown option or a stray argument now exits 2 with a message instead of being ignored (13eb21a).
- History rotation now keeps 5 runs **including the current one**: you see 5 run folders, not 5 old ones plus the current one (6 before). The rotation happens when a real run writes its first log entry, not at every start (420733b, 13eb21a).
- A dry run, `--help`, a bad option and a start that ends at the menu no longer delete anything from the run history (13eb21a).
- A run in which sudo gave up part-way (three wrong passwords) is now recorded as `incomplete`, not `ok`, with `steps_refused` in `last-run.json` (additive, still `"v":1`) (96911ea).
- An aborted run is recorded with the running step as Aborted and the later steps as skipped, so `steps_skipped` is no longer 0 (6c82bd5).
- When a step result is refused, the step it was meant to gate (for example topgrade behind an AUR finding) still asks for a confirm instead of running unasked, and a refused result no longer logs a false "aborted" (61771a2).
- The AUR audit never saves a snapshot from a failed, cancelled or empty audit, which used to wipe the baseline (79f5bca).
- The root helper no longer has the `upgrade-repos` verb or its polkit action; topgrade stays the only update path (f4bb885).
- Full Cleanup cleans `/tmp` and `/var/tmp` with `sudo systemd-tmpfiles --clean`, so your tmpfiles.d rules decide instead of a blanket 2-day delete; the step reads "Temp files cleaned per tmpfiles.d rules" (a5988bd).
- "Delete old downloads" picks the folder from `--downloads <dir>` (or `=<dir>`), `$ARCHMAID_DOWNLOADS`, `XDG_DOWNLOAD_DIR`, `~/Downloads`, `~/Download`; the notice and confirm name it, and a bad `--downloads` is reported, not skipped (ab7ceec).
- "Delete old downloads" lists the entries it will delete (ten, then "...and N more"), writes what was deleted and kept to `cleanup.log`, and a dry run reports `would delete: <name>` (a7a8478).
- The AUR audit gate also runs before standalone "Run topgrade", with the same confirm (default No) (33870bc).
- The AUR gate commits on accept; in the menu audit, high findings keep their old maintainer until you press `a` on the report (b258af4).
- Repair scans first as your user and only asks for a password if something looks broken; if nothing is, it ends with "No broken packages found." without touching sudo, and a root recheck drops false positives such as `amd-ucode` under the 0700 `/boot` (ae081b1).
- Repair no longer probes the sudo ticket twice (aa55868).
- Full Cleanup and "Run topgrade" start the AUR lookup while the password prompt is open (707be81).
- The status line shows available memory as "RAM avail:" instead of `MemFree` (d73c9e5).
- The integrity check uses `pacman -Qkq`, so only broken files are printed (a58ed79).
- The output pane shows the newest 2000 lines and no longer jumps to the bottom when you have scrolled up; the full output still goes to the log (1f2b703).
- Building needs Go 1.25 for `os.Root` (a7a8478).
- The install script builds with `-trimpath -ldflags='-s -w'` (50525fd).

### Performance

- `BenchmarkViewRunning` guards the running-screen render at a fixed 600 us; `BenchmarkActivateFullCleanup` (~5 us) guards a 1 ms limit so choosing Full Cleanup never runs pacman on the UI thread again (869bfd0, 7c712bc, 9332d02).
- Status line (every 4 s): nine forked processes (~6 ms each) replaced by native reads, about 28 us and no children (261fd0f).
- Streamed output: 5k lines 976 ms -> 5.4 ms (1.79 GB -> 15 MB allocated); 20k lines 5.16 s -> 21 ms (9.7 GB -> 63 MB) (1f2b703).
- Repair scan: ~850 ms for a root `pacman -Qk` over 1422 packages -> 316 ms as user with 16 workers, plus ~50 ms for the root recheck (ae081b1).
- Services discovery: 430 ms for `systemctl list-unit-files` -> 3 ms for `list-units` plus one `is-enabled` (bdf4027).
- Stripping a 5k-line coloured log takes 0.29 ms (5fa1b8b).
- Binary size 12.4 MB -> 8.3 MB (50525fd).
- The orphan queries (130-200 ms) no longer run inside Update when choosing Full Cleanup (eeba326).

### Added

- `archmaid-helper`: a root-side verb engine (`check-files`, `paccache`, `paccache-wipe`, `plan-orphans`, `remove-orphans`, `repair`, `vacuum-journal`, `clean-tmp`, `disable-unit`, `full-cleanup`) with JSON-lines v1 output, exit codes 0-5, an install-location check and a hardened process; its tests need no root. The TUI and the GUI call it through polkit and `helperclient` as of the later entries (a3a30a0, dcf6afc, 5eb3fbb).
- Helper `disable-unit` refuses firewall, MAC/audit, NVIDIA sleep, sleep-target, bus, serial-getty and power units (firewalld, nftables, apparmor, auditd, tlp, upower ...), checking names, D-Bus aliases and `[Install]` (Alias, WantedBy, RequiredBy, UpheldBy, Also) the way systemd parses it (88412f7, 614f49b, 35ea7ba).
- Helper `disable-unit` also refuses when an `Also=` companion is still active or a running or enabled socket/timer/path would restart the unit (54f4b66).
- Helper `repair` reinstalls a package only at its installed version and only when the whole transaction is exactly the broken set, so it can never cause a partial upgrade (b0ad2cd, 34db02f).
- Helper children run in their own session, and the helper catches (does not ignore) terminal signals, so it always writes its result line while pacman keeps default signal handling (f084504, 799403f; replaces e8e863e).
- polkit policy `org.archmaid.helper.policy`, one action per root verb: `check-files`, `paccache`, `vacuum-journal`, `clean-tmp` use `auth_admin_keep`; `repair`, `paccache-wipe`, `remove-orphans`, `disable-unit`, `full-cleanup` use `auth_admin`; never for inactive or remote sessions. The README documents what `auth_admin_keep` allows (1f8d70d, 52ae9d3).
- `packaging/arch/PKGBUILD` (`archmaid-git`) installing `/usr/bin/archmaid`, `/usr/lib/archmaid/archmaid-helper` (never setuid), the policy, an icon and `archmaid.desktop`, plus a packaging README (397c321, cb3ab76).
- `last-run.json` in the state dir records how the last run ended (ok, failed, aborted, cancelled, incomplete) and its action; a dry run writes its own copy (fff5b1d).
- A transcript harness and 31 characterization transcripts of every menu action, recorded end to end against shims (8bfc04e, 6760abf, d53580b).
- Rendered-frame goldens for the summary, popups, password prompt, log picker, audit report and askpass popup (efbc4f1, 09c192e).
- Askpass tests (coverage 0% -> ~98% at the time) (75bce79).
- Four end-to-end VHS tapes (clear-cache, services, orphans, abort) run against shimmed commands (f2a6f49).
- `scripts/check.sh` gate: race tests three times, vet, gofmt, a layering check and coverage floors (89.1% total, 80% for `askpass.go`) (e58563a, 91fb0bf).
- A root `Makefile` (`make all`, `gate`, `e2e`, `perf`) and a GitHub Actions workflow running them, with the core job as an unprivileged user (530845d, 7d42232, 3df53de).
- Perf guard: `ARCHMAID_PERF_GUARD=1` (or `make perf`) fails the gate if the stream or status benchmarks exceed 3x their baseline (60c5761).
- `TestNoSetsid` guards that TUI children share archmaid's session, which sudo's ticket depends on (48265e5).
- `packaging/polkit/LIVE-TEST-AGENT.md` and a TESTING.md plan for the manual polkit/pam_faillock check (2161d33, 283772c).
- `docs/polkit-live-test.md`: results of the live polkit/faillock run: one failure per wrong password and per cancelled dialog, pkexec exits 127 for every refusal, and an action without `exec.argv1` can capture calls meant for the verb actions (ours has none; `TestPolicyMatchesVerbs` enforces it) (15d4d4a).
- `docs/dms-widget.md`: design of the DMS bar widget, with the owner's decisions recorded (7106f85, b321ec9).
- DMS bar widget `archmaid` (`packaging/dms/archmaid/`): a pill with the pending-update count, tinted red for an unacknowledged high AUR finding or a failed run and orange for incomplete, aborted or cancelled runs or a last run older than 14 days; a popout with the details and Run Full Cleanup / Log folder buttons. It only reads the state files (f1655bc).
- User timer `archmaid-updates-check.timer` (no root): runs `checkupdates` 3 min after login and hourly and writes `updates-cache.json` in the state dir. The package installs the widget to `/etc/xdg/quickshell/dms-plugins/archmaid/`, the units to `/usr/lib/systemd/user/` (41b3c9c, dc856e3). Enable steps are in `docs/dms-widget.md` (e22c4f8).
- `privilege.FaillockStatus(user, now)` reads pam_faillock's tally and config (read-only, runs nothing) so a front end can refuse to prompt a locked account and warn before the last allowed attempt (d718dd0).
- `internal/engine`: the frozen engine API (Session, Input, Output, Event, Effect, Driver, `Secret` that never prints) for the TUI and the GUI; the Session was only an API at this point (it is complete as of the later entries). `internal/engine/stub` is a scripted Driver with a ready-made Full Cleanup run (b295907).
- `actions.Env.Validate` names every required field left nil; the TUI refuses to start on an incomplete Env, and `Report.Accept` replaces an untyped `Audit` (1dc3241).
- CLAUDE.md: rules for agents (never start archmaid against the real environment, no privileged commands, write only in the worktree, bound every background process, hand in through the gate) (24fc249, 0a4bb37, 8540b78).
- `internal/testenv` puts traps for `su`, `sudo`, `pkexec`, `runuser`, `faillock` and `pkttyagent` first on PATH; a test that reaches one fails its package. `TestEveryTestPackageRunsUnderTestenv` and `TestNoAbsolutePrivilegedExec` keep new packages wired (f932158).
- The helper's verbs and the `steps.Verb*` constants are checked against each other, and any step after hyprpm that needs root fails the tests (4e19c69).
- `scripts/state-guard.sh`: `check.sh` and `e2e/run.sh` fail if the real `~/.local/state/archmaid` changes; the e2e harness traps pkexec, runuser and faillock and runs the binary under a scratch `XDG_STATE_HOME` (b19256f, 9613503).
- `make bench` / `scripts/bench-compare.sh` and a CI `bench` job comparing base and HEAD with benchstat; documented in `docs/TESTING.md` (5135b92, 6a4794c, 67dd62e).

### Added (since 8540b78)

- The engine is complete: `engine.Session` runs queues, authentication (password, faillock, polkit), mid-run askpass for the running sudo step only, Abort from a step, a prompt or a wait, discovery pick lists and reports, last-run records and all 31 scenarios; confirms are labelled by what they do and the gate confirm previews its plan (faaa289, 63417ba, beedf24, 2bbd4e7, 042a2ec, 956606a, 71db9a3, d9a55e0).
- `internal/driver`: the real `engine.Driver` around a Session (sudo path), used by the TUI and the GUI (26a4c91).
- `helperclient`: runs a helper step through `pkexec` and maps its JSON lines to engine Inputs (af11008, 8d036dc). Under polkit, Repair rechecks its candidates through the helper's `check-files`, which now takes package names and ends in a check event (bf97a0a, 916a88d, b732a88, 771310c).
- Under polkit, orphans are removed from the plan the user saw: the step carries the plan's hash and `remove-orphans` is run from it, and the client's plan is tied to the helper's own (a508c72, c8452ec, 2a71f74, 3b647b9).
- `archmaid-gui`, a GTK4/libadwaita front end in `gui/`: run page, decision dialogs for every prompt kind, overview, logs and a Settings page (downloads folder, dry run), output in the dank16 colours, steps an abort left shown as not run, and "Waiting for authorisation..." on polkit steps. It runs on the real Driver with polkit and its own askpass relay; `--stub` keeps the scripted stub. It renders with cairo unless `GSK_RENDERER` is set (5e5fa7a, fb01c7b, b5f40e6, 8735472, 258b789, 1bd7257, 2e5d39a, bf9dad0, 67dc330, 4d5ba8c).
- Split packaging: `archmaid-git` (TUI, helper, policy, widget, timer; no GUI toolkit) and `archmaid-gui-git` (GUI, desktop entry, AppStream data, icon; depends on gtk4, libadwaita, libgirepository and the same archmaid-git). The DMS widget opens `archmaid-gui` when it is on PATH and falls back to the TUI (afc0991, 4a1ca95).
- Test infrastructure: the contract checker is stricter (AuthWaiting before its AuthResolved, plan changes after their step's StepDone, every Session Output checked as it comes); `sessionTarget` and `driverTarget` replay every scenario through the Session and the real Driver; an askpass test goes through the TUI's real socket into the real Driver; `e2e` has 9 checks (tapes for repair, downloads, the declined gate and `--help`/bad flag were added); the gate keeps its full race-test output when it fails; the headless GUI harness and a `gui` CI job (9777208, 1660c04, 880a299, d4c9705, 5cef060, 20e36d0, f422a39, 3bc1119, 0fc75f2, 392a05e, 563d046).
- `docs/polkit-keep-session-check.md`: a live check of `auth_admin_keep` inside a real session, with an optional part on a pkexec killed in the dialog (e1125c5, 8fcd6da).

### Changed (since 8540b78)

- `cmd/archmaid` is a thin main (command line, askpass relay, building the Session and Driver); the TUI lives in `internal/tui`, with its goldens. No packages under `internal/` or `cmd/` may import charmbracelet directly (7652f35).
- The TUI's model is derived from engine Events and no longer runs actions itself; behaviour goes into the engine, never back into `update.go` (65ac47c, 4a2fb23, e5e1e82).
- The menu audit stops with the Driver: quitting during "Audit AUR packages" no longer waits up to 20 s or saves a snapshot afterwards (629d448).
- Under polkit, a missing helper reads "archmaid-helper is not installed at <path>" instead of looking like a refused password (ea97314).
- `proc` strips an inherited `SUDO_ASKPASS` from every child without an explicit environment, and wrapped or combined-flag sudo is held to the same check, dry run too (534fa34, a1979d2).
- Engine `Options.Validate` checks the Env the way `New` will set it up (81f458d).
- The benchmarks changed shape with the move to the engine: the perf-guard baselines need re-measuring (see the notes below).

### Security (since 8540b78)

- The root helper no longer lets a started helper be killed or cut off by the client: after its start line, `helperclient` never kills pkexec or closes its output, keeps the real result and reports "stopped, but the root helper had already started and finished" (47db488).
- The helper and its client refuse unit names starting with `-` or `.`, and a test ties the two name checks together (5cb4af1, 3b97572).
- `remove-orphans` checks that pacman is idle right before the orphan recompute and again before `pacman -Rns`, and skips the removal (exit 5) otherwise (fbe2da5).
- Askpass requests are verified inside the real Driver (peer uid, start time, running sudo step) and are tested through the real socket; the password appears in no Event, report or log (f422a39).
- The TUI stops its askpass server on an error exit, so the socket and wrapper are not left behind (a51e782).

### Fixed (since 8540b78)

- A Session panic no longer leaves the TUI stuck on the running screen, and a password check that never resolves can be left with Ctrl-C (5de7e0c, 73b22cd).
- `proc.Stop`'s escalation goroutine exits once the child is reaped (8145c66).
- The e2e shim appends one whole line per write (1134f45).
- Tests now pin: a flood past `MaxLines` drops from the events, never from the log; the last-run outcome precedence (failed over incomplete) (c52e72e, c9539fb).

### Internal

- P1 layout: the TUI moves to `cmd/archmaid`, one Go module at the repo root (the helper's nested module is gone), TUI docs move to `docs/` (7ceabe9, 630e838, ba84e1e, b2f381d).
- P2 packages, no behaviour change: `internal/sanitize`, `internal/logstore`, `internal/sysstat`, `internal/downloads`, `internal/proc` (children: start, stream, stop, query, /proc reads) (61fcb32, 3c893b2, 40dec88, 8a1f649, 52a15be, 7e7f401, f90604e).
- Packages under `internal/` may not import charmbracelet; the gate enforces it (91fb0bf).
- Stream messages carry their step sequence; a `tickFn` seam lets tests drop timers (caf81b3, 8bfc04e).
- The AUR audit runs under one cancellable 20 s deadline and reads all its settings on the model's goroutine, which fixes data races under `-race` (ff6a515, 8316033).
- The audit fetch is a model-owned goroutine whose result the step awaits (707be81).
- Snapshot format is now `{"maintainer": ..., "comaintainers": [...]}`; the old bare-string format still loads without alarms (0ed25f2).
- Test infra: tests never touch the real home directory or state dir, and every package that can reach HOME runs under `testenv` (d5f1d56, d24b442, fff5b1d); askpass peer tests no longer depend on the real uid (5986bc9).
- P3 engine work: menu and queues are data in `internal/actions` and `internal/steps`; the transcript driver sits behind a `Target` interface; the AUR audit lives in `internal/audit`; sudo and askpass in `internal/privilege` (3ef4d38, 167f044, 2ffbeab, 7908ae7, 9332d02).
- CI uses Node 24 versions of its actions (vhs-action stays on node20 upstream) (521bfe6); the perf target's comment is corrected (8560d63); stale helper README notes are refreshed (74f72fa).

### GUI (since f46230a)

- **One polkit dialog per Full Cleanup** (decision E). The first live GUI run showed five dialogs. Now, under polkit, Full Cleanup's root steps (orphan removal, paccache, the optional wipe, tmpfiles, journal, integrity) run as one `archmaid-helper full-cleanup` call with one dialog. It is asked after topgrade, the orphan list and the cache-wipe confirm. Each step still shows its own row. If you refuse the dialog, all those rows read "Not authorised" and the run goes on (10666d8, f981d59, 4cd82e5, f7a8307, 0f83f65, 9dfd68a).
- **Under polkit, only topgrade asks for a password besides the dialog.** `pacman -Sc` (cached packages no longer installed) is now a part of the helper call (`--uninstalled`). The AUR-cache step is `yay -Sc --aur`, which cleans yay's build folder as you, without sudo (499d168, 746361f, a96182a, 6e6f70e; replaces 34cbce7).
- Steps no longer share the terminal the GUI was started from, so sudo can never prompt on that shell. Stop and Close end each step's whole process group (655fdb0, 60110ea, 3465a0d).
- Ctrl-C in the starting terminal, SIGTERM and closing that terminal all act as Stop and Quit: the run is aborted and recorded. A second signal kills everything the run started and exits at once (23f1f7b, 0f685e9, f43b87d, 76eec79).
- Closing the window while polkit's dialog is up stops and quits without asking, because the question would open under that dialog (e4010dc).
- Every start of an action gets its own run folder, and the AUR lookup runs while the Proceed dialog is up (e29a5fd).
- If the engine stops unexpectedly, the window leaves the running state and says so, and the next run starts on a fresh engine (97a4c09).
- The stub runs only with `--stub`, under a "Demo" banner, with "(stub)" in the title and its own app id (865124d).
- Fixed: clicking Authenticate could send an empty password, and "Stop and Quit" could leave the window open. The dialogs now act on the button you pressed, not on the dialog closing (bc5646b).
- Fixed: an answer retried because the engine was busy could reach the next run's prompt (ddf2098).
- Fixed: a dialog's list could scroll its title out of view in a short window (6df69df).
- A password dialog that the engine withdraws is wiped. sudo's prompt is cut to 48 characters, as in the TUI (3be3eed).
- A downloads folder read from `gui.json` is checked like a typed one (0de93ac).
- The end of a run reads the same way `last-run.json` judges it, and counts read "1 step", not "1 steps" (5a881d6, 4f22bed).

### TUI (since f46230a)

- **No step can read the TUI's terminal any more.** topgrade's `git pull` over HTTPS (also ssh and pinentry-tty) opened the terminal itself and read the same keys as the TUI. The TUI looked frozen, Ctrl-C couldn't abort, and `y` became git's username. The TUI now keeps its terminal to itself while it runs, so such a step fails at once instead (9d8ed41, 2783217).

### Internal and tests (since f46230a)

- `internal/frontend` holds the code both front ends share. The layering check now also keeps gotk4 out of `internal/` (2bebf67, ccaa96d, ea61927).
- `testenv` traps every system-changing tool (topgrade, yay, pacman, systemctl…), not only the privileged ones, and kills any process a test leaves running and fails that package (36318d0).
- GUI harness: a stand-in helper file, every Driver closed, the display suite split into four parts, checks for one polkit dialog and one askpass prompt, and every row line a run's events produce (5023db9, a6b22f9, e00141d, 08ecf61, bab48c6, fb436ff, 9dfd68a, 6e6f70e).
- Test fixes: the transcripts wait up to 30 s for a step to reach its shim; the pty test runs without `CI` and `NO_COLOR`; a fake AUR in a cancelled-fetch test can no longer hang Close (e71424d, 4d3b11d, 7f623af).

### Docs (since f46230a)

- `docs/first-live-run.md`: your guide to the first live run (5ae223d, 56fce2c, 0acb411).
- `docs/merge-to-main.md` (readiness review and checklist) and `docs/security-review-merge.md` (91f3f46, 99272a5).
- `docs/design/p8-workbench-spec.md`: the spec for the GUI redesign (d0bd9f9).
- The packaging README's scratch-build recipe, HANDOVER's open items brought up to date, and CLAUDE.md rules for tests and the test VM (17e4d42, e0f71d1, 0c4c15d, 95c2918, b45da6e, 5407fd4).

### Notes for the maintainer

- The live polkit/faillock test (`packaging/polkit/LIVE-TEST-AGENT.md`) was run against a scratch user (`docs/polkit-live-test.md`). `auth_admin_keep` is kept for the scope of the agent that asked: answered for the graphical session by the owner on 2026-10-11/12 (`docs/polkit-keep-session-check.md`, Results); the optional tty3 part is open.
- Pick a license: the repo has none, the PKGBUILD has `LicenseRef-unknown` and a TODO to install `LICENSE`. `namcap` has not been run on the packages.
- The first audit after upgrading rewrites `aur-maintainers.json` in the new format.
- Enable the widget after installing the package: `systemctl --user enable --now archmaid-updates-check.timer`, then enable "archmaid" in DMS Settings, Plugins and add it in Dank Bar (`docs/dms-widget.md`).
- The GUI's speed budgets are measured by hand (`ARCHMAID_GUI_PERF=1`, `docs/TESTING.md`); CI checks correctness only. The perf-guard baselines were taken before E7b changed the benchmarks' shape and want re-measuring (`make perf`, `make bench`).
- The merge itself is recorded in `docs/merge-to-main.md`.

[Unreleased]: https://github.com/Enzord758/archmaid/compare/v2.7.0...HEAD
[2.7.0]: https://github.com/Enzord758/archmaid/compare/v2.6.1...v2.7.0
[2.6.1]: https://github.com/Enzord758/archmaid/compare/v2.6.0...v2.6.1
[2.6.0]: https://github.com/Enzord758/archmaid/compare/v2.5.5...v2.6.0
[2.5.5]: https://github.com/Enzord758/archmaid/compare/v2.5.4...v2.5.5
[2.5.4]: https://github.com/Enzord758/archmaid/compare/v2.5.3...v2.5.4
[2.5.3]: https://github.com/Enzord758/archmaid/compare/v2.5.2...v2.5.3
[2.5.2]: https://github.com/Enzord758/archmaid/compare/v2.5.1...v2.5.2
[2.5.1]: https://github.com/Enzord758/archmaid/compare/v2.5.0...v2.5.1
[2.5.0]: https://github.com/Enzord758/archmaid/compare/v2.4.1...v2.5.0
[2.4.1]: https://github.com/Enzord758/archmaid/compare/v2.4.0...v2.4.1
[2.4.0]: https://github.com/Enzord758/archmaid/compare/v2.3.0...v2.4.0
[2.3.0]: https://github.com/Enzord758/archmaid/compare/v2.2.0...v2.3.0
[2.2.0]: https://github.com/Enzord758/archmaid/compare/v2.1.1...v2.2.0
[2.1.1]: https://github.com/Enzord758/archmaid/compare/v2.1.0...v2.1.1
[2.1.0]: https://github.com/Enzord758/archmaid/compare/v2.0.0...v2.1.0
[2.0.0]: https://github.com/Enzord758/archmaid/compare/5585358...v2.0.0
