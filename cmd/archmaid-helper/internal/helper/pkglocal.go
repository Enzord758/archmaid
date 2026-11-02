package helper

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// pkg-install-local: install AUR packages the user built and reviewed.
//
// This is the most sensitive verb: it runs `pacman -U` as root on files
// that came from a user-writable place, and a package is code that runs as
// root (scriptlets, hooks, any file anywhere). Local files carry no
// signature, so the one thing that ties what root installs to what the user
// confirmed is the hash H. The rules, in order (docs/design/packages-contract.md
// section 5, README.md):
//
//  1. Root reads each user file exactly once. The path is walked from the
//     passwd home of the invoking user with openat(O_NOFOLLOW|O_DIRECTORY),
//     component by component, every one checked on its open fd (never on its
//     name); the file is opened O_NOFOLLOW and checked on the fd; its bytes
//     are copied into a fresh root-only staging folder and hashed in the same
//     pass. Nothing is ever read from the user's path again.
//  2. The hash of the staged copies must equal H, else exit 3 (stale).
//  3. Only then is a staged file parsed (pacman -Qip, the parser that will
//     install it) and vetted: the name, version and architecture are the
//     file name's, nothing is protected, nothing conflicts or replaces.
//  4. pacman -U runs on the staged copies, under the lock, and the staging
//     folder is removed whatever happened.

// Limits.
const (
	// MaxLocalFiles caps the files of one call (an AUR run's packages).
	MaxLocalFiles = 64
	// MaxLocalFileSize caps one package file (the contract's 2 GiB).
	MaxLocalFileSize = 2 << 30
	// maxInfoFileList caps the file list relayed to the log.
	maxInfoFileList = 300

	pkgLocalMagic = "archmaid pkg local v1\n"

	// DefaultStagingRoot is where the per-run staging folders live: on
	// disk, because a package can be 2 GiB and /run is RAM. Root's, 0700.
	DefaultStagingRoot = "/var/cache/archmaid/staging"
	// DefaultPasswd is where the invoking user's home comes from.
	DefaultPasswd = "/etc/passwd"
)

// localSizeLimit is MaxLocalFileSize; a variable only so a test can lower it.
var localSizeLimit int64 = MaxLocalFileSize

// localPkgPattern is the name of a built package: pkgname-version-pkgrel-arch
// .pkg.tar.zst|xz, strictly. The pkgname is namePattern's, the version is
// printable ASCII without '-', '/' or a space (an epoch's ':' is in it).
// internal/aurbuild's pattern is the same shape (tied by a test).
var localPkgPattern = regexp.MustCompile(
	`^([A-Za-z0-9@_+][A-Za-z0-9@._+-]{0,127})-([\x21-\x2c\x2e\x30-\x7e]{1,64})-([0-9]{1,9})-([A-Za-z0-9_]{1,32})\.pkg\.tar\.(zst|xz)$`)

// LocalFile is one built package as the hash sees it.
type LocalFile struct {
	Name   string // the file name only
	Size   int64
	Sha256 string // lowercase hex of the content
}

