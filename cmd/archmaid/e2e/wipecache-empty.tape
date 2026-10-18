# Delete entire package cache with an empty cache: a notice, no confirm, nothing run.
# Driven by e2e/run.sh (scratch HOME, shimmed PATH).

Output e2e/wipecache-empty.txt

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

# Menu row 4 (Delete entire package cache) -> notice -> dismiss -> quit.
Down 4
Sleep 400ms
Enter
Sleep 1.5s
Enter
Sleep 400ms
Type "q"
Sleep 500ms
