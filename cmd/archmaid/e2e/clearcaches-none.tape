# Clear caches: the pick opens with both rows and the package cache's warning, nothing ticked.
# The tape presses Enter on it as it is: an empty pick runs nothing, and asks for no password.
# Driven by e2e/run.sh (scratch HOME, shimmed PATH).

Output e2e/clearcaches-none.txt

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

# The pick: hold it on screen, go with nothing ticked.
Sleep 1s
Enter
Sleep 1s

# Back on the menu: quit.
Type "q"
Sleep 500ms
