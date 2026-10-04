// Package engine is archmaid's run logic as a state machine with no IO and
// no screens: a Session takes what the user did (Start, Confirm,
// SubmitPassword, CancelPrompt, Select, Abort) and what the world did
// (Handle with an Input), and answers each with an Output — Events (what
// happened, for a front end to show) and Effects (IO for a Driver to carry
// out, whose results come back as Inputs). The TUI drives a Session
// synchronously; the GUI talks to a Driver, which owns a Session, runs its
// Effects and streams its Events.
//
// The API was frozen at E0: from then on changes are additive only — new
// Event, Effect, Input or Command types, new fields, new constants. Nothing
// is renamed, removed or given a new meaning. The Session is built up
// through P4 (its doc says what runs so far; the rest panics rather than
// pretend); until the real Driver (E6), front ends build against
// internal/engine/stub, a Driver that replays a scripted Event sequence.
//
// This comment and the doc comments on each type are the contract. A Driver
// or front end may rely on everything stated here, and on nothing else.
//
// # Sessions
//
// A Session runs one action at a time. It is not safe for concurrent use:
// one goroutine (the TUI's Update, or a Driver's loop) calls it. Every call
// returns at once; nothing in the package blocks, sleeps, reads a clock other
// than Options.Now, or touches the file system, the network or a process.
//
// # Prompts
//
// NeedsConfirm, NeedsPassword, NeedsSelection and OutputReport open a
// prompt with a PromptID. IDs are unique within a Session and never reused;
// zero is never an ID. At most one prompt is open at a time. A prompt is
// closed by its answer (Confirm, SubmitPassword, Select), by CancelPrompt,
// or by the engine itself, which then emits PromptClosed (an askpass client
// that hung up, an Abort). A call with an ID that is not the open prompt's —
// stale, already answered, never issued — is ignored: it returns an empty
// Output and changes nothing. So a front end can forward a late click
// without checking.
//
// # Ordering
//
// Events are in the order they happened, within one Output and across
// Outputs; a Driver publishes them in that order. Within one action:
//
//   - QueueStarted comes before any event naming a step; the plan it lists
//     changes only through StepInserted and PlanChanged.
//   - At most one step runs at a time.
//   - A step that runs gets, in order: its NeedsConfirm (if it has a
//     confirm), StepStarted, zero or more OutputChunk, then exactly one of
//     StepDone or Aborted. Nothing names it after that, except PlanChanged
//     or StepInserted entries for other steps and LogWritten for its log
//     entry, which follows its StepDone.
//   - A step that never runs (declined, behind a declined required step,
//     privilege not granted) gets one StepDone with Skipped or AuthRefused
//     and no StepStarted.
//   - After Aborted, steps still pending get no event; a front end shows
//     them as not run on Finished{OutcomeAborted} (counted in its NotRun).
//   - What a step's result changes in the plan (StepInserted, PlanChanged)
//     comes after that step's StepDone and before the next step's events.
//   - AuthWaiting is followed by AuthResolved before the step or queue it
//     waited for goes on.
//   - Finished is the last event of every action, exactly once; nothing
//     about that action follows it.
//
// StatusUpdated and PhaseChanged may come between any two events.
//
// # Dropping and coalescing
//
// The engine never drops, caps or merges anything: every line a step
// printed is in its OutputChunk events and in its log entry. A Driver may
// coalesce consecutive OutputChunk events of the same step into one (the
// lines concatenated in order, typically every 16–30 ms), and when its
// consumer falls behind it may drop the oldest undelivered lines of a step,
// counting them in the next chunk's Dropped. OutputChunk is the only event
// a Driver may coalesce or drop. Every other event — the control events —
// is delivered exactly once, in order, however slow the consumer: a Driver
// buffers them without bound rather than block the Session or lose one.
// The log is written from the engine's own copy of the output, so what the
// view dropped is still in the log.
//
// # Drivers
//
// Driver.Send returns false when a Command could not be queued; a front end
// retries it (the GUI does). Intent for the real Driver (E6): Commands go
// through their own small queue, apart from the Inputs and Events, so a
// Send effectively never fails while the Driver is open — false is kept
// for a closed Driver. The stub does not promise that yet.
//
// # Passwords
//
// No Event ever carries a password, in any field, however nested. A
// password enters as a Secret (SubmitPassword, the SubmitPassword
// command) and leaves only in an Effect (Authenticate, AnswerAskpass) for
// the Driver to hand straight to privilege; the Session keeps no copy past
// the call. Secret formats as "<redacted>" with every fmt verb, so logging
// a Command or Effect by accident can't leak it. Events are plain data — no
// funcs, channels or interfaces in their fields — and every string in them
// that came from outside archmaid (command output, AUR names, sudo's
// prompt) has been through internal/sanitize: output lines keep only SGR
// colour (sanitize.KeepSGR), everything else is plain.
//
// # Privilege
//
// Privileged work is a step with NeedsPriv or a Verb (internal/steps); the
// engine never decides how root is obtained. Options.Auth picks the flow
// (sudo: one password up front, askpass mid-run; polkit: the root helper
// asks per step through pkexec), and the Driver's privilege backend builds
// the command. Whatever the reason privilege was not granted — a wrong
// password three times, a dismissed polkit dialog, a locked account — the
// step ends as steps.AuthRefused: pkexec exits 127 for all of them, so the
// engine has one outcome (for the helper: it exited 127 before its JSON
// start line). Before a polkit prompt the engine may ask for the faillock
// count (ReadFaillock) and report it as FaillockState, so a front end can
// warn before the attempt that locks the account.
//
// As built (E3/E4): the count is read before the up-front sudo prompt and
// before each polkit helper step (a child with a Verb). The sudo prompt is
// shown even on a locked account (the front end has the FaillockState); a
// helper step on a locked account ends AuthRefused without starting, so no
// polkit dialog is shown that can't succeed. An unreadable count is not
// reported and changes nothing. AuthResolved{Granted: false} with
// AttemptsLeft 0 also ends a password check whose askpass request was
// withdrawn because its step ended; that step settles as its exit says.
//
// # Transcripts
//
// The events line up with internal/testtranscript's, so a sessionTarget
// maps them one to one: QueueStarted, StepInserted, PlanChanged,
// StepStarted, StepDone, Aborted, NeedsConfirm (Msg is the transcript's
// Text), NeedsPassword, NeedsSelection (Items), Notice and OutputReport
// (Text) by name; OutputChunk splits into one OutputLine per line; Finished
// is the Target's Outcome (Outcome.String is the transcript word, so a
// completed queue with Refused > 0 is still "completed"). StepDone's
// status maps as the TUI's checklist draws it: steps.AuthRefused is the
// transcript's skipped. Step indexes are 0-based plan positions in both.
//
// LogWritten is not mapped one to one: a transcript's log lines are the
// harness's diff of the log files (as for the TUI), taken after a batch of
// events and grouped by category file, so several entries written in one
// go are ordered by file, not as the events came
// (fullcleanup_three_wrong_passwords). A sessionTarget takes them the same
// way and checks that every LogWritten is among them. A transcript's Exec
// and SnapshotChanged are likewise the harness's own observations of the
// system, not engine events; PhaseChanged, StatusUpdated, PromptClosed,
// AuthWaiting, AuthResolved and FaillockState have no transcript line.
package engine
