package privilege

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"

	"github.com/Enzord758/archmaid/internal/sysbin"
)

// The TUI's sudo wrapper (docs/HANDOVER.md, "Nothing may prompt on the raw
// tty"). topgrade, yay and makepkg start sudo by name, and in the TUI their
// sudo has the TUI's terminal: without a reusable ticket (timestamp_timeout=0,
// a lapsed or dropped one) it would prompt there, under the full-screen UI,
// outside archmaid. So the TUI's askpass Server also writes a `sudo` of its
// own (ServerOptions.SudoWrapper) into a private directory, and the steps
// that get the askpass get that directory first on their PATH (Sudo.SudoDir):
// a sudo started by name is then this script, which execs the real sudo by
// absolute path (sysbin.Sudo, fixed when it is written) with -A, so a
// password goes through archmaid's popup (and Request.Answer's su check),
// never the terminal. It execs, so the real sudo's parent stays topgrade or
// yay, and Request.Verify sees the same process tree as without it.
//
// -A is left out when the caller already said how sudo gets its password
// (-A/--askpass, -S/--stdin, -n/--non-interactive, also in a cluster such as
// -nv) and for the modes that never ask for one and take no other option
// (-K/--remove-timestamp, -V/--version, -h/--help). It reads sudo's own
// options only: they end at the first word that isn't one (the command),
// at --, and at an option it doesn't know (whatever follows is then left
// to sudo, and -A is added: it can only keep sudo off the terminal). An
// option's value (-u -n: the user "-n"; -p, -C, --prompt …) is skipped,
// never read as a flag. The caller's arguments go to sudo exactly as given.
//
// The GUI doesn't use it: its children have no terminal, and sudo asks
// through SUDO_ASKPASS there by itself.

// sudoWrapperDir is the wrapper's directory inside the Server's private
// directory: it holds only `sudo`, so nothing else of the Server's (the
// socket, askpass.sh) is on a step's PATH.
const sudoWrapperDir = "bin"

// sudoWrapperScript is the wrapper: it runs sudo (an absolute path) with
// -A and SUDO_ASKPASS=askpass unless the arguments rule -A out (see above).
// Its variables are prefixed so that one the caller's environment exports
// under a short name is left alone.
func sudoWrapperScript(sudo, askpass string) string {
	return `#!/bin/sh
# archmaid's sudo for the TUI's steps (internal/privilege/sudowrap.go): the
# real sudo by absolute path, with -A, so it asks through archmaid's askpass,
# never on the terminal. Written for one archmaid session; removed with it.
archmaid_ask=-A
archmaid_val=
for archmaid_arg do
	if [ -n "$archmaid_val" ]; then archmaid_val=; continue; fi
	case $archmaid_arg in
	--) break ;;
	--askpass|--stdin|--non-interactive|--remove-timestamp|--version|--help) archmaid_ask=; break ;;
	--auth-type|--close-from|--login-class|--chdir|--group|--host|--prompt|--chroot|--role|--type|--command-timeout|--other-user|--user) archmaid_val=1 ;;
	--*=*|--background|--bell|--preserve-env|--edit|--set-home|--login|--reset-timestamp|--list|--no-update|--preserve-groups|--shell|--validate) ;;
	--*) break ;;
	-?*)
		archmaid_rest=${archmaid_arg#-}
		while [ -n "$archmaid_rest" ]; do
			archmaid_c=${archmaid_rest%"${archmaid_rest#?}"}
			archmaid_rest=${archmaid_rest#?}
			case $archmaid_c in
			A|S|n|K|V) archmaid_ask=; break 2 ;;
			h) if [ -z "$archmaid_rest" ]; then archmaid_ask=; break 2; fi; archmaid_rest= ;;
			a|C|c|D|g|p|R|r|T|t|U|u) if [ -z "$archmaid_rest" ]; then archmaid_val=1; fi; archmaid_rest= ;;
			B|b|E|e|H|i|k|l|N|P|s|v) ;;
			*) break 2 ;;
			esac
		done ;;
	*) break ;;
	esac
done
if [ -n "$archmaid_ask" ]; then
	SUDO_ASKPASS=` + shellQuote(askpass) + `
	export SUDO_ASKPASS
	exec ` + shellQuote(sudo) + ` -A "$@"
fi
exec ` + shellQuote(sudo) + ` "$@"
`
}

// writeSudoWrapper writes the wrapper into dir/bin (0700, the file 0700),
// running sysbin.Sudo as it is now, records it as this process's own
// (ownSudo) and returns its directory and path. It writes nothing and
// returns "" when sysbin.Sudo isn't an absolute path: that is only under
// testenv (bare names, so that PATH's traps decide), and a wrapper that
// looked sudo up on PATH would find itself.
func writeSudoWrapper(dir, askpass string) (bin, path string, err error) {
	sudo := sysbin.Sudo
	if !filepath.IsAbs(sudo) {
		return "", "", nil
	}
	bin = filepath.Join(dir, sudoWrapperDir)
	if err := os.Mkdir(bin, 0o700); err != nil {
		return "", "", err
	}
	// Its name is what the steps' programs look up on PATH; archmaid
	// writes this file and never runs it.
	path = filepath.Join(bin, "sudo")
	body := []byte(sudoWrapperScript(sudo, askpass))
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		return "", "", err
	}
	if _, err := f.Write(body); err != nil {
		f.Close()
		return "", "", err
	}
	if err := f.Close(); err != nil {
		return "", "", err
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return "", "", err
	}
	ownSudo.add(path, fi, body)
	return bin, path, nil
}

// ownSudo is every sudo wrapper this process wrote and hasn't removed:
// the PATH guard (PathShadows) takes the one first on a step's PATH for
// what it is, archmaid's own way to the real sudo, not a stand-in.
var ownSudo ownFiles

type ownFile struct {
	fi   os.FileInfo
	body []byte
}

type ownFiles struct {
	mu sync.Mutex
	m  map[string]ownFile
}

func (o *ownFiles) add(path string, fi os.FileInfo, body []byte) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.m == nil {
		o.m = map[string]ownFile{}
	}
	o.m[path] = ownFile{fi: fi, body: body}
}

func (o *ownFiles) remove(path string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.m, path)
}

// is reports whether the file at path (fi, not followed) is a wrapper this
// process wrote, as written: the same path, the same file (device and
// inode), still a regular 0700 file, with the same contents. A file put
// in its place, a copy elsewhere, a symlink to it or a rewritten one is not.
func (o *ownFiles) is(path string, fi os.FileInfo) bool {
	o.mu.Lock()
	own, ok := o.m[filepath.Clean(path)]
	o.mu.Unlock()
	if !ok || !fi.Mode().IsRegular() || fi.Mode().Perm() != 0o700 || !os.SameFile(fi, own.fi) {
		return false
	}
	b, err := os.ReadFile(path)
	return err == nil && bytes.Equal(b, own.body)
}
