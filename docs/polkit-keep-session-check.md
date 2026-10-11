# Live check: does `auth_admin_keep` carry across processes in a session?

For the owner to run by hand. No agent runs any of this (CLAUDE.md rule 2).

**Answered for the graphical session** (the owner, Part A, 2026-10-11/12:
G3 rc 0, G5 `unix-session:`; see "Results" below and HANDOVER → "`auth_admin_keep`
is kept for the scope of the agent that asked"). **The question** (`docs/polkit-live-test.md` case
F2): outside any logind session, polkit kept an `auth_admin_keep`
authorization only for the process that asked (pkexec's parent); a new process
was prompted again. Inside a real session (the graphical one, or a tty login),
polkit may key the kept authorization to the **session**. Then any process
in that session would get it for about 5 minutes. This check finds out which
one happens.

**How it stays safe**

- One throwaway action, `org.archmaid.keepcheck.k7q2`. It runs only
  `/usr/bin/true`, and only with the marker argument `archmaid-keepcheck-k7q2`
  (`exec.argv1`). No other installed action uses `/usr/bin/true`, so it can't
  take over any other action's calls. Nothing else on the system changes.
- **At most 2 polkit password prompts**, one each in Parts A and B. Every
  check after the first password is `pkcheck` without
  `--allow-user-interaction`, which never prompts. It only answers
  authorized or not. Part C (optional) opens one more dialog but you type
  nothing into it; it may cost one faillock row, which is what it measures.
- faillock has `deny=3`, and every wrong password counts: a polkit prompt, the
  tty3 login and `sudo` all go through `pam_faillock`. **A cancelled or
  dismissed polkit dialog also counts as one failure.** A correct password
  resets the count to 0.
- **Stop rule:** after any wrong password or cancelled dialog, don't retry.
  Go straight to the cleanup. If `faillock` ever shows **2 or more rows**,
  type no password at all (not even the cleanup's `sudo`) until 15 minutes
  have passed since the newest row (`fail_interval` = 900 s). Then check
  again.

**Time:** about 15 minutes for Part A, cleanup and the results. Part B (tty3)
is optional and adds about 5–10 minutes; Part C (a killed pkexec) is
optional and adds about 5. The steps are numbered 1–14, Part C's C1–C4.

## What you are looking for

- **Which action's banner appeared.** On tty3, polkit's text agent
  (`pkttyagent`, started in step 11) prints
  `==== AUTHENTICATING FOR org.archmaid.keepcheck.k7q2 ====`, and the
  next line is the message
  `archmaid keep check k7q2: throwaway test, runs /usr/bin/true`. In the
  graphical session, the polkit-gnome dialog shows that same message; its
  **Details** expander shows `Action: org.archmaid.keepcheck.k7q2`. If
  anything else shows up (a different id, a different message, or no
  `k7q2`), don't type your password. Write down what it says, cancel it
  (that costs one faillock row) and go to the cleanup.
- **pkcheck's exit code.** `0` = authorized now, without a prompt (stdout may
  show `polkit\56temporary_authorization_id=…` or `polkit\56result=…`:
  pkcheck itself writes the dot in a key as `\56`, not your shell). `2` =
  would need a password (pkcheck was told not to ask). Anything else: copy
  stderr into the notes.

## Where each step runs

Two logind sessions take part, and a step only means something in the
one it names. "Terminal 1" is one terminal window in your graphical
session (tty2); keep it open from step 1 to step 14, because `$d` (step 2)
and `$p` (Part C) live in that shell.

| Steps | Session | Shell |
|---|---|---|
| 1–9 (G0–G5) | graphical (tty2) | terminal 1's zsh; step 2b in a plain `bash` started there (and left again); G4 in a new terminal window (terminal 2) |
| 10–12 (T0–T5) | the tty3 text login, a session of its own | tty3's login shell, all in that one shell |
| C1–C4 (K0–K2) | graphical (tty2) | terminal 1 |
| 13–14 (cleanup) | graphical (tty2) | terminal 1 |

In the graphical session you can paste; on tty3 (a text console) you type.
zsh's url-quote-magic (step 2) only rewrites characters inside a word
that holds a colon (as a URL does); none of the lines you run in zsh has
one, so typed or pasted they arrive as written. Only the policy block
does, which is why step 2b runs in a plain bash.

**pkexec's built-in text agent doesn't work with this polkit (127).** On a
console with no agent of its own, pkexec starts its own text agent. It
prints the banner and takes the password, PAM accepts it (no faillock
row), and then pkexec says `AUTHENTICATION FAILED` / `Not authorized`;
the journal shows `polkit-agent-helper-1: error response to PolicyKit
daemon: ... No session for cookie`. "Session" there means polkit's
authentication session (the cookie), not the logind session. The cause
is in polkit itself: polkit 126 and later run `polkit-agent-helper-1` from
a socket (`/run/polkit/agent-helper.socket`, not setuid any more), and the
helper takes the agent's uid from the socket. pkexec is setuid root, so
that uid is 0. polkitd registered pkexec's agent under the user of the
subject (your uid), and only accepts a reply from the uid the agent was
registered under, so the reply is refused. polkit-gnome and `pkttyagent`
run as you, so their replies match. Part B therefore runs `pkttyagent` for
the tty3 shell and calls pkexec with `--disable-internal-agent`.
archmaid-gui does the same: it runs `pkexec --disable-internal-agent
/usr/lib/archmaid/archmaid-helper …` (`helperclient.Argv`), so with no
agent at all it fails at once as "no polkit agent" instead of prompting on
the terminal it was started from.

## Setup

1. **faillock first.** In a terminal in your graphical session:

   ```sh
   faillock --user "$USER"
   ```

   Write down the number of rows under the `When Type Source Valid` header.
   **It must be 0.** If it isn't, wait until the rows are older than 15
   minutes, or find out where they came from first.

2. **Write the policy** to a scratch directory (no root needed). The
   block has to arrive in the file **exactly as it is, flush left**.
   polkit silently ignores a policy file that doesn't parse, and any space
   before `<?xml` makes it unparseable ("XML declaration allowed only at
   the start of the document"). `pkaction` then says "No action with
   action id". The fenced blocks in steps 2-4 are deliberately not
   indented, so a copy from the raw Markdown file needs no stripping.

   **Paste the block into a plain `bash`, not into your zsh.** zsh's line
   editor rewrites what you paste or type, depending on the plugins you
   have loaded: oh-my-zsh binds bracketed paste to
   `bracketed-paste-magic`, which runs a paste through `url-quote-magic`,
   and that turns the DOCTYPE's `policyconfig.dtd">` into
   `policyconfig.dtd"\>` ("DOCTYPE improperly terminated"). Pasting again
   gives the same result. `bash --norc --noprofile` loads none of your
   shell setup, and bash inserts a paste (or typing) literally. `$d` is
   set and exported in zsh first, so it is still there after you leave
   bash.

   2a. In terminal 1 (zsh), make the directory and start the plain bash:

```sh
d=$(mktemp -d) && export d && echo "$d"
bash --norc --noprofile
```

   The prompt changes to `bash-5.x$`. 2b. Paste this block there
   (Ctrl+Shift+V) and press Enter if it doesn't run by itself:

```sh
cat > "$d/org.archmaid.keepcheck.k7q2.policy" <<'EOF'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE policyconfig PUBLIC "-//freedesktop//DTD PolicyKit Policy Configuration 1.0//EN"
 "http://www.freedesktop.org/standards/PolicyKit/1/policyconfig.dtd">
<policyconfig>
  <vendor>archmaid live check (throwaway)</vendor>
  <action id="org.archmaid.keepcheck.k7q2">
    <description>archmaid keep check k7q2 (throwaway)</description>
    <message>archmaid keep check k7q2: throwaway test, runs /usr/bin/true</message>
    <defaults>
      <allow_any>auth_admin_keep</allow_any>
      <allow_inactive>auth_admin_keep</allow_inactive>
      <allow_active>auth_admin_keep</allow_active>
    </defaults>
    <annotate key="org.freedesktop.policykit.exec.path">/usr/bin/true</annotate>
    <annotate key="org.freedesktop.policykit.exec.argv1">archmaid-keepcheck-k7q2</annotate>
  </action>
</policyconfig>
EOF
echo "$d" && head -c 5 "$d/org.archmaid.keepcheck.k7q2.policy" && echo && xmllint --noout --nonet "$d/org.archmaid.keepcheck.k7q2.policy" && echo XML-OK
```

   The `head` line must print `<?xml` with nothing before it, and
   `XML-OK` must appear. If either is wrong, don't install: paste the
   block again in the same bash (it overwrites the file); if it still
   fails, `exit`, `rm -r "$d"` and stop. (No `xmllint`? It is in
   `libxml2`; without it the `head` check alone must pass, and
   `grep -c '\\' "$d/org.archmaid.keepcheck.k7q2.policy"` must print `0`.)

   2c. Leave bash: type `exit` on its own and press Enter. Don't paste it
   together with the next line: bash would run `exit` and drop the rest.

```sh
exit
```

   Back in terminal 1's zsh, check `$d` is still set:

```sh
echo "$d"
```

   It prints the same directory. Keep it for the cleanup. Run steps 3-4
   (and the rest) in this zsh, so `$d` stays set.

3. **Install it** (one `sudo` password, so type it carefully). The target
   is the directory `/usr/share/polkit-1/actions/`, the file must end in
   `.policy`, and `install` makes it `root:root 0644`:

```sh
sudo install -o root -g root -m 0644 "$d/org.archmaid.keepcheck.k7q2.policy" /usr/share/polkit-1/actions/ && ls -l /usr/share/polkit-1/actions/org.archmaid.keepcheck.k7q2.policy
```

4. **Check that polkit sees it** (no root needed). polkitd picks up new
   action files by itself, normally within a second:

```sh
sleep 3 && pkaction --action-id org.archmaid.keepcheck.k7q2 --verbose
```

   It must list `implicit any/inactive/active: auth_admin_keep` (three
   times). If it says "No action with action id", wait 10 s and run it
   again. If it still isn't there, the file didn't parse (or the paste was
   indented): run `head -c 5` and `xmllint --noout --nonet` on the
   installed file, and read
   `journalctl -b -u polkit --no-pager | tail -20` for a parse error
   (read-only). Then stop and clean up (step 13). Don't restart polkit.

## Part A: the graphical session (required)

Do steps 5–9 within 5 minutes; a kept authorization lasts about 5 minutes.
Use the same terminal as in steps 1–4 (call it terminal 1) unless a step says
otherwise.

5. **Context and baseline** (no prompt):

   ```sh
   echo "shell pid $$"; loginctl show-user "$USER" -p Display; cat /proc/$$/cgroup
   pkcheck --action-id org.archmaid.keepcheck.k7q2 --process $$; echo "rc=$?"
   ```

   Case **G0**. Expected: `rc=2`. If it's `0`, a local rule already grants
   the action, so the check means nothing. Stop and clean up.

6. **The one password for Part A**:

   ```sh
   pkexec /usr/bin/true archmaid-keepcheck-k7q2; echo "rc=$?"
   ```

   Case **G1**. A polkit-gnome dialog appears. Before typing, check the
   message and, under Details, the action id (see "What you are looking
   for"). Type your password once. Expected: `rc=0`. (The dialog normally
   comes from polkit-gnome. If DankMaterialShell was restarted after
   polkit-gnome died, DMS's own "Authentication Required" dialog, window
   class `com.danklinux.dms`, is the agent instead; the check works the
   same with either.)

7. **Same process** (no prompt): the shell that ran pkexec.

   ```sh
   pkcheck --action-id org.archmaid.keepcheck.k7q2 --process $$; echo "rc=$?"
   ```

   Case **G2**. Expected `rc=0`, which shows that keep works at all.

8. **A new process in the same session** (no prompt):

   ```sh
   sh -c 'pkcheck --action-id org.archmaid.keepcheck.k7q2 --process $$; echo "pid $$ rc=$?"'
   ```

   Case **G3**, the question itself. Then open a **new terminal window**
   (terminal 2, a fresh process tree) and run the same `sh -c …` line there:
   case **G4**.

9. **What polkit kept** (no prompt):

   ```sh
   pkcheck --list-temp
   ```

   Case **G5**. Copy the output. It names the action and the **subject** the
   authorization is kept for: `unix-session:…` means session scope,
   `unix-process:…` means process scope. If it errors (for example "no
   session" for a process outside a session scope), copy the error. That's
   a result too. `--list-temp` lists only the kept authorizations whose
   *agent* was registered for your session: polkitd stores each one with
   its subject and with the scope of the agent that asked, and lists (and
   `--revoke-temp` removes) by that scope. polkit-gnome is registered for
   the graphical session, so G5 shows the authorization.

   How to read Part A: if G3/G4 give `rc=0`, keep carries across processes
   in the graphical session. If they give `rc=2` while G2 gives `0`, keep is
   per process here too.

## Part B: a tty login (optional)

Only if faillock still shows 0 rows (`faillock --user "$USER"`). Logging in on
tty3 is a password too. Every command in steps 10–12 runs in tty3's login
shell (one session, one shell; don't start a sub-shell or `tmux` there).
Do steps 11 and 12 within 5 minutes.

10. Press **Ctrl+Alt+F3** and log in as yourself. Then run step 5's commands
    (case **T0**). Also write down how long it has been since G1. A `0` here
    would mean the graphical session's keep reaches a different session.

11. **The one password for Part B.** pkexec's built-in agent can't be used
    (see "Where each step runs"), so start polkit's text agent for this
    shell first, in the background, with job control off so it may read
    the console:

    ```sh
    set +m; pkttyagent --process $$ & a=$!; sleep 2; kill -0 "$a" && echo "agent $a up for $$"
    ```

    It must print `agent … up for …`. If it doesn't (pkttyagent printed an
    error and exited), don't go on: no password has been asked; note the
    error, `set -m`, and skip to `exit` at the end of step 12. Then:

    ```sh
    pkexec --disable-internal-agent /usr/bin/true archmaid-keepcheck-k7q2; echo "rc=$?"
    ```

    Case **T1**. pkttyagent prints the
    `==== AUTHENTICATING FOR org.archmaid.keepcheck.k7q2 ====` banner and
    `Password:`. Check the id, then type the password once. Expected:
    `rc=0`. Then, in every case, stop the agent and turn job control back
    on, so nothing in step 12 can reach it:

    ```sh
    kill "$a"; wait "$a"; set -m
    ```

    If T1 went wrong, run that line, `exit`, Ctrl+Alt+F2 and go to the
    cleanup (step 13), noting what happened:
    - "No authentication agent found" (`rc=127`): no password was asked,
      nothing counted.
    - No banner within 10 s: press Ctrl+C (one faillock row).
    - `AUTHENTICATION FAILED` after the password: count it as a wrong
      password (stop rule) and copy
      `journalctl -b --since -5min --no-pager | grep -E 'polkit|pkexec'`
      (read-only) into the notes.

    Which agent asked doesn't change who may use what polkit keeps: the
    kept authorization is stored for pkexec's subject (this shell's
    process), exactly as with pkexec's own agent, and T2–T4 test who it
    covers. The agent only sets the authorization's *scope*, which decides
    where `--list-temp` shows it (T5, step 12).
    The agent was registered for this shell's process only (`--process
    $$`), never for the tty3 session, so once it is stopped nothing can
    answer a prompt in step 12.

12. Run step 7 (**T2**), step 8's `sh -c …` line (**T3**) and step 9 (**T5**).
    Then a real exec from a new process, which can't prompt either: with
    `--disable-internal-agent` and no agent registered for the tty3 session
    (or for the new `sh` process), pkexec fails with "No authentication
    agent found" instead of asking:

    ```sh
    sh -c 'pkexec --disable-internal-agent /usr/bin/true archmaid-keepcheck-k7q2; echo "pid $$ rc=$?"'
    ```

    Case **T4**. `rc=0` = authorized by the kept authorization. `rc=127`
    plus "No authentication agent found" = it would have needed a password.
    If a prompt appears anyway, don't answer it: press Ctrl+C (one faillock
    row) and note it.

    **T5 (`pkcheck --list-temp`) prints nothing here, and that is the
    expected result**, not an error. polkitd keeps each authorization with
    the scope of the agent that asked, and `--list-temp` (like
    `--revoke-temp`) only covers those whose scope is the caller's session
    (`polkit_subject_equal (ta->scope, session)` in polkit 127's
    `polkitbackendinteractiveauthority.c`). On tty3 the agent was step 11's
    pkttyagent, registered for this shell's process (`--process $$`), so
    the kept authorization is scoped to that process and never listed. T5
    therefore says nothing about session versus process; T2 and T3 do.
    T2's stdout names the kept authorization
    (`polkit\56temporary_authorization_id=tmpauthz…`): copy that id into
    the T5 notes. If T5 does list something, copy it: then an agent
    registered for the tty3 session asked, which this procedure doesn't
    start.

    Then `pkcheck --revoke-temp`, `exit`, and **Ctrl+Alt+F2** back to the
    graphical session. For the same reason `--revoke-temp` removes nothing
    on tty3. That is fine: the kept authorization's subject is this
    shell's process (pid and start time), so nothing else can use it,
    `exit` ends that process, and it expires about 5 minutes after T1.

## Part C: a pkexec killed in the dialog (optional)

**The question:** archmaid's Stop, while the polkit dialog is up, SIGKILLs
pkexec (`helperclient.Run`; the helper never runs; archmaid also passes
`--disable-internal-agent`, which changes nothing while polkit-gnome is
there to ask, so C2's plain pkexec is the same request). A dismissed dialog
costs one faillock failure. Does a pkexec killed mid-prompt cost one too?
Then a Stop at the dialog would need the same "last attempt" warning as a
cancel. This part types **no password**; it has its own faillock check
before and after. Run it in the graphical session (back on tty2 if Part B
ran), before the cleanup: it needs the throwaway action.

**C1. Before.** In terminal 1:

```sh
pkcheck --revoke-temp
faillock --user "$USER"
pkcheck --action-id org.archmaid.keepcheck.k7q2 --process $$; echo "rc=$?"
```

Write down the faillock rows (case **K0**). **Run Part C only if there
are 0 rows**; otherwise skip to the cleanup. `pkcheck` must give `rc=2`
(nothing kept, so the next step really shows a dialog); if it gives
`0`, wait 5 minutes and repeat C1.

**C2. Start pkexec, but don't answer.**

```sh
pkexec /usr/bin/true archmaid-keepcheck-k7q2 & p=$!; echo "pkexec pid $p"
```

The polkit-gnome dialog appears and **takes the keyboard focus**: from
now on every key you press goes into its password field, and Enter would
submit it as a wrong password (one faillock row). Check the message and
the action id (mouse only: Details is an expander) as in "What you are
looking for". **Type nothing, and don't press Cancel**: leave the dialog
open.

**C3. Kill it, as archmaid's Stop does** (within about 30 s of C2, in
terminal 1). First **click into terminal 1** with the mouse, so the
keyboard goes to the terminal; the dialog stays on screen. Check that the
terminal has the focus (its cursor is solid, or your typing shows up
there, not as dots in the dialog) before you press Enter. Then type:

```sh
kill -KILL "$p"; wait "$p"; echo "rc=$?"
```

Expected `rc=137`. Case **K1**: note whether the dialog closed by
itself, and how quickly. If it is still open after 10 s, press Cancel
and write that down: the dialog outliving pkexec is a finding too, but
the faillock count then can't tell the kill from the cancel.

**C4. After** (no password; about 10 s after C3, so a late failure is counted too):

```sh
faillock --user "$USER"
```

Case **K2**: the rows now. One more row than at K0 means a killed pkexec
costs a failure like a cancel; the same count means it doesn't. Write
it down **before** the cleanup, whose `sudo` password resets the count.
The stop rule still holds: with 2 or more rows, wait before the cleanup.

## Cleanup (always, also after a stop)

Mind the stop rule: with 2 or more faillock rows, wait before the `sudo`.

13. In terminal 1:

    ```sh
    pkcheck --revoke-temp
    sudo rm -f /usr/share/polkit-1/actions/org.archmaid.keepcheck.k7q2.policy
    rm -r "$d"
    pkaction --action-id org.archmaid.keepcheck.k7q2; echo "rc=$?"
    ls /usr/share/polkit-1/actions | grep -c keepcheck
    ```

    `pkaction` must fail ("No action with action id …", non-zero rc) and the
    `grep -c` must print `0`. (If `$d` is gone because the terminal was
    closed, find it with `ls -d /tmp/tmp.*` and remove only the one holding
    `org.archmaid.keepcheck.k7q2.policy`.) Close terminal 2.

14. **faillock last:**

    ```sh
    faillock --user "$USER"
    ```

    Expected: 0 rows. Every correct password resets the count. Write down
    what it shows.

Checklist:

- [ ] `/usr/share/polkit-1/actions/org.archmaid.keepcheck.k7q2.policy` removed; `pkaction` doesn't know the id
- [ ] scratch directory `$d` removed
- [ ] `pkcheck --revoke-temp` run in the graphical session (on tty3 it removes nothing, see step 12; `exit` there ends the only process that could use T1's authorization)
- [ ] step 11's `pkttyagent` stopped (`kill "$a"` in step 11), logged out of tty3, back on tty2
- [ ] terminal 2 closed
- [ ] Part C's pkexec is gone (`kill -0 "$p"` fails), if Part C ran
- [ ] `faillock --user "$USER"` shows 0 rows (or the count is written down below)

## Results (fill in)

**Part A, the owner's machine (r518.59f90b3, 2026-10-11/12):** G3 `rc=0`
(a new process authorised without a prompt) and G5 showed the kept
authorization for a `unix-session:` subject: in the graphical session, with
polkit-gnome as the agent, `auth_admin_keep` is scoped to the **session**.
polkitd keeps it for the scope of the agent that asked, so archmaid-gui's own
pkttyagent (registered for its process) keeps it process-scoped, and only the
fallback through the desktop's agent is session-scoped. Recorded in HANDOVER
("`auth_admin_keep` is kept for the scope of the agent that asked"); the
question is closed. Parts B and C weren't reported. The table stays for a
re-run.


Date: ______  polkit `pkcheck --version`: ______  faillock rows at step 1: ___ / at step 14: ___

| Case | Attempts (answers) | Prompts seen | faillock rows after | Exit code(s) | Notes (banner action id, pid, `--list-temp` subject) |
|---|---|---|---|---|---|
| G0 baseline | 0 | 0 | | | expected 2 |
| G1 pkexec | 1 (right) | | | | banner id: |
| G2 same shell | 0 | 0 | | | |
| G3 new process (`sh -c`) | 0 | 0 | | | |
| G4 new terminal | 0 | 0 | | | |
| G5 `--list-temp` | 0 | 0 | | | subject: |
| T0 tty3 baseline | 0 | 0 | | | minutes since G1: |
| T1 pkexec (via pkttyagent) | 1 (right) | | | | banner id: |
| T2 same shell | 0 | 0 | | | |
| T3 new process (`sh -c`) | 0 | 0 | | | |
| T4 `pkexec --disable-internal-agent` from a new process | 0 | | | | |
| T5 `--list-temp` | 0 | 0 | | | expected: no output; T2's tmpauthz id: |
| K0 faillock before | 0 | 0 | | | must be 0 rows |
| K1 pkexec SIGKILLed in the dialog | 0 | | | | dialog closed by itself? after how long? cancelled? |
| K2 faillock after | 0 | 0 | | | |

**Answer:** in the graphical session, `auth_admin_keep` is scoped to
☐ the session ☐ the requesting process. On tty3: ☐ the session ☐ the
requesting process ☐ not run. A pkexec SIGKILLed in the dialog
☐ costs a faillock failure ☐ doesn't ☐ inconclusive (dialog cancelled)
☐ not run.

Send the filled-in table back; it goes into `docs/polkit-live-test.md` and
closes (or rewords) the HANDOVER open item.
