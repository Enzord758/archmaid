# Repair finds a broken package (shim), rechecks it through the sudo shim,
# shows the reinstall confirm and declines it. SHIM_BROKEN names the package.
# Driven by e2e/run.sh (scratch HOME, shimmed PATH).

Output e2e/repair-found.txt

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

Down 2
Sleep 400ms
Enter
Sleep 2s

# "Reinstall 1 broken package(s)?" -> hold it on screen, then no.
Sleep 1s
Type "n"
Sleep 1s

# Summary -> menu -> quit.
Enter
Sleep 400ms
Type "q"
Sleep 500ms
