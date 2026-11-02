// Package pkgdbtest writes pacman database fixtures from Go values, for the
// tests of pkgdb and of whatever sits on top of it (the Packages page's
// model). Nothing is committed as a binary: a sync DB is a tar built on the
// spot, gzip or zstd. Test-only; no product code may import it.
package pkgdbtest

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/pkgdb"
	"github.com/klauspost/compress/zstd"
)

// WriteSync writes <dir>/<repo>.db with one entry per package, compressed
// with zstd or gzip. Repo, installed state and AUR data are ignored.
func WriteSync(t testing.TB, dir, repo string, useZstd bool, pkgs ...pkgdb.Package) {
	t.Helper()
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	for _, p := range pkgs {
		d := Desc(p, false)
		h := &tar.Header{Name: p.Name + "-" + p.Version + "/desc", Mode: 0o644, Size: int64(len(d)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(d))
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if useZstd {
		zw, err := zstd.NewWriter(&out)
		if err != nil {
			t.Fatal(err)
		}
		zw.Write(raw.Bytes())
		zw.Close()
	} else {
		gw := gzip.NewWriter(&out)
		gw.Write(raw.Bytes())
		gw.Close()
	}
	write(t, filepath.Join(dir, repo+".db"), out.Bytes())
}

// WriteLocal writes <dir>/<name>-<version>/desc for each package: Reason (a
// ReasonDep writes %REASON% 1), InstallDate and InstalledSize are kept.
// The version written is InstalledVersion when set, else Version.
func WriteLocal(t testing.TB, dir string, pkgs ...pkgdb.Package) {
	t.Helper()
	for _, p := range pkgs {
		if p.InstalledVersion != "" {
			p.Version = p.InstalledVersion
		}
		write(t, filepath.Join(dir, p.Name+"-"+p.Version, "desc"), []byte(Desc(p, true)))
	}
}

// WriteConf writes a pacman.conf with an [options] section and the repos in
// order, each with an Include line (which pkgdb does not follow).
func WriteConf(t testing.TB, file string, repos ...string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("# test\n[options]\nHoldPkg = pacman glibc\n\n")
	for _, r := range repos {
		fmt.Fprintf(&b, "[%s]\nInclude = /etc/pacman.d/mirrorlist\n\n", r)
	}
	write(t, file, []byte(b.String()))
}

func write(t testing.TB, file string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// Desc renders p as a desc file. Values are written verbatim, so a test may
// put hostile text in them.
func Desc(p pkgdb.Package, local bool) string {
	var b strings.Builder
	field := func(key string, vals ...string) {
		if len(vals) == 0 || (len(vals) == 1 && vals[0] == "") {
			return
		}
		fmt.Fprintf(&b, "%%%s%%\n%s\n\n", key, strings.Join(vals, "\n"))
	}
	deps := func(key string, ds []pkgdb.Dep, opt bool) {
		var l []string
		for _, d := range ds {
			if s := d.Raw; opt && d.Desc != "" {
				l = append(l, s+": "+d.Desc)
			} else {
				l = append(l, s)
			}
		}
		field(key, l...)
	}
	field("NAME", p.Name)
	field("VERSION", p.Version)
	field("DESC", p.Description)
	field("URL", p.URL)
	field("LICENSE", p.Licenses...)
	field("GROUPS", p.Groups...)
	field("PACKAGER", p.Packager)
	if !p.BuildDate.IsZero() {
		field("BUILDDATE", fmt.Sprint(p.BuildDate.Unix()))
	}
	if local {
		if !p.InstallDate.IsZero() {
			field("INSTALLDATE", fmt.Sprint(p.InstallDate.Unix()))
		}
		if p.Reason == pkgdb.ReasonDep {
			field("REASON", "1")
		}
		if p.InstalledSize > 0 {
			field("SIZE", fmt.Sprint(p.InstalledSize))
		}
	} else {
		if p.DownloadSize > 0 {
			field("CSIZE", fmt.Sprint(p.DownloadSize))
		}
		if p.InstalledSize > 0 {
			field("ISIZE", fmt.Sprint(p.InstalledSize))
		}
	}
	deps("DEPENDS", p.Depends, false)
	deps("OPTDEPENDS", p.OptDepends, true)
	deps("CONFLICTS", p.Conflicts, false)
	deps("REPLACES", p.Replaces, false)
	deps("PROVIDES", p.Provides, false)
	return b.String()
}
