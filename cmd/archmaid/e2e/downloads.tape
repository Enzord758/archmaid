# Delete old downloads with --downloads <scratch folder>. Age is ctime, which
# touch -d cannot set, so files with an old mtime are NOT old: notice, nothing deleted.
# Driven by e2e/run.sh (scratch HOME, shimmed PATH).

Output e2e/downloads.txt

Set Shell "bash"
Set Width 1100
Set Height 650
Set FontSize 16
Set TypingSpeed 25ms

Hide
Type "./archmaid --dryrun --downloads $DL_DIR"
Enter
Sleep 1.5s
Show

Down 3
Sleep 400ms
Enter
Sleep 1.5s
Enter
Sleep 500ms
Type "q"
Sleep 500ms
