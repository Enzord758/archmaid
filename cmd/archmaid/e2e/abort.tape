# Abort: Full Cleanup's first step (the AUR audit) blocks on a shimmed
# `pacman -Qqm`; ctrl+c, confirm the abort, land back on the menu, quit.
# (Under --dryrun every other step is stubbed and finishes instantly, so the
# in-process audit is the only step that can be held open.)
# Driven by e2e/run.sh (scratch HOME, shimmed PATH, SHIM_BLOCK_QQM set).

Output e2e/abort.txt

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

Enter
Sleep 800ms
Type "y"
Sleep 1s

Ctrl+C
Sleep 800ms
Type "y"
Sleep 1s

# Back on the menu: the cursor still works (Down), then quit.
Down
Sleep 400ms
Type "q"
Sleep 500ms
