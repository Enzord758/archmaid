package report

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/steps"
)

// world is a machine in a temp dir: a pacman log, a cache folder, yay's,
// temp, journal, a modules folder, and the commands the report may run.
type world struct {
	t                         *testing.T
	log, cache, yay, tmp, jnl string
	mods                      string
	answers                   map[string]func(args []string) ([]byte, error)
	ran                       [][]string
}

func newWorld(t *testing.T) *world {
	t.Helper()
	root := t.TempDir()
	w := &world{t: t, log: filepath.Join(root, "pacman.log"), cache: filepath.Join(root, "cache"),
		yay: filepath.Join(root, "yay"), tmp: filepath.Join(root, "tmp"), jnl: filepath.Join(root, "journal"),
		mods: filepath.Join(root, "modules"), answers: map[string]func([]string) ([]byte, error){}}
	for _, d := range []string{w.cache, w.yay, w.tmp, w.jnl, w.mods} {
		os.MkdirAll(d, 0o755)
	}
	os.WriteFile(w.log, []byte("[t] [ALPM] upgraded before-the-run (1 -> 2)\n"), 0o644)
	w.answers["pacman-conf"] = func([]string) ([]byte, error) { return []byte(w.cache + "/\n"), nil }
	return w
}

func (w *world) config() Config {
	return Config{
		PacmanLog: w.log, ModulesDir: w.mods,
		TmpDirs: []string{w.tmp}, JournalDirs: []string{w.jnl}, UserCacheDir: filepath.Join(w.tmp, "usercache"),
		YayDir:  func() string { return w.yay },
		Running: func() string { return "7.2.10-1-cachyos" },
		Query: func(_ context.Context, name string, args ...string) ([]byte, error) {
			w.ran = append(w.ran, append([]string{name}, args...))
			if f := w.answers[name]; f != nil {
				return f(args)
			}
			return nil, errors.New("no such command: " + name)
		},
	}
}

func (w *world) appendLog(lines ...string) {
	f, err := os.OpenFile(w.log, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		w.t.Fatal(err)
	}
	defer f.Close()
	f.WriteString(strings.Join(lines, "\n") + "\n")
}

func (w *world) fill(dir string, files, size int) {
	for i := 0; i < files; i++ {
		writeN(w.t, filepath.Join(dir, "f"+string(rune('a'+i))+".pkg.tar.zst"), size)
	}
}

func (w *world) empty(dir string) {
	os.RemoveAll(dir)
	os.MkdirAll(dir, 0o755)
}

func pkgStep(id string, argv ...string) steps.Step { return steps.Step{ID: id, Argv: argv} }

func areas(r Report) map[string]Area {
	m := map[string]Area{}
	for _, a := range r.Freed {
		m[a.ID] = a
	}
	return m
}

