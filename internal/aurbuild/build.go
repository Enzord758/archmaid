package aurbuild

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"

	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/sanitize"
	"github.com/Enzord758/archmaid/internal/steps"
)

// A pkgbase with several pkgnames is built once; makepkg makes every
// package of it (a split package cannot be built in part without sourcing
// the PKGBUILD, which waits for the user's accept). Only the pkgnames the
// plan names (the ones asked for and the ones a dependency needs) are
// handed to the root install; the others stay in the out folder and are
// never installed. A -debug package is one of the others.

// MaxPackageSize is the helper's cap on one built file.
const MaxPackageSize = 2 << 30

// pkgFile is the name a built package must have: the helper's pattern
// (docs/design/packages-contract.md, section 5), with the pkgname captured.
var pkgFile = regexp.MustCompile(`^([A-Za-z0-9@._+-]+)-([^-/]+)-([0-9]+)-([A-Za-z0-9_]+)\.pkg\.tar\.(zst|xz)$`)

// BuiltFile is one package file makepkg made.
type BuiltFile struct {
	Pkgname string
	Name    string // the file name
	Rel     string // "<base>/out/<name>", relative to the AUR cache root
	Size    int64
	Sha256  string // hex
}

// outDir is where base's packages are made (PKGDEST).
func (f Fetcher) outDir(base string) string { return filepath.Join(f.Root, base, "out") }

