# Delete entire package cache: paccache is a shim whose dry run (-dk0) prints a total.
# The confirm shows it with the warning; the tape answers no, so nothing runs.
# Driven by e2e/run.sh (scratch HOME, shimmed PATH).

Output e2e/wipecache-declined.txt

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

# Menu row 4 (Delete entire package cache).
Down 4
Sleep 400ms
Enter
Sleep 1.5s

# The confirm: hold it on screen, answer no.
Sleep 1s
Type "n"
Sleep 1s

# Summary -> menu -> quit.
Enter
Sleep 400ms
Type "q"
Sleep 500ms