// TestCollectorSudoRun: Full Cleanup under sudo. yay -Sc does both
// pacman -Sc and yay's cleaning, so it has two rows; the step that did not
// run has none; everything is keyed by step ID.
func TestCollectorSudoRun(t *testing.T) {
	w := newWorld(t)
	w.answers["pacman"] = func(args []string) ([]byte, error) { return []byte("google-chrome\n"), nil }
	w.answers["paccache"] = func([]string) ([]byte, error) { return []byte(paccacheDryFixture), nil }
	w.fill(w.cache, 6, 200<<10)
	w.fill(w.yay, 2, 100<<10)
	w.fill(w.tmp, 2, 40<<10)
	w.fill(w.jnl, 4, 80<<10)
	ctx := context.Background()
	c := New(w.config(), "full-cleanup", time.Now())

	// The audit: one finding, high.
	c.StepStarting(ctx, steps.Step{ID: steps.IDAURAudit})
	c.StepResult(steps.Result{Audit: &steps.AuditNote{Total: 5, Findings: []steps.AuditFinding{{Name: "evil-bin", High: true}, {Name: "meh", High: false}}}})
	c.StepDone(ctx, steps.IDAURAudit, steps.OK)

	// topgrade: the log grows, the cache gets two new packages, linux is replaced.
	c.StepStarting(ctx, pkgStep(steps.IDTopgrade, "topgrade", "-y"))
	w.appendLog(
		"[t] [ALPM] upgraded linux-cachyos (7.2.10-1 -> 7.2.12-1)",
		"[t] [ALPM] upgraded google-chrome (140-1 -> 141-1)",
		"[t] [ALPM] upgraded ripgrep (14-1 -> 15-1)",
		"[t] [ALPM] installed zenity (4.0.1-1)",
		"[t] [ALPM] warning: /etc/pacman.d/mirrorlist installed as /etc/pacman.d/mirrorlist.pacnew")
	w.fill(w.cache, 8, 200<<10) // a..h: two more than before
	c.StepDone(ctx, steps.IDTopgrade, steps.OK)
	os.RemoveAll(w.mods)
	os.MkdirAll(filepath.Join(w.mods, "7.2.12-1-cachyos"), 0o755)
	os.WriteFile(filepath.Join(w.mods, "7.2.12-1-cachyos", "pkgbase"), []byte("linux-cachyos\n"), 0o644)

	// paccache -r frees 5 of the 8 files.
	c.StepStarting(ctx, pkgStep(steps.IDPaccache, "sudo", "paccache", "-r"))
	for _, n := range []string{"fa", "fb", "fc", "fd", "fe"} {
		os.Remove(filepath.Join(w.cache, n+".pkg.tar.zst"))
	}
	c.StepDone(ctx, steps.IDPaccache, steps.OK)

	// The wipe was declined: no row.
	c.StepStarting(ctx, steps.Step{ID: steps.IDPaccacheWipe})
	c.StepDone(ctx, steps.IDPaccacheWipe, steps.Skipped)

	// The orphan removal: its own confirmed lists.
	c.StepStarting(ctx, steps.Step{ID: steps.IDOrphansRemove, Items: []string{"gtk2", "pyxdg"}, Cascade: []string{"gtk2 2.24-1", "pyxdg 0.28-1", "libfoo 1-1"}})
	w.appendLog("[t] [ALPM] removed gtk2 (2.24-1)", "[t] [ALPM] removed pyxdg (0.28-1)", "[t] [ALPM] removed libfoo (1-1)")
	c.StepDone(ctx, steps.IDOrphansRemove, steps.OK)

	// yay -Sc under sudo: pacman -Sc drops one more package, yay's folder is emptied.
	c.StepStarting(ctx, pkgStep(steps.IDYayCache, "yay", "-Sc", "--noconfirm"))
	os.Remove(filepath.Join(w.cache, "ff.pkg.tar.zst"))
	w.empty(w.yay)
	c.StepDone(ctx, steps.IDYayCache, steps.OK)

	c.StepStarting(ctx, pkgStep(steps.IDTmpfiles, "sudo", "systemd-tmpfiles", "--clean"))
	os.Remove(filepath.Join(w.tmp, "fa.pkg.tar.zst"))
	c.StepDone(ctx, steps.IDTmpfiles, steps.OK)

	// The journal vacuum frees nothing; the integrity check is not a cleaning step.
	c.StepStarting(ctx, pkgStep(steps.IDJournal, "sudo", "journalctl", "--vacuum-time=2weeks"))
	c.StepDone(ctx, steps.IDJournal, steps.OK)
	c.StepStarting(ctx, pkgStep(steps.IDIntegrity, "sudo", "pacman", "-Qkq"))
	c.StepDone(ctx, steps.IDIntegrity, steps.Failed)

	r := c.Finish(ctx, FinishInfo{Elapsed: 14 * time.Minute, NotRun: 1})

	if r.Steps != (StepTally{Done: 7, Skipped: 2, Failed: 1}) {
		t.Errorf("steps %+v", r.Steps)
	}
	p := r.Packages
	if p == nil || !reflect.DeepEqual(names(p.Upgraded), []string{"linux-cachyos", "google-chrome", "ripgrep"}) {
		t.Fatalf("packages %+v: the line before the run must not count", p)
	}
	if !p.AURKnown || p.AURUpgraded != 1 || !p.Upgraded[1].AUR || p.Upgraded[2].AUR {
		t.Errorf("AUR marks: %+v", p)
	}
	if names(p.Installed)[0] != "zenity" || len(p.Removed) != 3 {
		t.Errorf("installed %v removed %v", p.Installed, p.Removed)
	}
	if !reflect.DeepEqual(r.Orphans, &Orphans{Names: []string{"gtk2", "pyxdg"}, Total: 3}) {
		t.Errorf("orphans %+v", r.Orphans)
	}
	a := areas(r)
	if _, ok := a[AreaPaccacheWipe]; ok {
		t.Error("the declined wipe has a row")
	}
	if a[AreaPaccache].Bytes < 5*200<<10 || !a[AreaPaccache].Known {
		t.Errorf("paccache row %+v, want about 1 MB freed", a[AreaPaccache])
	}
	if a[AreaPkgUninstalled].Bytes < 200<<10 || a[AreaPkgUninstalled].Bytes >= 2*200<<10 {
		t.Errorf("uninstalled row %+v, want one package's worth", a[AreaPkgUninstalled])
	}
	if a[AreaYay].Bytes < 2*100<<10 || a[AreaTmpfiles].Bytes < 40<<10 || !a[AreaJournal].Known || a[AreaJournal].Bytes != 0 {
		t.Errorf("rows %+v", a)
	}
	var order []string
	for _, row := range r.Freed {
		order = append(order, row.ID)
	}
	if want := []string{AreaPaccache, AreaPkgUninstalled, AreaYay, AreaJournal, AreaTmpfiles}; !reflect.DeepEqual(order, want) {
		t.Errorf("row order %v, want %v", order, want)
	}
	var sum uint64
	for _, row := range r.Freed {
		sum += row.Bytes
	}
	if r.FreedBytes != sum || r.FreedPartial {
		t.Errorf("freed %d partial %v, rows sum to %d", r.FreedBytes, r.FreedPartial, sum)
	}
	if r.Cache == nil || r.Cache.Source != "paccache" || r.Cache.Packages != 312 {
		t.Errorf("cache %+v", r.Cache)
	}
	if r.Audit == nil || r.Audit.Findings != 2 || r.Audit.High != 1 {
		t.Errorf("audit %+v", r.Audit)
	}

	var kinds []string
	for _, it := range r.Attention {
		kinds = append(kinds, it.Kind)
	}
	if want := []string{KindRestart, KindPacnew, KindAudit}; !reflect.DeepEqual(kinds, want) {
		t.Fatalf("attention %v, want %v", kinds, want)
	}
	if got, want := r.Attention[0].Text, "Restart recommended: linux-cachyos upgraded (running 7.2.10-1-cachyos, installed 7.2.12-1-cachyos)"; got != want {
		t.Errorf("restart item %q, want %q", got, want)
	}
	if got := r.Attention[1].Text; got != "1 new .pacnew: /etc/pacman.d/mirrorlist.pacnew (merge it with pacdiff)" {
		t.Errorf("pacnew item %q", got)
	}
	s := r.Summary()
	if s.Upgraded != 3 || s.AUR != 1 || s.FreedBytes != r.FreedBytes || !reflect.DeepEqual(s.Attention, []string{"Restart recommended", "1 new .pacnew", "1 AUR warning"}) {
		t.Errorf("summary %+v", s)
	}

	// Every command was asked once at the end or for the folders, read-only.
	for _, argv := range w.ran {
		switch argv[0] {
		case "pacman-conf", "paccache", "pacman":
		default:
			t.Errorf("ran %v", argv)
		}
		if argv[0] == "pacman" && strings.Join(argv, " ") != "pacman -Qqm --color never" {
			t.Errorf("pacman ran as %v, want only -Qqm", argv)
		}
	}
}