// LocalHash is the hash the root verb recomputes over the staged copies:
// "sha256:" and the hex of sha256 over "archmaid pkg local v1\n" followed by
// one line "file <name> <size> <sha256hex>\n" per file, sorted by name
// (bytewise), name being the file name without its folder. helperclient's
// PkgLocalHash must give the same string for the same files.
func LocalHash(files []BuiltFile) string {
	lines := make([]string, 0, len(files))
	for _, f := range files {
		lines = append(lines, fmt.Sprintf("file %s %d %s\n", f.Name, f.Size, f.Sha256))
	}
	sort.Strings(lines)
	h := sha256.New()
	io.WriteString(h, "archmaid pkg local v1\n")
	for _, l := range lines {
		io.WriteString(h, l)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// prepare pins the checkout to the reviewed commit and makes the build
// folders fresh: out (PKGDEST) is emptied, so only what this run builds can
// be installed; src and build are created.
func (f Fetcher) prepare(ctx context.Context, p steps.AURPlan) error {
	dir, err := f.Dir(p.Base)
	if err != nil {
		return err
	}
	co := Checkout{Base: p.Base, Dir: dir, Commit: p.Commit}
	if err := f.Pin(ctx, co); err != nil {
		return err
	}
	for _, r := range reserved {
		if err := f.checkPath(filepath.Join(dir, r)); err != nil {
			return err
		}
	}
	if err := os.RemoveAll(f.outDir(p.Base)); err != nil {
		return err
	}
	for _, r := range reserved {
		if err := os.MkdirAll(filepath.Join(dir, r), 0o700); err != nil {
			return err
		}
	}
	return nil
}

// BuildEnv is the build step's added environment (steps.Step.Env): where
// makepkg puts packages, sources and its work. makepkg.conf is otherwise
// honoured; an environment value wins over the conf's.
func (f Fetcher) BuildEnv(base string) []string {
	dir := filepath.Join(f.Root, base)
	return []string{"PKGDEST=" + filepath.Join(dir, "out"), "SRCDEST=" + filepath.Join(dir, "src"), "BUILDDIR=" + filepath.Join(dir, "build")}
}

// MakepkgArgv is the build command. env -C runs it in the base's folder (this
// makepkg has no --dir). There is no -s, -i, -r, -d, --skipinteg,
// --skippgpcheck or --skipchecksums, and it never runs under sudo: makepkg
// installs nothing and checks everything; missing build dependencies fail
// the build with makepkg's own message (the repo ones were installed first).
func (f Fetcher) MakepkgArgv(base string) []string {
	return []string{"env", "-C", filepath.Join(f.Root, base), "makepkg", "--clean", "--cleanbuild", "--noprogressbar", "--noconfirm"}
}

// Queue is the steps of a confirmed AUR run (docs/design/packages-contract.md,
// section 5), in order:
//
//	deps            the repo dependencies, root (nil if none): pkg-install --asdeps
//	aur-fetch:<b>   pin <b> to the reviewed commit, fresh build folders (in process)
//	aur-build:<b>   makepkg as the user (a command: its output streams)
//	  ... for each base, dependencies first
//	aur-collect     list and hash the built files; inserts the root install
//	pkg-install-local  inserted right after aur-collect: all files, one call
//	aur-remember    records the reviewed commits (runs only after a good install)
//
// plans must be in build order. A failing step stops the queue, so nothing
// is installed after a failed build and nothing is remembered.
func (f Fetcher) Queue(plans []steps.AURPlan, deps *steps.Step, reviewed *Reviewed) []steps.Step {
	var q []steps.Step
	if deps != nil {
		q = append(q, *deps)
	}
	for _, p := range plans {
		p := p
		q = append(q,
			steps.Step{
				ID: steps.IDAURFetchPrefix + p.Base, Title: "Checking out " + p.Base + "…", LogLabel: "AUR checkout pinned",
				Run: func(ctx context.Context, env steps.Env) steps.Result {
					if env.DryRun {
						return steps.Result{Output: "[dry-run] would pin " + p.Base + " to " + p.Commit}
					}
					if err := f.prepare(ctx, p); err != nil {
						return steps.Result{Err: err}
					}
					return steps.Result{Output: "pinned " + p.Base + " at " + p.Commit}
				},
			},
			steps.Step{
				ID: steps.IDAURBuildPrefix + p.Base, Title: "Building " + p.Base + "…", LogLabel: "AUR package built",
				Argv: f.MakepkgArgv(p.Base), Env: f.BuildEnv(p.Base),
				AlwaysLog: true, Category: logstore.Cleanup,
			})
	}
	q = append(q,
		steps.Step{
			ID: "aur-collect", Title: "Checking the built packages…", LogLabel: "AUR packages collected",
			Run: func(ctx context.Context, env steps.Env) steps.Result {
				if env.DryRun {
					return steps.Result{Output: "[dry-run] would collect the built packages"}
				}
				built, err := f.Collect(plans)
				if err != nil {
					return steps.Result{Err: err}
				}
				st := InstallStep(built)
				return steps.Result{Output: describe(built), InsertNext: &st}
			},
		},
		steps.Step{
			ID: "aur-remember", Title: "Remembering what you reviewed…", LogLabel: "AUR review remembered",
			Run: func(ctx context.Context, env steps.Env) steps.Result {
				if env.DryRun || reviewed == nil {
					return steps.Result{}
				}
				marks := map[string]string{}
				for _, p := range plans {
					marks[p.Base] = p.Commit
				}
				return steps.Result{Err: reviewed.Mark(marks)}
			},
		})
	return q
}

func describe(built []BuiltFile) string {
	var b strings.Builder
	for _, f := range built {
		fmt.Fprintf(&b, "%s  %d bytes  sha256 %s\n", f.Name, f.Size, f.Sha256)
	}
	return b.String()
}

// InstallStep is the root step that installs built (in build order): the
// helper verb pkg-install-local with the files and their LocalHash. The
// consent is the sheet's; the helper verb is Dev 3's.
func InstallStep(built []BuiltFile) steps.Step {
	local := steps.PkgLocal{Hash: LocalHash(built)}
	var names []string
	for _, f := range built {
		local.Files = append(local.Files, f.Rel)
		names = append(names, f.Pkgname)
	}
	return steps.Step{
		ID: steps.IDPkgInstallLocal, Verb: steps.PkgVerbInstallLocal,
		Title: "Installing AUR packages…", LogLabel: "AUR packages installed",
		Items: names, PkgLocal: &local, NeedsPriv: true, AlwaysLog: true, Category: logstore.Cleanup,
	}
}

// Collect finds what the builds made: for each plan, in order, the files in
// its out folder whose pkgname the plan installs. A plan name with no file,
// or with two, fails (nothing is installed). Files are lstat'ed and hashed
// here, and they are what the root install must see unchanged.
func (f Fetcher) Collect(plans []steps.AURPlan) ([]BuiltFile, error) {
	var all []BuiltFile
	for _, p := range plans {
		out := f.outDir(p.Base)
		if err := f.checkPath(out); err != nil {
			return nil, err
		}
		entries, err := os.ReadDir(out)
		if err != nil {
			return nil, fmt.Errorf("%s made no packages: %v", p.Base, sanitize.OneLine(err.Error()))
		}
		want := map[string]bool{}
		for _, n := range p.Names {
			want[n] = true
		}
		got := map[string]bool{}
		var other []string
		for _, e := range entries {
			m := pkgFile.FindStringSubmatch(e.Name())
			if m == nil {
				if strings.Contains(e.Name(), ".pkg.tar") {
					other = append(other, e.Name())
				}
				continue
			}
			if !want[m[1]] {
				continue
			}
			if got[m[1]] {
				return nil, fmt.Errorf("%s made two files for %s", p.Base, m[1])
			}
			bf, err := hashFile(filepath.Join(out, e.Name()))
			if err != nil {
				return nil, fmt.Errorf("%s: %w", sanitize.Field(e.Name(), 100), err)
			}
			bf.Pkgname, bf.Name, bf.Rel = m[1], e.Name(), p.Base+"/out/"+e.Name()
			got[m[1]] = true
			all = append(all, bf)
		}
		for _, n := range p.Names {
			if !got[n] {
				msg := fmt.Sprintf("%s made no package file for %s", p.Base, n)
				if len(other) > 0 {
					msg += fmt.Sprintf(" (found %s: archmaid installs .pkg.tar.zst and .pkg.tar.xz; check PKGEXT in makepkg.conf)", sanitize.Field(other[0], 100))
				}
				return nil, errors.New(msg)
			}
		}
	}
	return all, nil
}

// hashFile lstat-checks and hashes one built file.
func hashFile(path string) (BuiltFile, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return BuiltFile{}, err
	}
	if !fi.Mode().IsRegular() {
		return BuiltFile{}, errors.New("not a regular file")
	}
	if fi.Size() > MaxPackageSize {
		return BuiltFile{}, errors.New("over 2 GiB")
	}
	fd, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return BuiltFile{}, err
	}
	defer fd.Close()
	h := sha256.New()
	n, err := io.Copy(h, fd)
	if err != nil {
		return BuiltFile{}, err
	}
	return BuiltFile{Size: n, Sha256: hex.EncodeToString(h.Sum(nil))}, nil
}
