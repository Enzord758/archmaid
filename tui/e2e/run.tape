# VHS e2e tape for the maintenance TUI.
#
# Drives the built binary in a real headless pty under --dryrun through the
# "Clear user cache" action (menu → confirm → dry-run → summary), then quits.
# Nothing mutating runs: the step is stubbed and logged as DRYRUN.
#
# Run it with e2e/run.sh, which points HOME at a scratch dir and asserts on the
# resulting ~/.maintenance_dryrun.log. The binary is expected at ../tui (build
# with `go build -o tui .` from the module root first).

Output e2e/run.txt

Set Shell "bash"
Set Width 1100
Set Height 650
Set FontSize 16
Set TypingSpeed 25ms

# Launch the app (hidden so the recording starts on the menu).
Hide
Type "./tui --dryrun"
Enter
Sleep 1.5s
Show

# Menu → move to "Clear user cache" (row 1) and activate it.
Down
Sleep 400ms
Enter
Sleep 500ms

# Confirm modal: "Clear all user cache?" → yes.
Type "y"
Sleep 1s

# Summary screen → back to the menu, then quit.
Enter
Sleep 400ms
Type "q"
Sleep 500ms