// TestCollectorPolkitRun: under polkit the root steps are one call with
// Parts. They are measured before the call; each part's end moves the next
// part's start, so paccache and pacman -Sc don't both count the first one's
// work.
func TestCollectorPolkitRun(t *testing.T) {
	w := newWorld(t)
	w.fill(w.cache, 6, 100<<10)
	w.fill(w.tmp, 2, 100<<10)
	ctx := context.Background()
	c := New(w.config(), "full-cleanup", time.Now())
	call := steps.Step{ID: "call", Parts: []steps.Step{
		{ID: steps.IDOrphansRemove, Items: []string{"gtk2"}},
		{ID: steps.IDPaccache},
		{ID: steps.IDPkgCacheUninstalled},
		{ID: steps.IDTmpfiles},
	}}
	c.StepStarting(ctx, call)
	c.StepDone(ctx, steps.IDOrphansRemove, steps.OK)
	for _, n := range []string{"fa", "fb", "fc"} {
		os.Remove(filepath.Join(w.cache, n+".pkg.tar.zst"))
	}
	c.StepDone(ctx, steps.IDPaccache, steps.OK)
	os.Remove(filepath.Join(w.cache, "fd.pkg.tar.zst"))
	c.StepDone(ctx, steps.IDPkgCacheUninstalled, steps.OK)
	c.StepDone(ctx, steps.IDTmpfiles, steps.OK)
	// yay, as the user, without sudo.
	c.StepStarting(ctx, pkgStep(steps.IDYayCache, "yay", "-Sc", "--aur", "--noconfirm"))
	w.empty(w.yay)
	c.StepDone(ctx, steps.IDYayCache, steps.OK)
	r := c.Finish(ctx, FinishInfo{})

	a := areas(r)
	each := uint64(100 << 10)
	if a[AreaPaccache].Bytes < 3*each || a[AreaPaccache].Bytes >= 4*each {
		t.Errorf("paccache freed %d, want three packages' worth", a[AreaPaccache].Bytes)
	}
	if a[AreaPkgUninstalled].Bytes < each || a[AreaPkgUninstalled].Bytes >= 2*each {
		t.Errorf("pacman -Sc freed %d, want one package's worth", a[AreaPkgUninstalled].Bytes)
	}
	if _, ok := a[AreaYay]; !ok || len(a) != 4 {
		t.Errorf("rows %v, want paccache, pacman -Sc, yay and tmpfiles (a row freeing 0 is a row)", a)
	}
	if r.Orphans == nil || r.Orphans.Names[0] != "gtk2" {
		t.Errorf("orphans %+v", r.Orphans)
	}
}