// PkgLocalHash is the hash of an AUR install: "sha256:" and the hex of
// sha256 over "archmaid pkg local v1\n" and, sorted bytewise, one line
// "file <name> <size> <sha256hex>\n" per file. aurbuild.LocalHash and
// helperclient.PkgLocalHash give the same string for the same files (tie
// tests in cmd/archmaid-helper).
func PkgLocalHash(files []LocalFile) string {
	lines := make([]string, 0, len(files))
	for _, f := range files {
		lines = append(lines, fmt.Sprintf("file %s %d %s\n", f.Name, f.Size, f.Sha256))
	}
	slices.Sort(lines)
	h := sha256.New()
	io.WriteString(h, pkgLocalMagic)
	for _, l := range lines {
		io.WriteString(h, l)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// ---- argv ------------------------------------------------------------------

// installLocalArgv is the install itself: -U on the staged copies, in the
// order given (build order). No --needed (a rebuild of an installed version
// is the user's wish), never --overwrite, -dd, --noscriptlet or --asdeps.
func installLocalArgv(staged []string) []string {
	return pacmanArgv([]string{"-U", "--noconfirm"}, colourAlways, staged)
}

// localInfoPkgArgv / localListArgv read a staged file with pacman's own
// parser: the one that will install it, so there is no parser differential.
func localInfoPkgArgv(staged string) []string {
	return pacmanArgv([]string{"-Qip"}, colourNever, []string{staged})
}
func localListArgv(staged string) []string {
	return pacmanArgv([]string{"-Qlp"}, colourNever, []string{staged})
}
func architectureArgv() []string { return []string{PacmanConf, "Architecture"} }

// ---- input -----------------------------------------------------------------

// localArg is one argv file, parsed: "<base>/out/<name>".
type localArg struct {
	base, name string
	pkgname    string
	version    string // "ver-rel", as .PKGINFO's Version
	arch       string
}

// parseLocalArgs validates the files of the call: 1–MaxLocalFiles, each
// exactly "<base>/out/<name>" (base a pacman-style name, the middle "out",
// name a built package's file name), no file twice. Everything that could
// make a path leave that shape (absolute, "..", an empty or a dotted
// component, a NUL, a second slash) cannot match. It returns why it refuses.
func parseLocalArgs(args []string) ([]localArg, string) {
	if len(args) == 0 || len(args) > MaxLocalFiles {
		return nil, fmt.Sprintf("pkg-install-local takes 1–%d files, got %d", MaxLocalFiles, len(args))
	}
	var out []localArg
	seen := map[string]bool{}
	for _, a := range args {
		parts := strings.Split(a, "/")
		if len(parts) != 3 || parts[1] != "out" || !namePattern.MatchString(parts[0]) {
			return nil, fmt.Sprintf("invalid package file %q: want <base>/out/<name>.pkg.tar.zst|xz", Sanitize(a))
		}
		m := localPkgPattern.FindStringSubmatch(parts[2])
		if m == nil {
			return nil, fmt.Sprintf("invalid package file name %q", Sanitize(parts[2]))
		}
		if seen[parts[2]] {
			return nil, fmt.Sprintf("package file %q is named twice", parts[2])
		}
		seen[parts[2]] = true
		out = append(out, localArg{base: parts[0], name: parts[2], pkgname: m[1], version: m[2] + "-" + m[3], arch: m[4]})
	}
	return out, ""
}

// homeFromPasswd is the home directory of uid in a passwd file: the first
// entry with that uid (as getpwuid), its home field absolute and clean. The
// environment is never consulted.
func homeFromPasswd(path string, uid uint32) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 4<<20))
	if err != nil {
		return "", err
	}
	want := strconv.FormatUint(uint64(uid), 10)
	for l := range strings.SplitSeq(string(data), "\n") {
		fields := strings.Split(l, ":")
		if len(fields) < 6 || fields[2] != want {
			continue
		}
		home := fields[5]
		if !filepath.IsAbs(home) || filepath.Clean(home) != home || home == "/" || strings.ContainsRune(home, 0) {
			return "", fmt.Errorf("the home of uid %d in %s is %q, not a plain absolute folder", uid, path, home)
		}
		return home, nil
	}
	return "", fmt.Errorf("uid %d has no entry in %s", uid, path)
}

// ---- the path walk ---------------------------------------------------------

// aurCacheRel is the AUR build cache below the home (internal/aurbuild's).
var aurCacheRel = []string{".cache", "archmaid", "aur"}

// userDirMode / ancestor rules: every folder from the home down is owned by
// the user and writable by nobody else; the folders above the home (/,
// /home) are root's or the user's and writable by nobody else.
func (s *session) checkWalkStat(label string, st *unix.Stat_t, uid uint32, ancestor bool) error {
	if h := s.o.fstatHook; h != nil {
		h(label, st)
	}
	switch {
	case st.Mode&unix.S_IFMT != unix.S_IFDIR:
		return fmt.Errorf("%s is not a folder", label)
	case ancestor && st.Uid != 0 && st.Uid != uid:
		return fmt.Errorf("%s is owned by uid %d, not root or you", label, st.Uid)
	case !ancestor && st.Uid != uid:
		return fmt.Errorf("%s is owned by uid %d, not you (uid %d)", label, st.Uid, uid)
	case st.Mode&0o022 != 0:
		return fmt.Errorf("%s is writable by group or others", label)
	}
	return nil
}

// openStep is openat(parent, name) with O_NOFOLLOW; a symlink or a
// non-folder gives a refusal with a message, a missing entry gives stale.
func openStep(parent int, name, label string, flags int) (int, *pkgFail) {
	fd, err := unix.Openat(parent, name, flags|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NOCTTY, 0)
	switch {
	case err == nil:
		return fd, nil
	case errors.Is(err, unix.ENOENT):
		return -1, &pkgFail{code: ExitStale, reason: ReasonStale, msg: fmt.Sprintf("%s is gone since the build: build again", label)}
	case errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOTDIR):
		return -1, unsafeFile("%s is a symbolic link or not what it should be", label)
	}
	return -1, unsafeFile("cannot open %s: %v", label, err)
}

