package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// isolateDownloads gives the test a scratch $HOME and XDG config dir, and
// clears every downloads override (flag and env), so the resolver never sees
// the real user-dirs.dirs or ~/Download. Returns the scratch home.
func isolateDownloads(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv(downloadsEnv, "")
	orig := downloadsFlag
	downloadsFlag = ""
	t.Cleanup(func() { downloadsFlag = orig })
	return home
}

// writeUserDirs writes $XDG_CONFIG_HOME/user-dirs.dirs with the given
// XDG_DOWNLOAD_DIR value (already quoted as the file would have it).
func writeUserDirs(t *testing.T, home, value string) {
	t.Helper()
	cfg := filepath.Join(home, ".config")
	if err := os.MkdirAll(cfg, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "# written by xdg-user-dirs-update\nXDG_DESKTOP_DIR=\"$HOME/Desktop\"\n" +
		"XDG_DOWNLOAD_DIR=" + value + "\n"
	if err := os.WriteFile(filepath.Join(cfg, "user-dirs.dirs"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mkdirs(t *testing.T, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// TestResolveDownloadsDir pins the resolver's order (flag → env → XDG →
// ~/Downloads → ~/Download) and its refusals: never $HOME (however it's
// spelled — "$HOME/", a symlink to it), never / or an ancestor of $HOME, and
// nothing outside $HOME unless the flag names it.
func TestResolveDownloadsDir(t *testing.T) {
	type env struct{ home, outside string }
	cases := []struct {
		name    string
		setup   func(t *testing.T, e env) // creates dirs, sets flag/env/XDG
		want    func(e env) string        // expected dir (before EvalSymlinks)
		wantErr string                    // substring of the error, if any
	}{
		{
			name: "XDG says $HOME/ and only ~/Download exists (this machine)",
			setup: func(t *testing.T, e env) {
				writeUserDirs(t, e.home, `"$HOME/"`)
				mkdirs(t, filepath.Join(e.home, "Download"))
			},
			want: func(e env) string { return filepath.Join(e.home, "Download") },
		},
		{
			name: "XDG says $HOME/ and nothing else exists",
			setup: func(t *testing.T, e env) {
				writeUserDirs(t, e.home, `"$HOME/"`)
			},
			wantErr: "No downloads folder found",
		},
		{
			name: "~/Download only",
			setup: func(t *testing.T, e env) {
				mkdirs(t, filepath.Join(e.home, "Download"))
			},
			want: func(e env) string { return filepath.Join(e.home, "Download") },
		},
		{
			name: "~/Downloads wins over ~/Download",
			setup: func(t *testing.T, e env) {
				mkdirs(t, filepath.Join(e.home, "Downloads"), filepath.Join(e.home, "Download"))
			},
			want: func(e env) string { return filepath.Join(e.home, "Downloads") },
		},
		{
			name: "XDG dir is used when it is a real folder in $HOME",
			setup: func(t *testing.T, e env) {
				writeUserDirs(t, e.home, `"$HOME/dl"`)
				mkdirs(t, filepath.Join(e.home, "dl"), filepath.Join(e.home, "Downloads"))
			},
			want: func(e env) string { return filepath.Join(e.home, "dl") },
		},
		{
			name: "XDG ${HOME} spelling expands too",
			setup: func(t *testing.T, e env) {
				writeUserDirs(t, e.home, `"${HOME}/dl"`)
				mkdirs(t, filepath.Join(e.home, "dl"))
			},
			want: func(e env) string { return filepath.Join(e.home, "dl") },
		},
		{
			name: "XDG dir outside $HOME is skipped",
			setup: func(t *testing.T, e env) {
				writeUserDirs(t, e.home, `"`+e.outside+`"`)
				mkdirs(t, filepath.Join(e.home, "Download"))
			},
			want: func(e env) string { return filepath.Join(e.home, "Download") },
		},
		{
			name: "~/Downloads symlinked to $HOME is refused",
			setup: func(t *testing.T, e env) {
				if err := os.Symlink(e.home, filepath.Join(e.home, "Downloads")); err != nil {
					t.Fatal(err)
				}
				mkdirs(t, filepath.Join(e.home, "Download"))
			},
			want: func(e env) string { return filepath.Join(e.home, "Download") },
		},
		{
			name: "~/Downloads symlinked to a folder inside $HOME resolves",
			setup: func(t *testing.T, e env) {
				mkdirs(t, filepath.Join(e.home, "data", "dl"))
				if err := os.Symlink(filepath.Join(e.home, "data", "dl"), filepath.Join(e.home, "Downloads")); err != nil {
					t.Fatal(err)
				}
			},
			want: func(e env) string { return filepath.Join(e.home, "data", "dl") },
		},
		{
			name: "~/Downloads as a plain file is skipped",
			setup: func(t *testing.T, e env) {
				if err := os.WriteFile(filepath.Join(e.home, "Downloads"), nil, 0o644); err != nil {
					t.Fatal(err)
				}
				mkdirs(t, filepath.Join(e.home, "Download"))
			},
			want: func(e env) string { return filepath.Join(e.home, "Download") },
		},
		{
			name: "env beats XDG and ~/Downloads",
			setup: func(t *testing.T, e env) {
				mkdirs(t, filepath.Join(e.home, "mine"), filepath.Join(e.home, "Downloads"))
				writeUserDirs(t, e.home, `"$HOME/Downloads"`)
				t.Setenv(downloadsEnv, filepath.Join(e.home, "mine"))
			},
			want: func(e env) string { return filepath.Join(e.home, "mine") },
		},
		{
			name: "env outside $HOME is skipped",
			setup: func(t *testing.T, e env) {
				t.Setenv(downloadsEnv, e.outside)
				mkdirs(t, filepath.Join(e.home, "Downloads"))
			},
			want: func(e env) string { return filepath.Join(e.home, "Downloads") },
		},
		{
			name: "env of / or an ancestor of $HOME is skipped",
			setup: func(t *testing.T, e env) {
				t.Setenv(downloadsEnv, filepath.Dir(e.home))
				writeUserDirs(t, e.home, `"/"`)
			},
			wantErr: "No downloads folder found",
		},
		{
			name: "env equal to $HOME is skipped",
			setup: func(t *testing.T, e env) {
				t.Setenv(downloadsEnv, e.home+"/")
			},
			wantErr: "No downloads folder found",
		},
		{
			name: "flag outside $HOME is allowed",
			setup: func(t *testing.T, e env) {
				mkdirs(t, filepath.Join(e.home, "Downloads"))
				downloadsFlag = e.outside
			},
			want: func(e env) string { return e.outside },
		},
		{
			name: "flag beats env",
			setup: func(t *testing.T, e env) {
				mkdirs(t, filepath.Join(e.home, "a"), filepath.Join(e.home, "b"))
				t.Setenv(downloadsEnv, filepath.Join(e.home, "a"))
				downloadsFlag = filepath.Join(e.home, "b")
			},
			want: func(e env) string { return filepath.Join(e.home, "b") },
		},
		{
			name: "flag naming $HOME is refused, not skipped",
			setup: func(t *testing.T, e env) {
				mkdirs(t, filepath.Join(e.home, "Downloads"))
				downloadsFlag = e.home
			},
			wantErr: "contains your home folder",
		},
		{
			name: "flag naming / is refused",
			setup: func(t *testing.T, e env) {
				downloadsFlag = "/"
			},
			wantErr: "root folder",
		},
		{
			name: "flag naming a missing folder is refused, not skipped",
			setup: func(t *testing.T, e env) {
				mkdirs(t, filepath.Join(e.home, "Downloads"))
				downloadsFlag = filepath.Join(e.home, "nope")
			},
			wantErr: "no such folder",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := env{home: isolateDownloads(t), outside: t.TempDir()}
			tc.setup(t, e)

			got, err := resolveDownloadsDir()
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("resolveDownloadsDir() = %q, %v; want error containing %q", got, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveDownloadsDir() error: %v", err)
			}
			want, err := filepath.EvalSymlinks(tc.want(e))
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("resolveDownloadsDir() = %q, want %q", got, want)
			}
		})
	}
}

// TestResolveDownloadsDirSymlinkedHome: when $HOME itself is a symlink (e.g.
// /home → /var/home), folders inside its real location still count as inside
// $HOME, and the real location itself is still refused.
func TestResolveDownloadsDirSymlinkedHome(t *testing.T) {
	isolateDownloads(t)
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "home")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", link)
	t.Setenv("XDG_CONFIG_HOME", "")
	mkdirs(t, filepath.Join(real, "Download"))

	got, err := resolveDownloadsDir()
	want, _ := filepath.EvalSymlinks(filepath.Join(real, "Download"))
	if err != nil || got != want {
		t.Fatalf("resolveDownloadsDir() = %q, %v; want %q", got, err, want)
	}

	t.Setenv(downloadsEnv, real)
	if got, _ := resolveDownloadsDir(); got != want {
		t.Fatalf("env naming the real $HOME was used: got %q, want fallback %q", got, want)
	}
}

// TestDiscoverDownloadsNamesFolder: the notice names the folder it looked in
// (as ~/…), and with no folder at all it says how to set one.
func TestDiscoverDownloadsNamesFolder(t *testing.T) {
	home := isolateDownloads(t)

	msg := discoverDownloads()().(discoveryMsg)
	if !strings.Contains(msg.notice, "No downloads folder found (set --downloads or ARCHMAID_DOWNLOADS)") {
		t.Fatalf("notice = %q, want the no-folder notice", msg.notice)
	}

	mkdirs(t, filepath.Join(home, "Download"))
	msg = discoverDownloads()().(discoveryMsg)
	if !strings.Contains(msg.notice, "~/Download") {
		t.Fatalf("notice = %q, want it to name ~/Download", msg.notice)
	}
}

// ── old-entry deletion ───────────────────────────────────────────────────────
//
// Age is ctime, which a test can't set. So a test builds the "old" part of
// its fixture, takes a mark between two pauses, builds the "new" part, and
// moves the clock (downloadsNow) to mark+30 days: the cutoff then falls on
// the mark, after every old ctime and before every new one (the pauses
// absorb the kernel's coarse timestamp granularity).

// ctimeMark pauses, returns now, and pauses again: a point strictly between
// the ctimes of what was made before it and what is made after.
func ctimeMark(t *testing.T) time.Time {
	t.Helper()
	time.Sleep(30 * time.Millisecond)
	mark := time.Now()
	time.Sleep(30 * time.Millisecond)
	return mark
}

// cutoffAt sets the downloads clock so the 30-day cutoff lands on mark.
func cutoffAt(t *testing.T, mark time.Time) {
	t.Helper()
	orig := downloadsNow
	downloadsNow = func() time.Time { return mark.Add(downloadsMaxAge) }
	t.Cleanup(func() { downloadsNow = orig })
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// discoverDownloadStep runs discovery and returns its single delete step.
func discoverDownloadStep(t *testing.T) step {
	t.Helper()
	msg := discoverDownloads()().(discoveryMsg)
	if msg.notice != "" || len(msg.pending) != 1 {
		t.Fatalf("discovery: notice=%q pending=%d, want one step", msg.notice, len(msg.pending))
	}
	s := msg.pending[0]
	if s.run == nil || s.command != "" {
		t.Fatalf("delete step must be in-process, got command=%q run=nil:%v", s.command, s.run == nil)
	}
	if !s.optional || !s.alwaysLog {
		t.Fatalf("delete step optional=%v alwaysLog=%v, want both", s.optional, s.alwaysLog)
	}
	return s
}

// TestDeleteOldDownloads: old files, old folders and old symlinks go; new
// files stay; a folder with anything new deep inside stays; a symlink is
// removed as a link, never followed into its target; and the downloads folder
// itself is never touched.
func TestDeleteOldDownloads(t *testing.T) {
	home := isolateDownloads(t)
	dl := filepath.Join(home, "Download")
	outside := t.TempDir()

	// Old.
	writeFile(t, filepath.Join(dl, "old.iso"), "x")
	writeFile(t, filepath.Join(dl, "olddir", "inner", "f.txt"), "x")
	writeFile(t, filepath.Join(dl, "busydir", "sub", "old.txt"), "x")
	writeFile(t, filepath.Join(outside, "target", "keep.txt"), "x")
	if err := os.Symlink(filepath.Join(outside, "target"), filepath.Join(dl, "link")); err != nil {
		t.Fatal(err)
	}
	mark := ctimeMark(t)
	// New: a fresh file, and a fresh file two levels into busydir (which
	// leaves busydir's own ctime old — only the walk can see it).
	writeFile(t, filepath.Join(dl, "new.txt"), "x")
	writeFile(t, filepath.Join(dl, "busydir", "sub", "new.txt"), "x")
	cutoffAt(t, mark)

	s := discoverDownloadStep(t)
	for _, want := range []string{"Delete 3 item(s)", "~/Download", "link", "old.iso", "olddir"} {
		if !strings.Contains(s.confirm, want) {
			t.Fatalf("confirm = %q, want it to contain %q", s.confirm, want)
		}
	}
	for _, not := range []string{"new.txt", "busydir"} {
		if strings.Contains(s.confirm, not) {
			t.Fatalf("confirm = %q, must not list %q", s.confirm, not)
		}
	}

	res := s.run(context.Background())
	if res.err != nil {
		t.Fatalf("delete step failed: %v\n%s", res.err, res.output)
	}
	for _, gone := range []string{"old.iso", "olddir", "link"} {
		if exists(filepath.Join(dl, gone)) {
			t.Errorf("%s still exists", gone)
		}
	}
	for _, kept := range []string{"new.txt", "busydir/sub/new.txt", "busydir/sub/old.txt"} {
		if !exists(filepath.Join(dl, kept)) {
			t.Errorf("%s was deleted", kept)
		}
	}
	if !exists(filepath.Join(outside, "target", "keep.txt")) {
		t.Fatal("the symlink's target was deleted through the link")
	}
	if !strings.Contains(res.output, "deleted: old.iso") {
		t.Fatalf("output = %q, want it to report each deletion", res.output)
	}
}

// TestDeleteOldDownloadsIgnoresMtime: a fresh download with a back-dated mtime
// (wget, curl -R and unzip all do this) is not old — only ctime counts — and
// an old folder with nothing old in it is reported as nothing to do, folder
// intact.
func TestDeleteOldDownloadsIgnoresMtime(t *testing.T) {
	home := isolateDownloads(t)
	dl := filepath.Join(home, "Downloads")
	mkdirs(t, dl)
	mark := ctimeMark(t)
	p := filepath.Join(dl, "archive.zip")
	writeFile(t, p, "x")
	ancient := time.Now().Add(-5 * 365 * 24 * time.Hour)
	if err := os.Chtimes(p, ancient, ancient); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(dl, ancient, ancient); err != nil {
		t.Fatal(err)
	}
	cutoffAt(t, mark)

	msg := discoverDownloads()().(discoveryMsg)
	if len(msg.pending) != 0 || !strings.Contains(msg.notice, "Nothing older than 30 days in ~/Downloads") {
		t.Fatalf("notice=%q pending=%d, want nothing to do", msg.notice, len(msg.pending))
	}
	if !exists(p) {
		t.Fatal("fresh file with an old mtime was deleted")
	}
}

// TestDeleteOldDownloadsRechecksBeforeDeleting: an entry touched or replaced
// between the scan (the confirm) and the delete is kept; the rest still go.
func TestDeleteOldDownloadsRechecksBeforeDeleting(t *testing.T) {
	home := isolateDownloads(t)
	dl := filepath.Join(home, "Downloads")
	for _, n := range []string{"a.txt", "b.txt", "c.txt"} {
		writeFile(t, filepath.Join(dl, n), "x")
	}
	mark := ctimeMark(t)
	cutoffAt(t, mark)

	s := discoverDownloadStep(t)
	if !strings.Contains(s.confirm, "Delete 3 item(s)") {
		t.Fatalf("confirm = %q, want 3 items", s.confirm)
	}

	// After the confirm: a.txt is written to, b.txt is replaced.
	f, err := os.OpenFile(filepath.Join(dl, "a.txt"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("more")
	f.Close()
	os.Remove(filepath.Join(dl, "b.txt"))
	writeFile(t, filepath.Join(dl, "b.txt"), "new")

	res := s.run(context.Background())
	if res.err != nil {
		t.Fatalf("delete step failed: %v", res.err)
	}
	if !exists(filepath.Join(dl, "a.txt")) || !exists(filepath.Join(dl, "b.txt")) {
		t.Fatalf("an entry changed since the scan was deleted:\n%s", res.output)
	}
	if exists(filepath.Join(dl, "c.txt")) {
		t.Fatal("c.txt, unchanged and old, was kept")
	}
	if !strings.Contains(res.output, "kept (changed since the scan): a.txt") {
		t.Fatalf("output = %q, want the kept entries reported", res.output)
	}
}

// TestDeleteOldDownloadsFolderSwapped: if the folder was replaced between the
// scan and the delete (say, by a symlink elsewhere), nothing is deleted.
func TestDeleteOldDownloadsFolderSwapped(t *testing.T) {
	home := isolateDownloads(t)
	dl := filepath.Join(home, "Downloads")
	writeFile(t, filepath.Join(dl, "a.txt"), "x")
	other := filepath.Join(home, "other")
	writeFile(t, filepath.Join(other, "a.txt"), "x")
	mark := ctimeMark(t)
	cutoffAt(t, mark)

	s := discoverDownloadStep(t)
	if err := os.Rename(dl, filepath.Join(home, "moved")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, dl); err != nil {
		t.Fatal(err)
	}

	res := s.run(context.Background())
	if res.err == nil {
		t.Fatal("want an error when the folder changed since the scan")
	}
	if !exists(filepath.Join(other, "a.txt")) || !exists(filepath.Join(home, "moved", "a.txt")) {
		t.Fatal("something was deleted after the folder changed")
	}
}

// TestDeleteOldDownloadsDryRun: the dry run reports what it would delete and
// deletes nothing.
func TestDeleteOldDownloadsDryRun(t *testing.T) {
	home := isolateDownloads(t)
	dl := filepath.Join(home, "Downloads")
	writeFile(t, filepath.Join(dl, "a.txt"), "x")
	writeFile(t, filepath.Join(dl, "d", "f.txt"), "x")
	mark := ctimeMark(t)
	cutoffAt(t, mark)

	dryRun = true
	defer func() { dryRun = false }()

	res := discoverDownloadStep(t).run(context.Background())
	if res.err != nil {
		t.Fatalf("dry run failed: %v", res.err)
	}
	if !exists(filepath.Join(dl, "a.txt")) || !exists(filepath.Join(dl, "d", "f.txt")) {
		t.Fatal("a dry run deleted something")
	}
	if !strings.Contains(res.output, "[dry-run] would delete: a.txt") ||
		!strings.Contains(res.output, "[dry-run] would delete: d") {
		t.Fatalf("output = %q, want each entry reported", res.output)
	}
}

// TestDownloadsThroughTheUI: discovery → the named confirm → "y" runs the
// in-process delete → summary, with what was deleted on record in
// cleanup.log.
func TestDownloadsThroughTheUI(t *testing.T) {
	home := isolateDownloads(t)
	oldLog := logDir
	logDir = t.TempDir()
	t.Cleanup(func() { logDir = oldLog })
	dl := filepath.Join(home, "Downloads")
	writeFile(t, filepath.Join(dl, "old.iso"), "x")
	cutoffAt(t, ctimeMark(t))

	m := update(initialModel(), discoverDownloads()())
	if m.screen != screenConfirm || !strings.Contains(m.confirmMsg, "old.iso") {
		t.Fatalf("screen=%d confirm=%q, want the confirm naming old.iso", m.screen, m.confirmMsg)
	}
	next, cmd := m.Update(key("y"))
	m = next.(model)
	if cmd == nil {
		t.Fatal("confirming should launch the delete step")
	}
	msg, ok := cmd().(stepResultMsg)
	if !ok {
		t.Fatalf("delete Cmd returned %T, want stepResultMsg", msg)
	}
	m = update(m, msg)
	if m.screen != screenSummary || m.errs != 0 {
		t.Fatalf("screen=%d errs=%d, want a clean summary", m.screen, m.errs)
	}
	if exists(filepath.Join(dl, "old.iso")) {
		t.Fatal("old.iso survived")
	}
	log, _ := os.ReadFile(logPath(logCategoryCleanup))
	if !strings.Contains(string(log), "OK: Old downloads deleted") || !strings.Contains(string(log), "deleted: old.iso") {
		t.Fatalf("cleanup.log should record the deletion:\n%s", log)
	}
}

// TestDownloadsConfirmListsEntries: the confirm names the first ten entries
// and counts the rest; a name that could forge a line or carry an escape is
// shown quoted.
func TestDownloadsConfirmListsEntries(t *testing.T) {
	var names []string
	for i := range 13 {
		names = append(names, fmt.Sprintf("f%02d", i))
	}
	got := downloadsConfirm("~/Downloads", names)
	if !strings.Contains(got, "Delete 13 item(s) older than 30 days in ~/Downloads?") ||
		!strings.Contains(got, "f09") || strings.Contains(got, "f10") ||
		!strings.Contains(got, "…and 3 more") {
		t.Fatalf("confirm = %q", got)
	}

	evil := downloadsConfirm("~/Downloads", []string{"a\nb", "x\x1b]52;c;Zm9v\x07"})
	if strings.ContainsAny(strings.SplitN(evil, "\n\n", 2)[1], "\x1b\x07") ||
		strings.Count(evil, "\n") != 3 {
		t.Fatalf("unsafe name rendered raw: %q", evil)
	}
}

// TestParseArgsDownloads: --downloads takes its folder as the next argument
// or after "=", and refuses to be given none.
func TestParseArgsDownloads(t *testing.T) {
	orig, origDry := downloadsFlag, dryRun
	defer func() { downloadsFlag, dryRun = orig, origDry }()

	for _, tc := range []struct {
		args    []string
		want    string
		wantErr bool
	}{
		{args: []string{"--downloads", "/data/dl"}, want: "/data/dl"},
		{args: []string{"--dryrun", "--downloads=/data/dl"}, want: "/data/dl"},
		{args: []string{"--downloads"}, wantErr: true},
		{args: []string{"--downloads="}, wantErr: true},
		{args: []string{"--dryrun"}, want: ""},
	} {
		downloadsFlag = ""
		err := parseArgs(tc.args)
		if (err != nil) != tc.wantErr || (!tc.wantErr && downloadsFlag != tc.want) {
			t.Fatalf("parseArgs(%q): flag=%q err=%v; want flag=%q err=%v",
				tc.args, downloadsFlag, err, tc.want, tc.wantErr)
		}
	}
}