// TestCollectorUnknownAndClamp: a target the user can't read is "unknown",
// a folder that grew during its step frees 0.
func TestCollectorUnknownAndClamp(t *testing.T) {
	w := newWorld(t)
	w.fill(w.cache, 2, 100<<10)
	blocker := filepath.Join(t.TempDir(), "file")
	os.WriteFile(blocker, nil, 0o644)
	cfg := w.config()
	cfg.JournalDirs = []string{filepath.Join(blocker, "journal")} // ENOTDIR: not readable, not "gone"
	ctx := context.Background()
	c := New(cfg, "full-cleanup", time.Now())
	c.StepStarting(ctx, pkgStep(steps.IDPaccache, "paccache", "-r"))
	w.fill(w.cache, 6, 100<<10) // grew
	c.StepDone(ctx, steps.IDPaccache, steps.OK)
	c.StepStarting(ctx, pkgStep(steps.IDJournal, "journalctl"))
	c.StepDone(ctx, steps.IDJournal, steps.OK)
	r := c.Finish(ctx, FinishInfo{})
	a := areas(r)
	if !a[AreaPaccache].Known || a[AreaPaccache].Bytes != 0 {
		t.Errorf("a growing cache: %+v, want a known 0", a[AreaPaccache])
	}
	if a[AreaJournal].Known || a[AreaJournal].Bytes != 0 {
		t.Errorf("an unreadable journal: %+v, want unknown", a[AreaJournal])
	}
	if !r.FreedPartial || r.FreedBytes != 0 {
		t.Errorf("total %d partial %v", r.FreedBytes, r.FreedPartial)
	}
}

// TestCollectorFailedAndAbortedStepsAreMeasured, and refused/skipped ones
// have no row.
func TestCollectorStepStatuses(t *testing.T) {
	w := newWorld(t)
	w.fill(w.cache, 4, 100<<10)
	w.fill(w.tmp, 4, 100<<10)
	w.fill(w.jnl, 4, 100<<10)
	ctx := context.Background()
	c := New(w.config(), "full-cleanup", time.Now())
	c.StepStarting(ctx, pkgStep(steps.IDPaccache, "paccache"))
	os.Remove(filepath.Join(w.cache, "fa.pkg.tar.zst"))
	c.StepDone(ctx, steps.IDPaccache, steps.Failed) // failed part-way: what it freed counts
	c.StepStarting(ctx, pkgStep(steps.IDTmpfiles, "x"))
	c.StepDone(ctx, steps.IDTmpfiles, steps.AuthRefused)
	c.StepStarting(ctx, pkgStep(steps.IDJournal, "x"))
	c.StepDone(ctx, steps.IDJournal, steps.Aborted)
	r := c.Finish(ctx, FinishInfo{})
	a := areas(r)
	if a[AreaPaccache].Bytes < 100<<10 {
		t.Errorf("failed paccache %+v", a[AreaPaccache])
	}
	if _, ok := a[AreaTmpfiles]; ok {
		t.Error("a refused step has a row")
	}
	if _, ok := a[AreaJournal]; !ok {
		t.Error("an aborted step has no row")
	}
	if r.Steps != (StepTally{Skipped: 2, Failed: 1}) {
		t.Errorf("steps %+v", r.Steps)
	}
}

