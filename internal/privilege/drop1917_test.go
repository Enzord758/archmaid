package privilege

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// sudo1917 is a sudo stand-in that follows sudo 1.9.17's parse_args.c for the
// flags DropTicket can use: a bare -k (no command, no -s/-i) becomes
// MODE_INVALIDATE with valid_flags = 0 (parse_args.c:540-545), so any other
// flag, -n included, fails `(flags & valid_flags) != flags` (line 567) with
// usage and exit 1. -v is the validate mode, which may prompt: the shim
// records that. It drops the ticket (removes the ticket file) only in a mode
// that sudo would accept.
const sudo1917 = `#!/bin/sh
d=%s
flags=""; k=0; v=0
for a in "$@"; do
	case "$a" in
	-k) k=1 ;;
	-v) v=1 ;;
	-n|-A|-S|-b|-E|-H|-P) flags="$flags $a" ;;
	*) echo "command: $a" > "$d/ran"; exit 2 ;;
	esac
done
if [ $k = 1 ] && [ $v = 0 ] && [ -n "$flags" ]; then
	echo "usage: sudo -h | -K | -k | -V" >&2
	exit 1
fi
[ $v = 1 ] && { echo prompt > "$d/prompted"; exit 1; }
/usr/bin/rm -f "$d/ticket"
exit 0
`

// TestDropTicketSudo1917: against sudo 1.9.17's argument rules the drop
// really removes the ticket and never prompts or runs a command. The old
// argv, `-n -k`, is rejected by that sudo (exit 1, ticket kept), so this
// fails without the fix.
func TestDropTicketSudo1917(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sudo"), []byte(fmt.Sprintf(sudo1917, dir)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ticket"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	if err := DropTicket(context.Background()); err != nil {
		t.Fatalf("DropTicket against sudo 1.9.17's rules: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "ticket")); err == nil {
		t.Error("the ticket survived the drop")
	}
	for _, f := range []string{"prompted", "ran"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			t.Errorf("the drop reached sudo's %s path", f)
		}
	}
}
