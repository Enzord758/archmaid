# Live polkit/faillock test: instructions for an agent with sudo

You are testing polkit and PAM behaviour on an Arch Linux machine. You don't
need to know the app. It will run root actions through `pkexec` and polkit,
and Arch locks an account after **3** failed passwords
(`pam_faillock deny=3`). We must learn four things:

1. how many faillock failures **one wrong password** costs in a polkit prompt
   (1 or 2);
2. whether **cancelling** the prompt counts as a failure;
3. whether `auth_admin_keep` is scoped to the **requesting process**;
4. whether pkexec (polkit 127) selects an action by
   `org.freedesktop.policykit.exec.argv1`.

Everything runs against a throwaway user `amtest`. Run each command below as
root (`sudo …`). Shell state doesn't need to survive between commands, because
all state lives in `/root/amtest-live`.

## Safety rules

- **Never** type, read, store or guess the invoking user's password. If `sudo`
  would prompt, STOP and ask the user to run `sudo -v` first.
- **Never** touch the faillock state of a real user. You only *read* the
  invoking user's (`faillock --user "$SUDO_USER"`). If it changes at any
  point, STOP.
- **Never** edit anything in `/etc/pam.d/`, `/etc/security/`, sudoers or
  polkit's shipped rules. Only create the three files named below.