// TestCollectorAURUnknown: pacman -Qqm failing is said, not guessed;
// exiting 1 with nothing means no foreign packages.
func TestCollectorAURUnknown(t *testing.T) {
	run := func(qm func([]string) ([]byte, error)) Report {
		w := newWorld(t)
		w.answers["pacman"] = qm
		c := New(w.config(), "topgrade", time.Now())
		c.StepStarting(context.Background(), pkgStep(steps.IDTopgrade, "topgrade"))
		w.appendLog("[t] [ALPM] upgraded a (1 -> 2)")
		c.StepDone(context.Background(), steps.IDTopgrade, steps.OK)
		return c.Finish(context.Background(), FinishInfo{})
	}
	r := run(func([]string) ([]byte, error) { return nil, errors.New("boom") })
	if r.Packages.AURKnown || len(r.Notes) != 1 || !strings.Contains(r.Notes[0], "pacman -Qqm") {
		t.Errorf("failed -Qqm: known %v notes %v", r.Packages.AURKnown, r.Notes)
	}
	exit1 := exec.Command("sh", "-c", "exit 1").Run()
	r = run(func([]string) ([]byte, error) { return nil, exit1 })
	if !r.Packages.AURKnown || r.Packages.AURUpgraded != 0 || len(r.Notes) != 0 {
		t.Errorf("-Qqm exit 1 with no output: known %v aur %d notes %v", r.Packages.AURKnown, r.Packages.AURUpgraded, r.Notes)
	}
}

// TestCollectorLogProblems: an unreadable or rotated log is said in Notes.
func TestCollectorLogProblems(t *testing.T) {
	w := newWorld(t)
	w.answers["pacman"] = func([]string) ([]byte, error) { return nil, nil }
	c := New(w.config(), "topgrade", time.Now())
	c.StepStarting(context.Background(), pkgStep(steps.IDTopgrade, "topgrade"))
	os.Rename(w.log, w.log+".1")
	os.WriteFile(w.log, []byte("[t] [ALPM] upgraded a (1 -> 2)\n"), 0o644)
	c.StepDone(context.Background(), steps.IDTopgrade, steps.OK)
	r := c.Finish(context.Background(), FinishInfo{})
	if r.Packages == nil || !r.Packages.Partial || len(r.Notes) == 0 || !strings.Contains(r.Notes[0], "rotated") {
		t.Errorf("rotated: %+v notes %v", r.Packages, r.Notes)
	}

	w = newWorld(t)
	c = New(w.config(), "topgrade", time.Now())
	c.StepStarting(context.Background(), pkgStep(steps.IDTopgrade, "topgrade"))
	os.Remove(w.log)
	c.StepDone(context.Background(), steps.IDTopgrade, steps.OK)
	r = c.Finish(context.Background(), FinishInfo{})
	if r.Packages != nil || len(r.Notes) != 1 || r.HasData() {
		t.Errorf("unreadable: %+v notes %v", r.Packages, r.Notes)
	}
}

// TestCollectorNoPackageStep: an action that changes no packages never reads
// the log, and a quiet cleaning has a report with only its rows.
func TestCollectorNoPackageStep(t *testing.T) {
	w := newWorld(t)
	w.fill(w.tmp, 1, 4096)
	os.Remove(w.log) // reading it would be an error note
	c := New(w.config(), "user-cache", time.Now())
	c.StepStarting(context.Background(), pkgStep(steps.IDUserCache, "bash"))
	c.StepDone(context.Background(), steps.IDUserCache, steps.OK)
	r := c.Finish(context.Background(), FinishInfo{})
	if r.Packages != nil || len(r.Notes) != 0 || len(r.Freed) != 1 || r.Freed[0].ID != AreaUserCache {
		t.Errorf("report %+v", r)
	}
	if r.Cache != nil {
		t.Error("a user-cache run reports the package cache")
	}
}

