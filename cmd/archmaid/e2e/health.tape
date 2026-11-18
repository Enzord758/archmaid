# System health: the menu entry opens the screen, the checks run in the
# background (every tool they ask is a shim; systemctl lists one failed
# unit via SHIM_FAILED), the look row shows with its detail, `c` prints its
# command, esc returns to the menu. Driven by e2e/run.sh (scratch HOME,
# shimmed PATH).

Output e2e/health.txt

Set Shell "bash"
Set Width 1100
Set Height 900
Set FontSize 16
Set TypingSpeed 25ms

Hide
Type "./archmaid --dryrun"
Enter
Sleep 1.5s
Show

# Menu row 7 (System health) -> the checks run -> the rows.
Down 7
Sleep 400ms
Enter
Sleep 5s
Type "c"
Sleep 800ms
Escape
Sleep 500ms
Type "q"
Sleep 500ms
