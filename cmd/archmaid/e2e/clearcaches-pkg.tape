# Clear caches under --dryrun with the package cache ticked alone: the step only logs what it would run.
# Both caches are measured first (paccache is a shim whose dry run prints a total; ~/.cache is seeded).
# Driven by e2e/run.sh (scratch HOME, shimmed PATH).

Output e2e/clearcaches-pkg.txt

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

# Menu row 1 (Clear caches).
Down
Sleep 400ms
Enter
Sleep 2s

# The pick, nothing ticked: hold it on screen, tick the package cache (row 2), go.
Sleep 1s
Down
Sleep 300ms
Space
Sleep 600ms
Enter
Sleep 1.5s

# Summary -> menu -> quit.
Enter
Sleep 400ms
Type "q"
Sleep 500ms