// TestRestartItemTexts: the restart item's wording for each cause.
func TestRestartItemTexts(t *testing.T) {
	w := newWorld(t)
	c := New(w.config(), "topgrade", time.Now()) // running 7.2.10-1-cachyos, no module folders
	if _, ok := c.restartItem(up("systemd", "mesa")); !ok {
		t.Fatal("systemd and mesa upgraded: no restart item")
	}
	it, _ := c.restartItem(up("systemd", "mesa"))
	if it.Text != "Restart recommended: systemd, mesa upgraded" || !reflect.DeepEqual(it.Detail, []string{"systemd", "mesa"}) {
		t.Errorf("item %+v", it)
	}
	if _, ok := c.restartItem(up("ripgrep")); ok {
		t.Error("ripgrep asks for a restart")
	}
	os.MkdirAll(filepath.Join(w.mods, "9.9.9-arch1-1"), 0o755)
	os.WriteFile(filepath.Join(w.mods, "9.9.9-arch1-1", "pkgbase"), []byte("linux\n"), 0o644)
	it, ok := c.restartItem(up("ripgrep"))
	if !ok || it.Text != "Restart recommended: the running kernel (7.2.10-1-cachyos) was replaced" {
		t.Errorf("kernel replaced by no package of this run: %+v %v", it, ok)
	}
}

// TestPacnewItems: up to three files are named, the rest counted; pacsave
// has its own item.
func TestPacnewItems(t *testing.T) {
	c := New(newWorld(t).config(), "topgrade", time.Now())
	items := c.fileItems(LogChanges{
		Pacnew:  []string{"/etc/a.pacnew", "/etc/b.pacnew", "/etc/c.pacnew", "/etc/d.pacnew"},
		Pacsave: []string{"/etc/x.pacsave"},
	})
	if len(items) != 2 || items[0].Text != "4 new .pacnew: /etc/a.pacnew, /etc/b.pacnew, /etc/c.pacnew +1 more (merge it with pacdiff)" ||
		items[0].Short != "4 new .pacnew" || len(items[0].Detail) != 4 || items[1].Kind != KindPacsave {
		t.Errorf("items %+v", items)
	}
}

// TestSummaryPackagesFreedBytes: a Packages page removal puts the size it
// frees in the stored summary's FreedBytes (what the widget shows as
// "freed"); an install, which uses space, adds nothing and never goes
// negative; a mixed run counts only a net shrink; cleaning rows are kept and
// summed with it; a failed run (Packages nil) has none.
func TestSummaryPackagesFreedBytes(t *testing.T) {
	pk := func(inst, rem int, change int64, known bool) *Packages {
		return &Packages{
			Installed: make([]Pkg, inst), Removed: make([]Pkg, rem),
			SizeChange: change, SizeKnown: known,
		}
	}
	for _, tc := range []struct {
		name string
		r    Report
		want uint64
	}{
		{"removal", Report{Action: ActionPackages, Packages: pk(0, 1, -3050000, true)}, 3050000},
		{"install", Report{Action: ActionPackages, Packages: pk(1, 0, 4000000, true)}, 0},
		{"mixed net shrink (AUR build + removed deps)", Report{Action: ActionPackages, Packages: pk(1, 3, -2000000, true)}, 2000000},
		{"mixed net growth", Report{Action: ActionPackages, Packages: pk(2, 1, 500, true)}, 0},
		{"size unknown", Report{Action: ActionPackages, Packages: pk(0, 1, -9, false)}, 0},
		{"failed before any change", Report{Action: ActionPackages}, 0},
		{"other action ignores it", Report{Action: "full", Packages: pk(0, 1, -9, true)}, 0},
		{"summed with cleaning rows", Report{Action: ActionPackages, Packages: pk(0, 1, -100, true), FreedBytes: 50}, 150},
	} {
		if got := tc.r.Summary().FreedBytes; got != tc.want {
			t.Errorf("%s: FreedBytes %d, want %d", tc.name, got, tc.want)
		}
	}
	r := Report{Action: ActionPackages, Packages: pk(0, 1, -100, true)}
	if r.FreedBytes != 0 || r.Summary().Removed != 1 {
		t.Errorf("the report's own FreedBytes must stay the cleaning steps' only: %+v", r)
	}
}
