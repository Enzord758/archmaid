package helperclient

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"regexp"
	"slices"
	"sort"

	"github.com/Enzord758/archmaid/internal/steps"
)

// The AUR install's root verb (docs/design/packages-contract.md, section 5):
//
//	pkg-install-local <H> <file>...
//
// H is PkgLocalHash over the built files and each file is
// "<base>/out/<name>.pkg.tar.zst" (or .xz) relative to the invoking user's
// ~/.cache/archmaid/aur, in build order. The helper (Dev 3's) takes the home
// from the passwd entry of PKEXEC_UID, opens each file without following
// symlinks, copies it to its root staging folder while hashing, and refuses
// (exit 3, stale) when the hash differs. Like the Packages verbs it is not a
// Verb* constant until the helper has it.
const PkgVerbInstallLocal = steps.PkgVerbInstallLocal

// MaxLocalFiles is the most files one pkg-install-local call takes.
const MaxLocalFiles = 64

// localHashHeader is the first line of what PkgLocalHash hashes.
const localHashHeader = "archmaid pkg local v1\n"

var (
	// localHashPattern is H as the client accepts it.
	localHashPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	// localFilePattern is a file argument: "<base>/out/<name>" with the base a
	// package base (namePattern's) and the name a package file as the helper
	// accepts it.
	localFilePattern = regexp.MustCompile(`^[A-Za-z0-9@_+][A-Za-z0-9@._+-]{0,127}/out/[A-Za-z0-9@._+-]+-[^-/\s]+-[0-9]+-[A-Za-z0-9_]+\.pkg\.tar\.(zst|xz)$`)
)

// LocalFile is one built package file as PkgLocalHash needs it: the file name
// without its folder, its size and the hex sha256 of its content.
type LocalFile struct {
	Name   string
	Size   int64
	Sha256 string
}

// PkgLocalHash is H for pkg-install-local, the helper's hash byte for byte
// (and aurbuild.LocalHash, tied by a test): "sha256:" and the hex of sha256
// over
//
//	archmaid pkg local v1
//	file <name> <size> <sha256hex>      one line per file, sorted bytewise by line
//
// where <name> is the file name without its folder and the line ends in "\n".
func PkgLocalHash(files []LocalFile) string {
	lines := make([]string, 0, len(files))
	for _, f := range files {
		lines = append(lines, fmt.Sprintf("file %s %d %s\n", f.Name, f.Size, f.Sha256))
	}
	sort.Strings(lines)
	h := sha256.New()
	io.WriteString(h, localHashHeader)
	for _, l := range lines {
		io.WriteString(h, l)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// pkgLocalArgs is the arguments of pkg-install-local: the step's PkgLocal,
// checked as the helper will (hash and file patterns, 1–MaxLocalFiles files,
// none twice). The hash itself cannot be recomputed here (it needs the sizes
// and contents); the helper does, and a mismatch is its exit 3.
func pkgLocalArgs(st steps.Step) ([]string, error) {
	l := st.PkgLocal
	if l == nil {
		return nil, fmt.Errorf("%w: %s needs the built files (step %q has none)", ErrUnsupported, st.Verb, st.ID)
	}
	if !localHashPattern.MatchString(l.Hash) {
		return nil, fmt.Errorf("%w: %s: invalid hash", ErrBadItems, st.Verb)
	}
	if n := len(l.Files); n == 0 || n > MaxLocalFiles {
		return nil, fmt.Errorf("%w: %s takes 1–%d files, got %d", ErrBadItems, st.Verb, MaxLocalFiles, n)
	}
	seen := make(map[string]bool, len(l.Files))
	for _, f := range l.Files {
		if !localFilePattern.MatchString(f) {
			return nil, fmt.Errorf("%w: %s: invalid file %q", ErrBadItems, st.Verb, f)
		}
		if seen[f] {
			return nil, fmt.Errorf("%w: %s: %q named twice", ErrBadItems, st.Verb, f)
		}
		seen[f] = true
	}
	return append([]string{l.Hash}, slices.Clone(l.Files)...), nil
}
