# Full Cleanup's gate answered no: back on the menu, nothing run, a cancelled dry-run last-run record.
# Driven by e2e/run.sh (scratch HOME, shimmed PATH).

Output e2e/fullcleanup-gate-declined.txt

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

Enter
Sleep 800ms
Type "n"
Sleep 1s

# Back on the menu: the cursor still works (Down), then quit.
Down
Sleep 400ms
Type "q"
Sleep 500ms