func unsafeFile(format string, a ...any) *pkgFail {
	return &pkgFail{code: ExitRefused, reason: ReasonUnsafeFile, msg: fmt.Sprintf(format, a...)}
}

// openUserFile walks home/.cache/archmaid/aur/<base>/out and opens the file
// in it. Every folder is opened relative to the previous open one with
// O_NOFOLLOW|O_DIRECTORY and checked on its own fd; the file is opened
// O_NOFOLLOW|O_NONBLOCK (a FIFO must not hang the helper) and checked on its
// fd: a regular file of the user's, writable by nobody else, 1 byte to
// MaxLocalFileSize. The returned file is the only handle root ever uses.
func (s *session) openUserFile(uid uint32, home string, a localArg) (*os.File, *pkgFail) {
	start, comps := "/", strings.Split(strings.Trim(home, "/"), "/")
	if anchor := s.o.walkAnchor; anchor != "" { // test seam: begin below a trusted folder
		rest, ok := strings.CutPrefix(home, anchor+"/")
		if !ok {
			return nil, unsafeFile("the home %s is not below %s", home, anchor)
		}
		start, comps = anchor, strings.Split(rest, "/")
	}
	cur, err := unix.Open(start, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, unsafeFile("cannot open %s: %v", start, err)
	}
	nHome := len(comps)
	comps = slices.Concat(comps, aurCacheRel, []string{a.base, "out"})
	label := ""
	if start != "/" {
		label = start
	}
	for i, c := range comps {
		label += "/" + c
		next, f := openStep(cur, c, label, unix.O_RDONLY|unix.O_DIRECTORY)
		unix.Close(cur)
		if f != nil {
			return nil, f
		}
		cur = next
		var st unix.Stat_t
		if err := unix.Fstat(cur, &st); err != nil {
			unix.Close(cur)
			return nil, unsafeFile("cannot check %s: %v", label, err)
		}
		if err := s.checkWalkStat(label, &st, uid, i < nHome-1); err != nil {
			unix.Close(cur)
			return nil, unsafeFile("%v", err)
		}
	}
	defer unix.Close(cur)
	label += "/" + a.name
	fd, f := openStep(cur, a.name, label, unix.O_RDONLY|unix.O_NONBLOCK)
	if f != nil {
		return nil, f
	}
	file := os.NewFile(uintptr(fd), label)
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		file.Close()
		return nil, unsafeFile("cannot check %s: %v", label, err)
	}
	if h := s.o.fstatHook; h != nil {
		h(label, &st)
	}
	switch {
	case st.Mode&unix.S_IFMT != unix.S_IFREG:
		file.Close()
		return nil, unsafeFile("%s is not a regular file", label)
	case st.Uid != uid:
		file.Close()
		return nil, unsafeFile("%s is owned by uid %d, not you (uid %d)", label, st.Uid, uid)
	case st.Mode&0o022 != 0:
		file.Close()
		return nil, unsafeFile("%s is writable by group or others", label)
	case st.Size <= 0 || st.Size > localSizeLimit:
		file.Close()
		return nil, unsafeFile("%s is %d bytes: a package is 1 byte to %d", label, st.Size, localSizeLimit)
	}
	return file, nil
}

// ---- staging ---------------------------------------------------------------

// staging is the root-only folder one run copies into.
type staging struct {
	dir string // absolute
}

// checkRootDir: path is a real folder (not a symlink) owned by this
// process's euid (root in production) and writable by nobody else.
func checkRootDir(path string) error {
	var st unix.Stat_t
	if err := unix.Lstat(path, &st); err != nil {
		return err
	}
	switch {
	case st.Mode&unix.S_IFMT != unix.S_IFDIR:
		return fmt.Errorf("%s is not a folder (or is a symlink)", path)
	case st.Uid != uint32(unix.Geteuid()):
		return fmt.Errorf("%s is owned by uid %d", path, st.Uid)
	case st.Mode&0o022 != 0:
		return fmt.Errorf("%s is writable by group or others", path)
	}
	return nil
}

