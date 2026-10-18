# Disable unused services: pick one of two listed services, confirm, dry-run.
# systemctl is a shim listing fake-alpha and fake-beta; the tape toggles beta.
# Driven by e2e/run.sh (scratch HOME, shimmed PATH).

Output e2e/services.txt

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

# Menu row 6 (Disable unused services).
Down 6
Sleep 400ms
Enter
Sleep 1.5s

# Cursor starts on alpha: move to beta, toggle it, then confirm.
Down
Sleep 300ms
Space
Sleep 800ms
Enter
Sleep 1.5s

# Summary -> menu -> quit.
Enter
Sleep 400ms
Type "q"
Sleep 500ms
