# archmaid live polkit/faillock test — results (2026-10-04)

Run per `LIVE-TEST-AGENT.md` against throwaway user `amtest`, plus one retest of
case F with only the keep action installed. No STOPs. The invoking user's
(`enzord`) faillock was unchanged throughout, and cleanup was verified after
both runs: no user, files, actions or `/root/amtest-live` left.

## Summary

| Question | Answer |
|---|---|
| 1. Failures per wrong polkit password | **1**. pkttyagent doesn't re-prompt: one dialog = one attempt |
| 2. Does cancelling count as a failure? | **Yes**. EOF and Ctrl-C each add 1 faillock row |
| 3. `auth_admin_keep` scope | **The requesting process** (pkexec's parent), when that process has no session. Must be re-checked in a real session (tty3) |
| 4. pkexec honours `exec.argv1` | **Yes**, but an action **without** argv1 for the same `exec.path` can take calls meant for an argv1-specific one (see trap below) |

Other findings:

- A successful authentication resets faillock rows to 0 (B2).
- After 3 failures the account is locked. Even the right password fails with rc 127,
  and no new row is added. Before the prompt, pkttyagent prints
  `Info: The account is locked due to 3 failed logins.` / `Info: (10 minutes left to unlock)`.
  The app could detect this text.
- A dismissed or failed pkexec gives rc **127** in every case (no 126 seen).
- `su` has no faillock (0 rows). `sudo` for a user not in sudoers refuses
  without asking for a password (0 rows).

### Trap for archmaid's policy design

In the first run the policy had `.true` (no argv1), `.keep` (argv1 `keep`) and
`.argv1` (argv1 `argv1-match`), all with `exec.path=/usr/bin/true`.
`pkexec /usr/bin/true keep` showed the banner for **`org.archmaid.test.true`**,
not `.keep`. `argv1-match` did select its own action. With `.true` removed
(the retest), `keep` selected `.keep` at once.

Likely cause: pkexec takes the first action whose path matches, and an action
without argv1 matches any argv1. Which one wins then depends on the order
actions are listed in, not on which is more specific. (This was not checked
against the source, which wasn't on disk.)

**Rule:** never ship an action without argv1 next to argv1-specific actions
for the same helper path.

## Versions

```
pkexec version 127
pkttyagent version 127
polkit 127-3.1
pam 1.7.3-1.1
sudo 1.9.17.p2-6.1
systemd 262-1
7.2.8-2-cachyos
deny: default (3)
--- polkit-1:
cat: /etc/pam.d/polkit-1: No such file or directory
--- system-auth auth:
auth       required                    pam_faillock.so      preauth
-auth      [success=2 default=ignore]  pam_systemd_home.so
auth       [success=1 default=bad]     pam_unix.so          try_first_pass nullok
auth       [default=die]               pam_faillock.so      authfail
auth       optional                    pam_permit.so
auth       required                    pam_env.so
auth       required                    pam_faillock.so      authsucc
--- /usr/lib/pam.d/polkit-1:
#%PAM-1.0

auth       include      system-auth
account    include      system-auth
password   include      system-auth
session    include      system-auth
```

## Run 1 — full case list

| Case | Attempts (answers) | Prompts seen | faillock rows after | Exit code(s) | Notes (journal excerpt, banner action id) |
|---|---|---|---|---|---|
| A | wrong, cancel | 1 | 1 | 127 | **No re-prompt** after the wrong password: AUTHENTICATION FAILED, then pkexec exits. Journal: `FAILED to authenticate … org.archmaid.test.true`. Banner `.true` |
| B | wrong / right (two-step B1, B2) | 1 / 1 | 1 / **0** | 127 / 0 | **The success resets the rows to 0** (the authsucc row). No journal line for B2 |
| C1–C3 | wrong ×3 | 1 each | 1 / 2 / 3 | 127 ×3 | One `FAILED to authenticate` journal line per attempt |
| C4 | right while locked | 1 | 3 (no new row) | **127** | Fails despite the right password. `Info: The account is locked due to 3 failed logins.` `Info: (10 minutes left to unlock)` |
| D sudo -S | wrong | — | 0 | 1 | **no auth**: `Sorry, user amtest may not run sudo on enz-arch-btw.` |
| D sudo -A | wrong, cancel | — | 0 | 1 | **no auth**: same message, askpass never called |
| E su | wrong | 1 | **0** | 1 | `su: Authentication failure`. su has no faillock |
| F1 / F2 | right, keep ×2 / keep | **2** / 1 | 1 / 0 | 0, 127 / 0 | **Banner `org.archmaid.test.true`, not `.keep`**, so keep was not tested (see retest). F1's 2nd prompt got EOF → rc 127 and +1 row |
| G1 / G2 | argv1-match / nomatch | 0 / 1 | 0 / 0 | 0 / 0 | G1: no prompt (`.argv1`, allow=yes). G2: banner `org.archmaid.test.true` (fallthrough) |
| H1 / H2 | eof / ctrl-c | 1 / 1 | **1 / 1** | 127 / — | H1: `Got unexpected EOF…`, AUTHENTICATION FAILED. H2: Ctrl-C killed the whole wrapper (no pkexec rc). Journal subject `[<unknown>]`. Both write `FAILED to authenticate` |

## Run 2 — keep retest (only `org.archmaid.test.keep` installed)

| Case | Attempts (answers) | Prompts seen | faillock rows after | Exit code(s) | Notes |
|---|---|---|---|---|---|
| F1 | right, then `keep` ×2 from the same parent | **1** | 0 | 0, 0 | Banner **`org.archmaid.test.keep`**. 2nd pkexec authorised without a prompt |
| F2 | right, then `keep` from a new parent process (within about 30 s) | **1** | 0 | 0 | **Prompted again**: keep does not carry over to a new process |

Caveat: the test subjects ran outside any logind session. My understanding is
that polkit ties keep to the session when there is one, so the GUI (active
session) may share keep across processes. Re-check on tty3.

## Deviations

- `/etc/pam.d/polkit-1` doesn't exist. polkit 127 ships `/usr/lib/pam.d/polkit-1`
  (it includes system-auth, so faillock applies).
- polkitd logged no "compiling rules" line. The rules were demonstrably active.
- Case B used the two-step form (B1/B2), because A showed no re-prompt.
- The session ran as root with `SUDO_USER=enzord`. The scripts and policy files
  were written with an editor tool instead of heredocs, with the same content as
  the instructions.