// newStaging prepares the staging root (created 0700 when missing; its parent
// 0755), clears what a killed run left (the helper lock is held, so no run
// is using any of it) and makes this run's folder: 0700, empty, fresh.
func (s *session) newStaging() (*staging, error) {
	root := s.o.StagingRoot
	if root == "" {
		root = DefaultStagingRoot
	}
	parent := filepath.Dir(root)
	for _, d := range []struct {
		path string
		mode os.FileMode
	}{{parent, 0o755}, {root, 0o700}} {
		if err := os.Mkdir(d.path, d.mode); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("cannot create %s: %v", d.path, err)
		}
		if err := checkRootDir(d.path); err != nil {
			return nil, err
		}
	}
	if ents, err := os.ReadDir(root); err == nil {
		for _, e := range ents {
			if strings.HasPrefix(e.Name(), "run-") {
				os.RemoveAll(filepath.Join(root, e.Name())) // our own, root-only: a killed run's
			}
		}
	}
	dir, err := os.MkdirTemp(root, "run-")
	if err != nil {
		return nil, fmt.Errorf("cannot create a staging folder: %v", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	return &staging{dir: dir}, nil
}

func (st *staging) remove() { os.RemoveAll(st.dir) }

func (st *staging) path(name string) string { return filepath.Join(st.dir, name) }

// copyIn copies src into the staging folder as name while hashing the bytes
// it copies, at most MaxLocalFileSize of them. The size and hash it returns
// are of what was copied, not of what stat said.
func (st *staging) copyIn(name string, src *os.File) (LocalFile, *pkgFail) {
	dst, err := os.OpenFile(st.path(name), os.O_WRONLY|os.O_CREATE|os.O_EXCL|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return LocalFile{}, failed("cannot stage %s: %v", name, err)
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(dst, h), io.LimitReader(src, localSizeLimit+1))
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	switch {
	case err != nil:
		return LocalFile{}, failed("cannot copy %s: %v", name, err)
	case n > localSizeLimit:
		return LocalFile{}, unsafeFile("%s grew past %d bytes while it was copied", name, localSizeLimit)
	case n == 0:
		return LocalFile{}, unsafeFile("%s is empty", name)
	}
	return LocalFile{Name: name, Size: n, Sha256: hex.EncodeToString(h.Sum(nil))}, nil
}

// rehash reads a staged copy back (the root-only one) and returns what is
// really on disk: it must be what was hashed on the way in.
func (st *staging) rehash(name string) (LocalFile, error) {
	f, err := os.OpenFile(st.path(name), os.O_RDONLY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return LocalFile{}, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return LocalFile{}, err
	}
	return LocalFile{Name: name, Size: n, Sha256: hex.EncodeToString(h.Sum(nil))}, nil
}

// ---- .PKGINFO, through pacman ----------------------------------------------

// localInfo is what `pacman -Qip` says about a staged file.
type localInfo struct {
	name, version, arch string
	installScript       bool
	provides            []string
}

// parseLocalInfo reads the fields of one `pacman -Qip` block (LC_ALL=C).
// The first occurrence of a field wins; pacman prints Name, Version and
// Architecture before any free text, and a value cannot hold a newline, so
// no description can make a line look like a field.
func parseLocalInfo(out string) (localInfo, syncEntry, error) {
	var in localInfo
	fields := map[string]string{}
	cur := ""
	for l := range strings.SplitSeq(out, "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if l[0] == ' ' || l[0] == '\t' {
			if cur != "" {
				fields[cur] += " " + strings.TrimSpace(l)
			}
			continue
		}
		k, v, ok := strings.Cut(l, ":")
		if !ok {
			cur = ""
			continue
		}
		cur = strings.TrimSpace(k)
		if _, dup := fields[cur]; dup {
			cur = ""
			continue
		}
		fields[cur] = strings.TrimSpace(v)
	}
	in.name, in.version, in.arch = fields["Name"], fields["Version"], fields["Architecture"]
	if in.name == "" || in.version == "" || in.arch == "" {
		return in, syncEntry{}, errors.New("pacman -Qip printed no name, version or architecture")
	}
	in.installScript = strings.EqualFold(fields["Install Script"], "yes")
	e := syncEntry{name: in.name}
	rels := func(field, kind string) {
		for _, f := range strings.Fields(fields[field]) {
			if i := strings.IndexAny(f, "<>="); i >= 0 {
				f = f[:i]
			}
			if f != "" && f != "None" {
				switch kind {
				case "":
					in.provides = append(in.provides, f)
				default:
					e.relations = append(e.relations, syncRel{kind, f})
				}
			}
		}
	}
	rels("Provides", "")
	rels("Replaces", "replaces")
	rels("Conflicts With", "conflicts with")
	return in, e, nil
}