- Leave ≥ 10 s between password attempts. Never run cases in parallel.
- On **any** unexpected result (an error not listed here, a prompt on the
  user's desktop, a changed baseline), STOP: run **Cleanup**, then report what
  you saw.
- Always run **Cleanup**, even after a STOP. Collect the case outputs first;
  cleanup deletes them.

## 0. Preflight (STOP if any check fails)

```sh
sudo -n true || echo "STOP: sudo needs a password"
id amtest 2>/dev/null && echo "STOP: user amtest already exists"
sudo ls -d /etc/polkit-1/rules.d/49-amtest.rules /usr/share/polkit-1/actions/org.archmaid.test.policy /root/amtest-live 2>/dev/null && echo "STOP: leftovers from an earlier run"
systemctl is-active polkit || echo "STOP: polkit not running"
for c in pkexec pkttyagent pkaction faillock systemd-run python3 runuser chpasswd; do command -v $c >/dev/null || echo "STOP: missing $c"; done
sudo install -d -m 0700 /root/amtest-live
sudo sh -c 'echo "$SUDO_USER" > /root/amtest-live/me; faillock --user "$SUDO_USER" > /root/amtest-live/me.before'
sudo sh -c '{ pkexec --version; pkttyagent --version; pacman -Q polkit pam sudo systemd; uname -r;
  grep -E "^\s*deny" /etc/security/faillock.conf || echo "deny: default (3)";
  echo "--- polkit-1:"; cat /etc/pam.d/polkit-1; echo "--- system-auth auth:"; grep -E "^\s*-?auth" /etc/pam.d/system-auth; } > /root/amtest-live/versions'
```

Nothing is overwritten (preflight stops on leftovers), so there is nothing to
back up.

## 1. Setup

```sh
sudo sh -c 'python3 -c "import secrets; print(secrets.token_urlsafe(18))" > /root/amtest-live/pw; chmod 600 /root/amtest-live/pw'
sudo useradd -m -s /bin/bash amtest          # deliberately NOT in wheel
sudo sh -c 'printf "amtest:%s\n" "$(cat /root/amtest-live/pw)" | chpasswd'
sudo faillock --user amtest --reset
```

The test policy has three actions on `/usr/bin/true`: `.true` (auth_admin, no argv1),
`.keep` (auth_admin_keep, argv1 `keep`) and `.argv1` (yes, argv1 `argv1-match`):

```sh
sudo tee /usr/share/polkit-1/actions/org.archmaid.test.policy >/dev/null <<'EOF'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE policyconfig PUBLIC "-//freedesktop//DTD PolicyKit Policy Configuration 1.0//EN"
 "http://www.freedesktop.org/standards/PolicyKit/1/policyconfig.dtd">
<policyconfig>
  <action id="org.archmaid.test.true">
    <description>archmaid test (auth_admin)</description><message>archmaid test: true</message>
    <defaults><allow_any>no</allow_any><allow_inactive>no</allow_inactive><allow_active>auth_admin</allow_active></defaults>
    <annotate key="org.freedesktop.policykit.exec.path">/usr/bin/true</annotate>
  </action>
  <action id="org.archmaid.test.keep">
    <description>archmaid test (keep)</description><message>archmaid test: keep</message>
    <defaults><allow_any>no</allow_any><allow_inactive>no</allow_inactive><allow_active>auth_admin_keep</allow_active></defaults>
    <annotate key="org.freedesktop.policykit.exec.path">/usr/bin/true</annotate>
    <annotate key="org.freedesktop.policykit.exec.argv1">keep</annotate>
  </action>
  <action id="org.archmaid.test.argv1">
    <description>archmaid test (argv1, no auth)</description><message>archmaid test: argv1</message>
    <defaults><allow_any>no</allow_any><allow_inactive>no</allow_inactive><allow_active>yes</allow_active></defaults>
    <annotate key="org.freedesktop.policykit.exec.path">/usr/bin/true</annotate>
    <annotate key="org.freedesktop.policykit.exec.argv1">argv1-match</annotate>
  </action>
</policyconfig>
EOF
```

**The non-interactive problem.** You have no tty3, so your processes are not
in an *active local* session, and `allow_active` never applies to them. The
rules file therefore returns the same results explicitly for `amtest`,
whatever the session. A second issue: if amtest's processes ran inside the
user's desktop session, polkit could send the prompt to the user's desktop
agent. So every attempt runs in a transient `systemd-run --uid=amtest --pty`
service, which is outside any logind session. A `pkttyagent --process` is
registered for the exact shell that calls pkexec. pkexec's polkit subject is
its **parent** process, so that is the process the agent registers for.

```sh
sudo tee /etc/polkit-1/rules.d/49-amtest.rules >/dev/null <<'EOF'
// TEMPORARY (archmaid live test). amtest authenticates as itself, only for org.archmaid.test.*
polkit.addAdminRule(function(action, subject) {
    if (subject.user == "amtest" && action.id.indexOf("org.archmaid.test.") == 0)
        return ["unix-user:amtest"];
});
polkit.addRule(function(action, subject) {
    if (subject.user != "amtest") return polkit.Result.NOT_HANDLED;
    if (action.id == "org.archmaid.test.true")  return polkit.Result.AUTH_ADMIN;
    if (action.id == "org.archmaid.test.keep")  return polkit.Result.AUTH_ADMIN_KEEP;
    if (action.id == "org.archmaid.test.argv1") return polkit.Result.YES;
    return polkit.Result.NOT_HANDLED;
});
EOF
sudo tee /home/amtest/wrap.sh >/dev/null <<'EOF'
#!/bin/sh
# One tty agent for THIS shell (pkexec's subject), then one pkexec per arg ("-" = no argv1).
pkttyagent --process $$ & agent=$!
sleep 1
for a in "$@"; do
  if [ "$a" = "-" ]; then pkexec /usr/bin/true; else pkexec /usr/bin/true "$a"; fi
  echo "[wrap] pkexec argv1=$a rc=$?"
done
kill $agent 2>/dev/null
EOF
sudo tee /home/amtest/askpass.sh >/dev/null <<'EOF'
#!/bin/sh
# First call answers a wrong password, later calls cancel (like the TUI's askpass).
[ -e "$HOME/.asked" ] && exit 1
touch "$HOME/.asked"; echo definitely-wrong
EOF
sudo chmod 0755 /home/amtest/wrap.sh /home/amtest/askpass.sh
sudo journalctl -u polkit --since "-1min" --no-pager -o cat | tail -3   # expect "...compiling and executing N rules"
pkaction --action-id org.archmaid.test.true >/dev/null && echo "policy loaded"
```

The driver answers `Password:` prompts on a pty with only the Python stdlib,
so `expect` isn't needed:

```sh
sudo tee /root/amtest-live/drive.py >/dev/null <<'EOF'
import os, pty, re, select, sys, time
# drive.py ANSWERS -- CMD...   ANSWERS: comma list of right|wrong|eof|ctrlc (extra prompts get eof)
sep = sys.argv.index("--"); answers = [a for a in sys.argv[1].split(",") if a]; cmd = sys.argv[sep + 1:]
pw = open("/root/amtest-live/pw").read().strip()
keys = {"right": pw.encode() + b"\n", "wrong": b"definitely-wrong\n", "eof": b"\x04", "ctrlc": b"\x03"}
pid, fd = pty.fork()
if pid == 0:
    os.execvp(cmd[0], cmd)
out, buf, prompts, end = b"", b"", 0, time.time() + 120
while time.time() < end:
    if not select.select([fd], [], [], 1)[0]: continue
    try: chunk = os.read(fd, 4096)
    except OSError: break
    if not chunk: break
    out += chunk; buf += chunk
    if re.search(rb"[Pp]assword[^\n]*:\s*$", buf):
        buf = b""; prompts += 1
        if prompts > 1: time.sleep(10)             # pacing between attempts
        a = answers.pop(0) if answers else "eof"
        time.sleep(0.5); os.write(fd, keys[a])
else:
    os.kill(pid, 9); print("[drive] TIMEOUT")
_, st = os.waitpid(pid, 0)
sys.stdout.write(out.decode(errors="replace").replace(pw, "<pw>"))
print(f"\n[drive] prompts={prompts} exit={os.waitstatus_to_exitcode(st)} unused={answers}")
EOF
sudo tee /root/amtest-live/case.sh >/dev/null <<'EOF'
#!/bin/bash
# case.sh LABEL ANSWERS WRAP-ARGS...   (no reset here: reset explicitly where a case says so)
label=$1 answers=$2; shift 2; L=/root/amtest-live; sleep 10
since=$(date '+%F %T')
{ echo "### $label  answers=$answers  wrap=$*"
  python3 $L/drive.py "$answers" -- systemd-run --quiet --uid=amtest --pty --wait --collect \
     --working-directory=/home/amtest /bin/sh /home/amtest/wrap.sh "$@"
  echo "--- faillock amtest rows: $(faillock --user amtest | tail -n +3 | grep -c .)"
  echo "--- journal:"; journalctl -u polkit --since "$since" --no-pager -o cat
  faillock --user "$(cat $L/me)" | diff -q - $L/me.before >/dev/null \
     && echo "--- invoking user's faillock unchanged" || echo "!!! STOP: invoking user's faillock CHANGED"
} 2>&1 | tee "$L/$label.out"
EOF
sudo chmod 0700 /root/amtest-live/case.sh
```

## 2. Cases

Run the cases in order, one command at a time, and read each output before
going on. A `STOP` line, a `TIMEOUT`, or a password dialog on the user's
desktop means STOP. `C=/root/amtest-live/case.sh`; `R` resets amtest.

```sh
C=/root/amtest-live/case.sh; R() { sudo faillock --user amtest --reset; }
# A  one wrong, then cancel (in the same dialog if the agent re-prompts)
R; sudo $C A wrong,eof -
# B  wrong then right. If A showed prompts=1 (no re-prompt), use the two-step form instead
R; sudo $C B wrong,right -        # or:  R; sudo $C B1 wrong -; sudo $C B2 right -
# C  lockout: three wrong with no reset in between, then the RIGHT password
R; sudo $C C1 wrong -; sudo $C C2 wrong -; sudo $C C3 wrong -; sudo $C C4 right -
# D  sudo baselines (amtest is not in sudoers), rows counted after each
R; sudo runuser -u amtest -- sh -c "printf 'wrong\n' | sudo -S -k -v"; echo "rc=$?"; sudo faillock --user amtest
sleep 10; R; sudo runuser -u amtest -- env SUDO_ASKPASS=/home/amtest/askpass.sh sudo -A -k -v; echo "rc=$?"; sudo faillock --user amtest
# E  su baseline
sleep 10; R; sudo python3 /root/amtest-live/drive.py wrong -- runuser -u amtest -- su amtest -c true; sudo faillock --user amtest
# F  keep scope: two pkexec from one parent, then (within 1 min) a new process
R; sudo $C F1 right keep keep; sudo $C F2 right keep
# G  exec.argv1: matching argv1 (allowed, no prompt), then a non-matching one (falls through)
R; sudo $C G1 "" argv1-match; sudo $C G2 right nomatch
# H  cancel straight away: EOF, then Ctrl-C
R; sudo $C H1 eof -; R; sudo $C H2 ctrlc -
```

| Case | Expect / what we need |
|---|---|
| A | `prompts=`: does the agent re-prompt after a wrong password? Rows after: **1 or 2?** rc 126/127 |
| B | final rc 0; **does the success reset rows to 0?** |
| C | rows 1, 2, 3 after C1–C3; **C4 fails (rc 127) despite the right password** while locked |
| D | **2** rows each. If sudo says "not in the sudoers file" without asking, record "no auth" |
| E | **0** rows (su's PAM stack has no faillock on Arch) |
| F | F1: one prompt, both rc 0. **F2 (new parent process): prompted or not?** |
| G | G1: **0 prompts, rc 0** (the argv1 action). G2: a prompt naming `org.archmaid.test.true` (fallthrough), rc 0 |
| H | **does a cancel add a row?** rc 126 (dismissed) or 127 |

If a result differs from "Expect", don't retry blindly. Record it. Retry only
if it is clearly a harness problem (for example `No authentication agent
found`: raise `sleep 1` in wrap.sh to `sleep 3` once).

F has a caveat. Your subjects have no session, while the real GUI runs in an
active session. Report F as measured; the user re-checks it on tty3.

## 3. Cleanup (always)

```sh
sudo faillock --user amtest --reset
sudo pkill -KILL -u amtest; sleep 1
sudo userdel -r amtest
sudo rm -f /etc/polkit-1/rules.d/49-amtest.rules /usr/share/polkit-1/actions/org.archmaid.test.policy /run/faillock/amtest
id amtest 2>/dev/null && echo "LEFTOVER: user"
ls /etc/polkit-1/rules.d/49-amtest.rules /usr/share/polkit-1/actions/org.archmaid.test.policy /home/amtest 2>/dev/null && echo "LEFTOVER: files"
pkaction | grep org.archmaid.test && echo "LEFTOVER: action"
sudo sh -c 'faillock --user "$(cat /root/amtest-live/me)" | diff -q - /root/amtest-live/me.before && echo "invoking user faillock unchanged"'
sudo rm -rf /root/amtest-live      # only after you copied the outputs into your report
```

polkitd reloads rules and actions by itself, so don't restart anything.

## 4. Report (fill in and give to the user verbatim)

```markdown
## archmaid live polkit/faillock test — <date>
Versions: <paste /root/amtest-live/versions verbatim>

| Case | Attempts (answers) | Prompts seen | faillock rows after | Exit code(s) | Notes (journal excerpt, banner action id) |
|---|---|---|---|---|---|
| A | wrong, cancel | | | | re-prompt after wrong? |
| B | wrong, right | | | | success resets? |
| C1–C3 | wrong ×3 | | 1/2/3? | | |
| C4 | right while locked | | | | |
| D sudo -S | wrong | — | | | |
| D sudo -A | wrong, cancel | — | | | |
| E su | wrong | | | | |
| F1 / F2 | right, keep ×2 / keep | | | | F2 prompted? |
| G1 / G2 | argv1-match / nomatch | | | | action ids |
| H1 / H2 | eof / ctrl-c | | | | |

Answers: (1) one wrong polkit password costs __ failures. (2) Cancel counts: yes/no.
(3) auth_admin_keep is scoped to: process / session / other. (4) exec.argv1 honoured: yes/no.
Invoking user's faillock unchanged throughout: yes/no. Cleanup verified: yes/no.
Deviations / STOPs: …
```
