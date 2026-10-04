# Menu -> Clear user cache -> confirm -> dry-run -> summary, then quit.
# Driven by e2e/run.sh (scratch HOME, shimmed PATH).

Output e2e/clear-cache.txt

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

Down
Sleep 400ms
Enter
Sleep 500ms

Type "y"
Sleep 1s

Enter
Sleep 400ms
Type "q"
Sleep 500ms
