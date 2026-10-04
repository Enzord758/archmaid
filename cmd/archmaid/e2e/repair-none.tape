# Repair finds nothing: pacman -Qkq is clean, so a notice and no sudo.
# Driven by e2e/run.sh (scratch HOME, shimmed PATH).

Output e2e/repair-none.txt

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

# Menu row 2 (Repair broken packages) -> notice -> dismiss -> quit.
Down 2
Sleep 400ms
Enter
Sleep 2s
Enter
Sleep 500ms
Type "q"
Sleep 500ms
