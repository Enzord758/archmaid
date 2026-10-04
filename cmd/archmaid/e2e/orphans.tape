# Full Cleanup: the orphan confirm must show "2 ... 3 total" (pacman is a shim).
# Declines the optional package-cache wipe, accepts the orphan removal.
# Driven by e2e/run.sh (scratch HOME, shimmed PATH).

Output e2e/orphans.txt

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

# Menu row 0 (Full Cleanup) -> "Proceed with full cleanup?" -> yes.
Enter
Sleep 800ms
Type "y"
Sleep 2s

# "Delete ENTIRE package cache?" -> no.
Type "n"
Sleep 1s

# Orphan confirm: hold it on screen, then accept.
Sleep 1s
Type "y"
Sleep 2s

# Summary -> menu -> quit.
Enter
Sleep 400ms
Type "q"
Sleep 500ms