// allowedArches: the architectures pacman accepts here ("any" always), from
// pacman-conf ("auto" meaning this machine's).
func (s *session) allowedArches() (map[string]bool, *pkgFail) {
	out, errOut, exit, err := s.o.Runner.Query(architectureArgv())
	if err != nil {
		return nil, failed("pacman-conf Architecture: %v", err)
	}
	if exit != 0 {
		return nil, failed("pacman-conf Architecture exited %d: %s", exit, firstLine(errOut))
	}
	set := map[string]bool{"any": true}
	for _, a := range strings.Fields(out) {
		if a != "auto" {
			set[a] = true
			continue
		}
		m, err := s.machine()
		if err != nil {
			return nil, failed("uname: %v", err)
		}
		set[m] = true
	}
	return set, nil
}

func (s *session) machine() (string, error) {
	if s.o.uname != nil {
		return s.o.uname()
	}
	var u unix.Utsname
	if err := unix.Uname(&u); err != nil {
		return "", err
	}
	return unix.ByteSliceToString(u.Machine[:]), nil
}

// vetLocal reads every staged file with pacman, logs what it says and
// refuses anything that is not the package its file name claims, or that is
// protected, or would replace or conflict with an installed package.
func (s *session) vetLocal(st *staging, args []localArg) *pkgFail {
	arches, f := s.allowedArches()
	if f != nil {
		return f
	}
	installed, f := s.installedVersions()
	if f != nil {
		return f
	}
	set, err := s.protectedSet()
	if err != nil {
		return failed("%s", err.Error())
	}
	seen := map[string]bool{}
	for _, a := range args {
		bad := func(format string, v ...any) *pkgFail {
			return &pkgFail{code: ExitRefused, reason: ReasonBadPackage, names: []string{a.pkgname}, msg: fmt.Sprintf(format, v...)}
		}
		out, errOut, exit, err := s.o.Runner.Query(localInfoPkgArgv(st.path(a.name)))
		if err != nil {
			return failed("pacman -Qip: %v", err)
		}
		if exit != 0 {
			return bad("pacman cannot read %s: %s", a.name, firstLine(errOut))
		}
		s.em.Line("out", Sanitize("== "+a.name))
		for _, l := range nonEmptyLines(out) {
			s.em.Line("out", Sanitize(l))
		}
		info, rel, err := parseLocalInfo(out)
		if err != nil {
			return bad("%s: %v", a.name, err)
		}
		switch {
		case info.name != a.pkgname:
			return bad("%s holds package %q, not %q", a.name, info.name, a.pkgname)
		case info.version != a.version:
			return bad("%s holds version %q, not %q", a.name, info.version, a.version)
		case info.arch != a.arch:
			return bad("%s holds architecture %q, not %q", a.name, info.arch, a.arch)
		case !arches[info.arch]:
			return bad("%s is built for %q, which this pacman does not accept", a.name, info.arch)
		case seen[info.name]:
			return bad("package %q is in the list twice", info.name)
		}
		seen[info.name] = true
		if info.installScript {
			s.em.Line("out", "NOTE: "+Sanitize(info.name)+" has an install script (.INSTALL): it runs as root")
		} else {
			s.em.Line("out", Sanitize(info.name)+" has no install script (.INSTALL)")
		}
		if lout, _, lexit, lerr := s.o.Runner.Query(localListArgv(st.path(a.name))); lerr == nil && lexit == 0 {
			files := nonEmptyLines(lout)
			for i, l := range files {
				if i == maxInfoFileList {
					s.em.Line("out", fmt.Sprintf("… and %d more files", len(files)-i))
					break
				}
				s.em.Line("out", Sanitize(l))
			}
		}
		// A protected package is never installed here: not by its name, and
		// not by claiming a protected name through provides.
		for _, n := range append([]string{info.name}, info.provides...) {
			if r := protectedReason(set, n); r != "" {
				return &pkgFail{code: ExitRefused, reason: ReasonProtected, names: []string{info.name},
					msg: fmt.Sprintf("%s would take the place of %s (%s), which is protected and never changed here", info.name, n, r)}
			}
		}
		for _, c := range rel.relations {
			if c.name == info.name {
				continue
			}
			if _, ok := installed[c.name]; ok {
				return &pkgFail{code: ExitRefused, reason: ReasonConflict, names: []string{info.name, c.name},
					msg: fmt.Sprintf("%s %s the installed package %s: remove %s first, then install %s.", info.name, c.kind, c.name, c.name, info.name)}
			}
		}
	}
	return nil
}

