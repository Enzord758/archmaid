# Run output as a terminal shows it (task #129)

Base: `ecb0903` (main = refactor/p0). Owner's retest of "Run colours" failed:
oh-my-zsh's rainbow logo, ollama's `[GIN]` cells and pacman/yay download
bars are missing on the Run page, topgrade's section rules are short, and
ollama's spinner frames pile up on one line.

## Cause

Two things, both in archmaid, not in the renderer (`runview/ansi.go` draws
256 colours, truecolour, backgrounds and the styles already):

1. **No terminal on the child's output.** Every step's child writes into a
   pipe, so every tool that asks `isatty(1)` turns colour, progress bars and
   its terminal width off (topgrade's rule falls back to 80 columns, ollama's
   bar to a few cells). #110 forced colour for topgrade and yay by flag/env;
   everything under them (oh-my-zsh, ollama, pacman's bars) still sees a pipe.
2. **No line semantics.** Output is split at `\n` and each line is sanitized
   on its own (`\r` keeps only the text after it; every cursor move is
   dropped). A tool that redraws (`\r`, `ESC[1A`, `ESC[K`) leaves every frame
   behind, or glued onto one line.

## Design

### 1. A pty as output, never as the controlling terminal (GUI only)

`proc.Options.Pty` (with `Detach`; without it Pty is ignored) gives the
child a pty slave as fd 1 and fd 2; fd 0 stays `/dev/null`. The child keeps
`Setsid` (detach.go, unchanged) and **never gets `Setctty`/`TIOCSCTTY`**:

- `isatty(1)` and `isatty(2)` are true: tools turn on colour, bars and read
  the width from `TIOCGWINSZ`.
- The child leads a session with **no controlling terminal**: inheriting a
  tty fd does not make it one (only a session leader's `open()` of a tty
  for reading without `O_NOCTTY`, or its `TIOCSCTTY` on an fd that can
  read, does). So `/dev/tty` can't be opened (ENXIO) and `tty_nr` in
  `/proc/<pid>/stat` is 0.
- The slave is opened from the master with `TIOCGPTPEER` (no path lookup),
  **write-only** and `O_NOCTTY`, and its `/dev/pts/N` is made **mode 0200**
  (review #135). So nothing under the step can take it as a controlling
  terminal: `TIOCSCTTY` on fd 1 or 2 is EPERM (it needs read access), a
  write-only open by name gives no controlling terminal even without
  `O_NOCTTY`, and an open for reading by name is EACCES (the mode). A read
  of fd 1 or 2 fails at once (EBADF), so a pager reading its keys from
  stderr gives up instead of waiting forever. Opens for writing by name
  (`>/dev/stderr`, `>/proc/self/fd/1`) work as on any terminal. Root
  (sudo's pacman) passes the mode; nothing there opens a terminal by name
  as a session leader. (#129 made the slave exclusive, `TIOCEXCL`, instead:
  that refused those redirections with EBUSY, and left the inherited fds
  readable and `TIOCSCTTY`-able.)
- **sudo** finds its tty from `tty_nr` (`/proc/self/stat`), then tries
  `/dev/tty`: both say "none", exactly as with the pipe. So the sudo path is
  unchanged: it asks through `SUDO_ASKPASS`, or with `-n` not at all; its
  ticket stays keyed as before (no tty); `use_pty` needs a tty, so it
  doesn't start one. The HANDOVER Detach landmine stays true word for word:
  a session of its own, no controlling terminal.
- stdin stays `/dev/null`: a prompt that reads stdin gets EOF at once, as
  today. A tool that wants a key from `/dev/tty` gets ENXIO, as today.

`TERM=xterm-256color` is set for such a child (after the step's own Env).
The window size: columns from the Run view's width in monospace cells
(`driver.Deps.TermCols`, read at each step's start; the GUI measures its
output list), rows fixed at 50. Resize mid-step is **not done**: a pty with
no session has no foreground process group, so `TIOCSWINSZ` would send no
`SIGWINCH`, and tools cache the width until they get one. The next step
gets the new width.

The master is read until EIO (every slave fd closed). Something the child
left running holds the slave like it held the pipe, so `WaitDelay` bounds
the read after the child exits the same way (a read deadline on the
master, then it is closed: a later write by that process gets EIO).

**What the merge costs:** stdout and stderr were already one pipe
(`cmd.Stdout, cmd.Stderr = pw, pw`); nothing in archmaid has a stream tag,
no display distinguishes them and no step's output is parsed (orphans and
Repair parse `proc.Query`, which never gets a pty). On a pty their order is
the order written, as in a terminal. Nothing is lost.

**Behaviour changes a pty brings** (for the VM run): topgrade asks
"Retry? (y)es/(N)o/(s)hell/(q)uit" after a failed step when stdout is a
terminal. Its key read (console 0.16.6, `read_single_key`) opens `/dev/tty`
because stdin isn't one, which fails (ENXIO); topgrade's handler logs
"Error reading from terminal" and goes on as "No". So a failed topgrade
step prints two extra lines and carries on; it can't hang. `--no-retry`
would remove the lines; left to the lead (it changes the step's argv in 21
files' goldens).

The TUI keeps its process model: plain children in its session, pipes,
never Detach, never Pty (`TestTUIDoesNotDetach` also holds Pty).

### 2. Terminal line semantics: `internal/vt`

A small, bounded line model, used for **every child step's output**, pipe
or pty, in `proc` (helper steps' JSON lines and in-process results append
as before):

- `\r`, `\n` (also VT, FF; `\n` is CR+LF, as the pty's ONLCR makes it),
  `\b`, `\t` (to the next multiple of 8);
- `ESC[K` (EL 0/1/2), `ESC[J` (ED 0/1; ED 2/3, "clear screen", is ignored:
  it would blank real output), `ESC[nA/B/C/D/E/F/G`, `ESC[nX/P/@`,
  `ESC 7/8` and `ESC[s/u`, `ESC M`, `ESC D`, `ESC E`;
- SGR is tracked per cell (bold, dim, italic, underline, blink, reverse,
  strike; 16/256/24-bit fg and bg) and written back as the child wrote it:
  each cell's pen keeps the SGR text that set it since the last reset
  (`"\x1b[1m\x1b[34m"` stays that), so a line that wasn't redrawn comes out
  byte for byte as before and #110's colour tests pass unchanged; a pen whose
  text grows past 256 bytes becomes its canonical SGR. An SGR that changes
  nothing is not kept; conceal is never kept (as sanitize), not even
  inside a sequence that changes something else (`ESC[8;1m` keeps only the
  bold). Every other
  escape (OSC, DCS/SOS/PM/APC,
  private CSI like `?25l`, `?1049h`, mouse, absolute positioning `ESC[H`) is
  dropped. A string (OSC, DCS/SOS/PM/APC) the child never terminates ends
  at a newline, or after 4096 bytes, and what follows is output again
  (review #135: one unterminated `ESC ]` swallowed the rest of the step). Invalid UTF-8 → U+FFFD; C1 and hidden format characters are
  dropped (sanitize's set). Wide runes take two cells.
- **Bounds:** the cursor reaches only the last 64 lines of the current
  child's output (a fresh model per child: never an earlier step), a line
  holds at most 8192 cells (text past that is dropped), a rendered line at
  most 64 KiB of SGR (past that its styles stop), at most 1024 distinct
  pens (one compaction per 64 new lines, then new ones draw plain), a
  control sequence is parsed from at most 256 bytes (longer ones are
  consumed and ignored), and at most 128 lines wait behind a held-back
  redraw before a flush is forced. Fuzzed (`FuzzScreen`): the caps hold,
  no wide rune is ever left half, and any split of the input into writes
  and flushes ends as one flush would.
- **Cost (review #135, F5):** the dearest input is a line-wide shift
  (`ESC[P`, `ESC[@`) or a fill (EL, ECH with a background) of a full
  8192-cell line per 3–4 bytes: a move of the line, no per-cell lookups,
  about 0.16 µs per input byte (6 MB/s, `BenchmarkScreenDCHICH`; a
  whole-line rune-width scan per DCH/ICH made it 25 µs per byte before).
  Only the child's own output pays it, and the pty holds the child back
  while archmaid catches up.
- No autowrap: a logical line stays one line; the view wraps it.

It emits **rewrites**: `(replace n, lines)` = the step's last n delivered
lines are replaced by `lines` (`len(lines) >= n`; lines never disappear).

**Deviation from the brief:** not `From` (an absolute index) but
`Replace int`, relative to the tail: `OutputChunk.Replace`, `StepOutput.
Replace`, `proc.Msg.Replace`, `proc.Batch.Replace`. Why: (a) additive in the
frozen API — `Replace`'s zero value is "append", the meaning every existing
chunk (stub scripts, helper lines, tests) already has, while `From`'s zero
would mean "rewrite from line 0"; (b) every consumer that merges, slices or
drops chunks (the Driver's outbox, the GUI pump's merge/`take`/`trim`,
`proc.Next`) composes it without knowing absolute positions: a chunk with
Replace r merged into one with lines L becomes `L[:len(L)-r]+new` or, when
r > len(L), Replace grows by r−len(L); a split keeps Replace on the head
piece; a drop keeps Replace and counts the dropped head in `Dropped`.

**Consumer rule** (OutputChunk doc): remove the step's last `Replace` lines
— never more than it holds of that step since its last gap (a `Dropped`
note, the step's start, lines evicted by its cap) — then show `Dropped`,
then append all of `Lines`: what it can't remove is gone already, so the
new versions of those lines belong after the gap. The engine is stricter
with its own copy (the log): a line it added itself (an askpass refusal,
an in-process result) ends what the child may redraw, and the new versions
of the lines before it are dropped, since their old versions are still
there. A Driver that drops a whole queued chunk keeps it, empty, with its
`Dropped`, when it or the next chunk of the step is a redraw: carried into
a redraw, the gap would come after its removal and the removal would take
a line the consumer has in place of a dropped one.

**Coalescing** (in `proc`, which has the clock): new complete lines go out
at once (as today); a partial line (no `\n` yet) after 50 ms without more
output; a rewrite of delivered lines at most once per 50 ms per step (one
flush carries every changed line), or when 128 lines wait behind it; and a
final flush at EOF. So a spinner or bar costs at most 20 redraws a second,
however fast it repaints (`TestRedrawCoalesced`: a child redrawing one line
as fast as a shell can for 2 s, on a pipe and on a pty). A line cut at a
read's end that the timer finds unfinished goes out and comes back whole
as a redraw; the timer waits while more output is already read. The Driver's outbox and
the GUI pump keep their Dropped/back-pressure rules; they only learn to
merge and trim Replace chunks as above.

### 3. Logs and last-run.json

The engine's copy of a step's output (`run.out`, the log entry's body)
applies Replace too, so the log gets each line's final text (then
`sanitize.Plain` as today), never the intermediate frames.

### 4. Front ends

- GUI `runview.Model`: Apply pops the replaced lines off its ring and
  pushes the new ones; the diff gains `Change.OutTrim` and `StepOut.Trim`
  (lines leaving the tail before the append), so the widget splices only
  the changed tail. The pump merges/trims Replace chunks.
- TUI pane (`lineTail`): pops Replace lines of the running step, then
  appends. The TUI has no pty, but its pipe output now has line semantics
  too (a `\r` bar redraws in place).
- Glyphs: the Run view's lines use libadwaita's `.monospace` (fontconfig's
  `monospace`; on the owner's machine Noto Sans Mono). It has the block
  (U+2580–259F) and box (U+2500–257F) glyphs; it has **no braille**
  (U+2800–28FF): Pango falls back per glyph (here Iosevka Nerd Font), so
  spinners draw, slightly narrower than a cell. `TestGUIRunColour` reports
  the unknown-glyph count of all three groups in the headless window
  (0, 0, 0 on this machine) and fails on blocks or boxes.
- Width: `ui.TermCols` is the Run view's width less the lines' padding
  (62 px) over the font's digit width, less one column, measured whenever
  the list's horizontal adjustment changes (every allocation that changes
  its width; no per-frame tick), at least 20. 720 px → 72 columns in the
  harness.

### 5. NO_COLOR

Unchanged: `sanitize.ColourMode` strips SGR in the engine; the pty and the
line semantics stay on (a bar still redraws in place, without colour).
