# Clear caches with both caches empty (no ~/.cache, an empty package cache): a notice, no pick, nothing run.
# Driven by e2e/run.sh (scratch HOME, shimmed PATH).

Output e2e/clearcaches-empty.txt

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

# Menu row 1 (Clear caches) -> notice -> dismiss -> quit.
Down
Sleep 400ms
Enter
Sleep 2s
Enter
Sleep 400ms
Type "q"
Sleep 500ms