// ---- the verb --------------------------------------------------------------

func runPkgInstallLocal(s *session, args []string) int {
	if len(args) < 2 || !hashPattern.MatchString(args[0]) {
		return s.badRequest("pkg-install-local takes the hash (sha256:<64 hex>) of the built files, then 1–%d files <base>/out/<name>.pkg.tar.zst", MaxLocalFiles)
	}
	want := args[0]
	files, why := parseLocalArgs(args[1:])
	if why != "" {
		return s.badRequest("%s", why)
	}
	uid, home, err := s.caller()
	if err != nil {
		s.reason = ReasonUnsafeFile
		return s.finish(ExitRefused, err.Error())
	}

	release, code := s.lock()
	if code != ExitOK {
		return code
	}
	defer release()

	busy := func(err error) int {
		s.skip("pkg-install-local", "pacman is running")
		s.reason = ReasonBusy
		return s.finish(ExitBusy, err.Error()+"; nothing was changed")
	}
	if err := pacmanIdle(s.lockPath()); err != nil {
		return busy(err)
	}
	if f := s.checkNoUpdates(); f != nil {
		return s.finishFail(f)
	}

	st, err := s.newStaging()
	if err != nil {
		return s.finish(ExitFailed, err.Error())
	}
	defer st.remove() // success, failure and panic; a signal is caught and dropped (main)

	// Step 1: one read of each user file, into staging, hashed on the way.
	s.progress(1, 3, fmt.Sprintf("Copying %d built package(s) to a safe place", len(files)))
	var copied []LocalFile
	for _, a := range files {
		f, fail := s.openUserFile(uid, home, a)
		if fail != nil {
			fail.names = []string{a.pkgname}
			return s.finishFail(fail)
		}
		if h := s.o.afterOpen; h != nil {
			h(filepath.Join(home, filepath.Join(aurCacheRel...), a.base, "out", a.name))
		}
		lf, fail := st.copyIn(a.name, f)
		f.Close()
		if fail != nil {
			fail.names = []string{a.pkgname}
			return s.finishFail(fail)
		}
		copied = append(copied, lf)
	}
	if h := s.o.afterStage; h != nil {
		h(st.dir)
	}
	// Step 2: the staged copies, read back, are what was hashed and what
	// the user confirmed.
	var staged []LocalFile
	for i, a := range files {
		lf, err := st.rehash(a.name)
		if err != nil {
			return s.finish(ExitFailed, fmt.Sprintf("cannot read back the staged %s: %v", a.name, err))
		}
		if lf != copied[i] {
			return s.finish(ExitFailed, fmt.Sprintf("the staged copy of %s is not what was copied", a.name))
		}
		staged = append(staged, lf)
	}
	if got := PkgLocalHash(staged); got != want {
		s.progress(2, 3, "Skipped: the built files are not the ones that were confirmed")
		s.skip("pkg-install-local", "the built files changed since they were confirmed")
		s.reason = ReasonStale
		return s.finish(ExitStale, "the built packages are not the ones you confirmed (hash differs); nothing changed: build again")
	}

	// Step 3: pacman's own reading of the staged files, then the vetting.
	s.progress(2, 3, "Checking the built package(s)")
	if f := s.vetLocal(st, files); f != nil {
		return s.finishFail(f)
	}

	if err := pacmanIdle(s.lockPath()); err != nil {
		return busy(err)
	}
	paths := make([]string, len(files))
	for i, a := range files {
		paths[i] = st.path(a.name)
	}
	if ok, msg := s.step(3, 3, fmt.Sprintf("Installing %d built package(s)", len(files)), installLocalArgv(paths)); !ok {
		return s.finish(ExitFailed, msg)
	}
	return s.finish(ExitOK, "")
}

// caller is the invoking user's uid and passwd home. Never root: a root
// caller could stage any file the walk reaches and has no need of this verb.
func (s *session) caller() (uint32, string, error) {
	if !uidPattern.MatchString(s.o.CallerUID) {
		return 0, "", errors.New("no PKEXEC_UID: not started through pkexec")
	}
	n, err := strconv.ParseUint(s.o.CallerUID, 10, 32)
	if err != nil || n == 0 {
		return 0, "", fmt.Errorf("PKEXEC_UID %q is not a user's uid", s.o.CallerUID)
	}
	path := s.o.PasswdPath
	if path == "" {
		path = DefaultPasswd
	}
	home, err := homeFromPasswd(path, uint32(n))
	if err != nil {
		return 0, "", err
	}
	return uint32(n), home, nil
}
