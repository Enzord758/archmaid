package aurbuild

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/steps"
)

// shims is a fixture directory with a git and a makepkg program on PATH that
// write fixture files and log what they were asked: no network, no real
// repository, no real makepkg. The fixture dir is baked into the scripts
// (Fetcher hands git a clean environment, so an env variable would not
// reach it).
//
//	repo/        what clone and fetch copy into the checkout
//	commit       what rev-parse prints
//	<sub>.rc     exit code of git <sub> (default 0); <sub>.err its stderr
//	<sub>.out    what git <sub> prints (ls-files, status, diff, cat-file)
//	rp_head      what rev-parse prints for HEAD, if different from commit
//	hang         git sleeps (a hung network)
//	make_outputs package files makepkg writes into $PKGDEST, one name per line
//	make_fail    makepkg fails (exit 4)
//	git.log, make.log  one line per call
type shims struct {
	dir  string
	fix  string
	root string // the AUR cache root of a Fetcher
}

const gitShim = `#!/bin/bash
FIX=%q
sub=""
args=("$@")
i=0
while [ $i -lt $# ]; do
  a="${args[$i]}"
  case "$a" in
    -c|-C) i=$((i+2)); continue;;
    -*) i=$((i+1)); continue;;
    *) sub="$a"; break;;
  esac
done
{
  echo "ARGS: $*"
  echo "PWD: $PWD"
  echo "ENV: $(env | grep -E '^(GIT_|FOO_|HOME=|LC_ALL=|PATH=|SUDO_ASKPASS)' | sort | tr '\n' ' ')"
} >> "$FIX/git.log"
if [ -e "$FIX/hang" ]; then exec sleep 30; fi
rc=0; [ -e "$FIX/$sub.rc" ] && rc=$(cat "$FIX/$sub.rc")
[ -e "$FIX/$sub.err" ] && cat "$FIX/$sub.err" >&2
[ "$rc" != 0 ] && exit "$rc"
case "$sub" in
  clone)
    target="${args[$(($#-1))]}"
    mkdir -p "$target/.git" && cp -a "$FIX/repo/." "$target/" ;;
  fetch)
    cp -a "$FIX/repo/." "$PWD/" ;;
  rev-parse)
    last="${args[$(($#-1))]}"
    if [ "$last" = "HEAD^{commit}" ] && [ -e "$FIX/rp_head" ]; then cat "$FIX/rp_head"; else cat "$FIX/commit"; fi ;;
  *)
    [ -e "$FIX/$sub.out" ] && cat "$FIX/$sub.out" ;;
esac
exit 0
`

const makepkgShim = `#!/bin/bash
FIX=%q
{
  echo "ARGS: $*"
  echo "PWD: $PWD"
  echo "PKGDEST=$PKGDEST SRCDEST=$SRCDEST BUILDDIR=$BUILDDIR"
} >> "$FIX/make.log"
if [ -e "$FIX/make_fail" ]; then echo "==> ERROR: A failure occurred in build()." >&2; exit 4; fi
mkdir -p "$PKGDEST"
while read -r f; do [ -n "$f" ] && echo "package $f" > "$PKGDEST/$f"; done < "$FIX/make_outputs"
echo "==> Finished making"
`

// newShims puts the shims first on PATH for the test and returns a Fetcher
// whose repo URL is "fixture://<base>".
func newShims(t *testing.T) (*shims, Fetcher) {
	t.Helper()
	s := &shims{dir: t.TempDir(), fix: t.TempDir(), root: filepath.Join(t.TempDir(), "aur")}
	if err := os.MkdirAll(filepath.Join(s.fix, "repo"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"git": gitShim, "makepkg": makepkgShim} {
		if err := os.WriteFile(filepath.Join(s.dir, name), []byte(fmt.Sprintf(body, s.fix)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", s.dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	s.put("commit", strings.Repeat("a", 40)+"\n")
	s.put("make_outputs", "")
	f := Fetcher{Root: s.root, URL: func(base string) string { return "fixture://" + base }, Allow: "fixture"}
	return s, f
}

func (s *shims) put(name, text string) {
	if err := os.WriteFile(filepath.Join(s.fix, name), []byte(text), 0o644); err != nil {
		panic(err)
	}
}

func (s *shims) repoFile(name, text string) {
	p := filepath.Join(s.fix, "repo", name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		panic(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		panic(err)
	}
}

func (s *shims) log(name string) string {
	b, _ := os.ReadFile(filepath.Join(s.fix, name))
	return string(b)
}

// gitCalls are the logged "ARGS:" lines of the git shim.
func (s *shims) gitCalls() []string {
	var out []string
	for _, l := range strings.Split(s.log("git.log"), "\n") {
		if strings.HasPrefix(l, "ARGS: ") {
			out = append(out, strings.TrimPrefix(l, "ARGS: "))
		}
	}
	return out
}

// runStep runs a step the way a front end would: an in-process step through
// its Run, a command with its Env added to this process's.
func runStep(t *testing.T, st steps.Step) steps.Result {
	t.Helper()
	if st.Run != nil {
		return st.Run(context.Background(), steps.Env{PriorOK: true})
	}
	cmd := exec.Command(st.Argv[0], st.Argv[1:]...)
	cmd.Env = append(os.Environ(), st.Env...)
	out, err := cmd.CombinedOutput()
	return steps.Result{Output: string(out), Err: err}
}
