# Menu -> Clear caches -> the pick (~/.cache ticked alone) -> dry-run -> summary, then quit.
# ~/.cache is seeded by run.sh (SEED_USER_CACHE); paccache is the shim whose dry run prints a total.
# The pick is the confirm: no second question, no password, no sudo.
# Driven by e2e/run.sh (scratch HOME, shimmed PATH).

Output e2e/clearcaches-user.txt

Set Shell "bash"
Set Width 1100
Set Height 650
Set FontSize 16
Set TypingSpeed 25ms

Hide
Type "./archmaid --dryrun"
Enter
Sleep 1.5s
Show

# Menu row 1 (Clear caches): it measures both caches, then the pick opens.
Down
Sleep 400ms
Enter
Sleep 2s

# The cursor starts on ~/.cache: tick it, go.
Space
Sleep 600ms
Enter
Sleep 1.5s

# Summary -> menu -> quit.
Enter
Sleep 400ms
Type "q"
Sleep 500ms
