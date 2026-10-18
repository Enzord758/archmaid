# Delete entire package cache under --dryrun: confirmed, the step only logs what it would run.
# Driven by e2e/run.sh (scratch HOME, shimmed PATH).

Output e2e/wipecache.txt

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

Sleep 1s
Type "y"
Sleep 1.5s

# Summary -> menu -> quit.
Enter
Sleep 400ms
Type "q"
Sleep 500ms
