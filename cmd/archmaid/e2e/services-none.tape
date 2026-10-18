# Disable unused services finds nothing: systemctl is a shim listing
# fake-alpha and fake-beta, both "disabled" (SHIM_UNIT_STATE). The notice says
# what was checked and where the record is; the record goes to the dry-run
# slot. Driven by e2e/run.sh (scratch HOME, shimmed PATH).

Output e2e/services-none.txt

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

# Menu row 6 (Disable unused services) -> notice -> dismiss -> quit.
Down 6
Sleep 400ms
Enter
Sleep 2s
Enter
Sleep 500ms
Type "q"
Sleep 500ms
