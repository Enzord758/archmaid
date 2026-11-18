package tui

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/exp/teatest"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/health"
)

// healthFixture is a report with every state, in the order Run returns them
// (by group): two look rows, one with an ignored item, an unknown, an ignored
// check and fine ones.
func healthFixture() health.Report {
	return health.Report{At: time.Date(2026, 11, 18, 14, 3, 0, 0, time.UTC), Checks: []health.Check{
		{ID: health.IDFailedUnits, Title: "Failed services", Group: health.GroupSystem, State: health.Look,
			Detail:       "2 failed: cups.service, bluetooth.service",
			Copy:         "journalctl -b -u cups.service -u bluetooth.service",
			Items:        []health.Item{{Key: "unit:cups.service", Label: "cups.service"}, {Key: "unit:bluetooth.service", Label: "bluetooth.service"}},
			IgnoredItems: []health.Item{{Key: "unit:old.service", Label: "old.service"}}},
		{ID: health.IDTimeSync, Title: "Clock sync", Group: health.GroupSystem, State: health.OK, Detail: "The clock is synchronised"},
		{ID: health.IDPacnew, Title: "Config files to merge", Group: health.GroupPackages, State: health.Look,
			Detail: "1 .pacnew file: /etc/pacman.conf.pacnew", Copy: "sudo pacdiff",
			Items: []health.Item{{Key: "pacnew:/etc/pacman.conf.pacnew", Label: "/etc/pacman.conf.pacnew"}}},
		{ID: health.IDNews, Title: "Arch news", Group: health.GroupPackages, State: health.Unknown, Detail: "Couldn't reach archlinux.org"},
		{ID: health.IDMirror, Title: "Mirror", Group: health.GroupPackages, State: health.Look,
			Detail: "The mirror is 3 days behind", Link: "https://archlinux.org/mirrors/status/",
			Items: []health.Item{{Key: "mirror", Label: "Mirror"}}},
		{ID: health.IDDisk, Title: "Disk space", Group: health.GroupHardware, State: health.OK, Detail: "58 GiB free on /"},
		{ID: health.IDDiskHealth, Title: "Drive health", Group: health.GroupHardware, State: health.Ignored,
			Detail: "A drive reports a warning", IgnoredItems: []health.Item{{Key: "drive:sda", Label: "sda"}}},
	}}
}

// healthModel is a model on the health screen showing rep, at 80x24.
func healthModel(t *testing.T, rep health.Report) model {
	t.Helper()
	m := sizedModel()
	m.screen = screenHealth
	m.health.rep, m.health.have = rep, true
	m.healthLayout()
	return m
}

// healthTmpStore points the ignore list at a scratch folder for the test.
func healthTmpStore(t *testing.T) *health.IgnoreStore {
	t.Helper()
	old := healthStore
	st := &health.IgnoreStore{Dir: t.TempDir()}
	healthStore = st
	t.Cleanup(func() { healthStore = old })
	return st
}

// runCmd runs a command's function (a test has no program to do it).
func runCmd(t *testing.T, c tea.Cmd) tea.Msg {
	t.Helper()
	if c == nil {
		t.Fatal("no command")
	}
	return c()
}

func TestMenuHasHealthBeforeEditConfig(t *testing.T) {
	var ids []string
	for _, a := range menu() {
		ids = append(ids, a.ID)
	}
	n := len(ids)
	if ids[n-1] != actions.IDExit || ids[n-2] != actions.IDViewLog || ids[n-3] != actions.IDEditConfig || ids[n-4] != idHealth {
		t.Fatalf("menu ends %v", ids[max(n-5, 0):])
	}
	// The engine's menu is untouched: the GUI shows it too.
	for _, a := range actions.Menu {
		if a.ID == idHealth {
			t.Fatal("the TUI's entry leaked into actions.Menu")
		}
	}
}

func TestHealthRowsOrder(t *testing.T) {
	rows := healthRows(healthFixture())
	var got []string
	for _, r := range rows {
		s := r.chk.ID
		if r.section != "" {
			s = r.section + ":" + s
		}
		got = append(got, s)
	}
	want := []string{
		"Needs your attention:" + health.IDFailedUnits, health.IDPacnew, health.IDMirror,
		"System:" + health.IDTimeSync,
		"Packages:" + health.IDNews,
		"Hardware:" + health.IDDisk, health.IDDiskHealth,
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("rows\n got %v\nwant %v", got, want)
	}
	// A group Run does not know lands under Other; an empty report has no rows.
	odd := health.Report{Checks: []health.Check{{ID: "x", Title: "X", Group: "weird", State: health.OK}}}
	if r := healthRows(odd); len(r) != 1 || r[0].section != "Other" {
		t.Errorf("unknown group: %+v", r)
	}
	if len(healthRows(health.Report{})) != 0 {
		t.Error("empty report has rows")
	}
}

func TestHealthSummary(t *testing.T) {
	for _, c := range []struct {
		look int
		want string
	}{{0, "Nothing needs attention"}, {1, "1 thing needs attention"}, {3, "3 things need attention"}} {
		var rep health.Report
		for i := 0; i < c.look; i++ {
			rep.Checks = append(rep.Checks, health.Check{State: health.Look})
		}
		if got := healthSummary(rep); got != c.want {
			t.Errorf("%d look: %q, want %q", c.look, got, c.want)
		}
	}
}

func TestHealthGolden(t *testing.T) {
	viewGolden(t, healthModel(t, healthFixture()))
}

// TestHealthSelectedLookRowGolden: the cursor on the mirror row (link, no
// command) with its link printed, and the keys the row has in the hint.
func TestHealthSelectedLookRowGolden(t *testing.T) {
	m := healthModel(t, healthFixture())
	m = update(m, key("down"))
	m = update(m, key("down"))
	m = update(m, key("o"))
	if !strings.Contains(m.health.note, "https://archlinux.org/mirrors/status/") {
		t.Fatalf("note %q", m.health.note)
	}
	viewGolden(t, m)
}

func TestHealthBusyFirstRunGolden(t *testing.T) {
	m := sizedModel()
	m.screen = screenHealth
	m.health.busy = true
	m.healthLayout()
	viewGolden(t, m)
}

// TestHealthFitsTheTerminal: whatever the size, the frame is never taller
// than the terminal, and the selected row is on it after moving down the
// whole list.
func TestHealthFitsTheTerminal(t *testing.T) {
	for _, h := range []int{12, 16, 24, 40} {
		m := healthModel(t, healthFixture())
		m = update(m, tea.WindowSizeMsg{Width: 60, Height: h})
		for i := range healthRows(healthFixture()) {
			if lines := lipgloss.Height(m.View()); lines > h {
				t.Fatalf("height %d, row %d: frame is %d lines", h, i, lines)
			}
			if !strings.Contains(m.View(), "➤") {
				t.Fatalf("height %d, row %d: the selected row is not on screen:\n%s", h, i, m.View())
			}
			m = update(m, key("down"))
		}
	}
}

// TestHealthDetailWraps: a long detail wraps inside the width, never cut.
func TestHealthDetailWraps(t *testing.T) {
	rep := health.Report{Checks: []health.Check{{ID: "a", Title: "A", Group: health.GroupSystem, State: health.Look,
		Detail: strings.Repeat("word ", 30) + "END"}}}
	m := healthModel(t, rep)
	m = update(m, tea.WindowSizeMsg{Width: 50, Height: 24})
	v := m.View()
	if !strings.Contains(v, "END") {
		t.Fatalf("the detail was cut:\n%s", v)
	}
	for _, l := range strings.Split(v, "\n") {
		if lipgloss.Width(l) > 50 {
			t.Fatalf("line wider than the terminal: %q", l)
		}
	}
}

func TestHealthOpenRunsOffTheLoop(t *testing.T) {
	var calls int
	old := healthRun
	healthRun = func(ctx context.Context) health.Report { calls++; return healthFixture() }
	defer func() { healthRun = old }()

	m := sizedModel()
	m.cursor = len(menu()) - 4
	if menu()[m.cursor].ID != idHealth {
		t.Fatalf("menu index drifted: %q", menu()[m.cursor].ID)
	}
	next, cmd := m.Update(key("enter"))
	m = next.(model)
	if m.screen != screenHealth || !m.health.busy || calls != 0 {
		t.Fatalf("screen=%d busy=%v calls=%d: the checks must run in the command, not in Update", m.screen, m.health.busy, calls)
	}
	if v := m.View(); !strings.Contains(v, "Checking the system") {
		t.Fatalf("no spinner line while running:\n%s", v)
	}
	m = update(m, runCmd(t, cmd))
	if calls != 1 || m.health.busy || !m.health.have {
		t.Fatalf("calls=%d busy=%v have=%v after the run", calls, m.health.busy, m.health.have)
	}
	if v := m.View(); !strings.Contains(v, "2 things need attention") && !strings.Contains(v, "3 things need attention") {
		t.Fatalf("no summary:\n%s", v)
	}
	// Opening again with a fresh report runs nothing; an old one runs again.
	m = update(m, key("esc"))
	if m.screen != screenMenu {
		t.Fatalf("esc: screen %d", m.screen)
	}
	m.health.rep.At = time.Now()
	again, cmd := m.openHealthForTest()
	if cmd != nil || again.health.busy {
		t.Fatal("a fresh report ran the checks again")
	}
	again.health.rep.At = time.Now().Add(-time.Hour)
	if _, cmd = again.openHealthForTest(); cmd == nil {
		t.Fatal("an hour-old report was not run again")
	}
}

// openHealthForTest is the menu's open, as a model.
func (m model) openHealthForTest() (model, tea.Cmd) {
	t, cmd := m.openHealth()
	return t.(model), cmd
}

// TestHealthStaleRunIsDropped: leaving the screen while the checks run, and
// opening it again, takes only the newer run's result.
func TestHealthStaleRunIsDropped(t *testing.T) {
	m := sizedModel()
	m, _ = m.startHealth()
	first := m.health.seq
	m.screen = screenHealth
	m = update(m, key("esc"))
	m, _ = m.startHealth()
	m = update(m, healthDoneMsg{seq: first, rep: healthFixture()})
	if m.health.have || !m.health.busy {
		t.Fatalf("a dropped run's result was taken: have=%v busy=%v", m.health.have, m.health.busy)
	}
	m = update(m, healthDoneMsg{seq: m.health.seq, rep: healthFixture()})
	if !m.health.have || m.health.busy {
		t.Fatalf("the current run's result was not taken: have=%v busy=%v", m.health.have, m.health.busy)
	}
}

func TestHealthEscCancelsTheRun(t *testing.T) {
	m := sizedModel()
	m.screen = screenHealth
	m, _ = m.startHealth()
	cancelled := false
	m.health.cancel = func() { cancelled = true }
	m = update(m, key("esc"))
	if !cancelled || m.health.busy || m.screen != screenMenu {
		t.Fatalf("cancelled=%v busy=%v screen=%d", cancelled, m.health.busy, m.screen)
	}
}

func TestHealthCopyPrintsAndSendsOSC52(t *testing.T) {
	var out bytes.Buffer
	old := osc52Out
	osc52Out = &out
	defer func() { osc52Out = old }()

	m := healthModel(t, healthFixture())
	next, cmd := m.Update(key("c"))
	m = next.(model)
	runCmd(t, cmd)
	// base64 of the command, in an OSC 52 clipboard set.
	if !strings.HasPrefix(out.String(), "\x1b]52;c;") || !strings.HasSuffix(out.String(), "\x07") {
		t.Fatalf("escape %q", out.String())
	}
	if !strings.Contains(m.health.note, "journalctl -b -u cups.service -u bluetooth.service") {
		t.Fatalf("the command is not printed: %q", m.health.note)
	}
	if !strings.Contains(m.View(), "journalctl -b -u cups.service") {
		t.Fatalf("the note is not on screen:\n%s", m.View())
	}
	// No command: nothing sent.
	out.Reset()
	m = healthModel(t, healthFixture())
	m.health.cur = 2 // the mirror row: a link, no command
	next, cmd = m.Update(key("c"))
	if cmd != nil || out.Len() != 0 || !strings.Contains(next.(model).health.note, "no command") {
		t.Fatalf("cmd=%v out=%q note=%q", cmd, out.String(), next.(model).health.note)
	}
}

func TestHealthLinkIsPrintedNeverOpened(t *testing.T) {
	m := healthModel(t, healthFixture())
	m = update(m, key("o")) // failed services: no link
	if !strings.Contains(m.health.note, "no link") {
		t.Fatalf("note %q", m.health.note)
	}
	m.health.cur = 2
	next, cmd := m.Update(key("o"))
	if cmd != nil {
		t.Fatal("'o' started something: archmaid must only print the link")
	}
	if !strings.Contains(next.(model).health.note, "https://archlinux.org/mirrors/status/") {
		t.Fatalf("note %q", next.(model).health.note)
	}
}

func TestHealthIgnoreAndUndoWriteTheStore(t *testing.T) {
	st := healthTmpStore(t)
	calls := 0
	old := healthRun
	healthRun = func(context.Context) health.Report { calls++; return healthFixture() }
	defer func() { healthRun = old }()

	m := healthModel(t, healthFixture())
	next, cmd := m.Update(key("i")) // failed services: two active items
	m = next.(model)
	msg := runCmd(t, cmd)
	got := st.Load()
	if _, ok := got["unit:cups.service"]; !ok || len(got) != 2 {
		t.Fatalf("ignore list %v", got)
	}
	next, cmd = m.Update(msg)
	m = next.(model)
	if !strings.Contains(m.health.note, "Ignored 2 items") || !m.health.busy {
		t.Fatalf("note %q busy=%v: the checks should run again", m.health.note, m.health.busy)
	}
	m = update(m, runCmd(t, cmd))
	if calls != 1 {
		t.Fatalf("calls %d", calls)
	}

	// Undo takes the row's ignored items off the list.
	if err := st.Ignore("unit:old.service"); err != nil {
		t.Fatal(err)
	}
	m.health.cur = 0
	next, cmd = m.Update(key("u"))
	m = next.(model)
	msg = runCmd(t, cmd)
	if _, ok := st.Load()["unit:old.service"]; ok {
		t.Fatal("undo left the item on the list")
	}
	m = update(m, msg)
	if !strings.Contains(m.health.note, "No longer ignoring 1 item.") {
		t.Fatalf("note %q", m.health.note)
	}
}

func TestHealthIgnoreNeedsALookRowAndUndoAnIgnoredOne(t *testing.T) {
	m := healthModel(t, healthFixture())
	m.health.cur = 3 // Clock sync: fine
	next, cmd := m.Update(key("i"))
	if cmd != nil || !strings.Contains(next.(model).health.note, "Only a check that needs attention") {
		t.Fatalf("i on an ok row: %v %q", cmd, next.(model).health.note)
	}
	next, cmd = m.Update(key("u"))
	if cmd != nil || !strings.Contains(next.(model).health.note, "Nothing is ignored") {
		t.Fatalf("u on a row with nothing ignored: %v %q", cmd, next.(model).health.note)
	}
}

// TestHealthDryRunLeavesTheIgnoreList: --dryrun changes nothing, and says so.
func TestHealthDryRunLeavesTheIgnoreList(t *testing.T) {
	st := healthTmpStore(t)
	dryRun = true
	defer func() { dryRun = false }()
	m := healthModel(t, healthFixture())
	next, cmd := m.Update(key("i"))
	if cmd != nil || !strings.Contains(next.(model).health.note, "Dry run") || len(st.Load()) != 0 {
		t.Fatalf("cmd=%v note=%q list=%v", cmd, next.(model).health.note, st.Load())
	}
}

func TestHealthIgnoreFailureIsShown(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	old := healthStore
	healthStore = &health.IgnoreStore{Dir: filepath.Join(blocker, "sub")} // a folder under a file
	defer func() { healthStore = old }()

	m := healthModel(t, healthFixture())
	next, cmd := m.Update(key("i"))
	m = update(next.(model), runCmd(t, cmd))
	if !strings.Contains(m.health.note, "Couldn't change the ignore list") || m.health.busy {
		t.Fatalf("note %q busy=%v", m.health.note, m.health.busy)
	}
	m = update(m, healthIgnoredMsg{err: health.ErrIgnoreFull})
	if !strings.Contains(m.health.note, "ignore list is full") {
		t.Fatalf("note %q", m.health.note)
	}
}

func TestHealthRerun(t *testing.T) {
	m := healthModel(t, healthFixture())
	m, cmd := m.startHealthKey(t)
	if cmd == nil || !m.health.busy {
		t.Fatal("r did not start a run")
	}
	// While one runs, r starts no second.
	next, cmd := m.Update(key("r"))
	if cmd != nil || !next.(model).health.busy {
		t.Fatal("r during a run started another")
	}
	if v := m.View(); !strings.Contains(v, "checking…") {
		t.Fatalf("the spinner is not shown over the old report:\n%s", v)
	}
}

func (m model) startHealthKey(t *testing.T) (model, tea.Cmd) {
	next, cmd := m.Update(key("r"))
	return next.(model), cmd
}

// TestHealthKeysMoveAndScroll: ctrl+c quits, up at the top and down at the
// bottom stay, pgdn is passed to the list, an empty report doesn't panic.
func TestHealthKeysMoveAndScroll(t *testing.T) {
	m := healthModel(t, healthFixture())
	m = update(m, key("up"))
	if m.health.cur != 0 {
		t.Fatalf("cur %d", m.health.cur)
	}
	for range 20 {
		m = update(m, key("j"))
	}
	if n := len(healthRows(healthFixture())); m.health.cur != n-1 {
		t.Fatalf("cur %d, want %d", m.health.cur, n-1)
	}
	m = update(m, key("k"))
	m = update(m, tea.KeyMsg{Type: tea.KeyPgDown})
	if _, cmd := m.Update(key("ctrl+c")); cmd == nil {
		t.Fatal("ctrl+c did not quit")
	}
	e := healthModel(t, health.Report{})
	for _, k := range []string{"c", "o", "i", "u", "down", "up"} {
		e = update(e, key(k))
	}
	_ = e.View()
	// A shorter report after a re-run keeps the cursor on a row.
	m.health.cur = 6
	m = update(m, healthDoneMsg{seq: m.health.seq, rep: health.Report{Checks: healthFixture().Checks[:2]}})
	if m.health.cur != 1 {
		t.Fatalf("cur %d after a shorter report (two rows)", m.health.cur)
	}
}

// TestHealthHostileTextIsCleaned: a check's text is another program's
// output: control characters never reach the screen or the clipboard.
func TestHealthHostileTextIsCleaned(t *testing.T) {
	var out bytes.Buffer
	old := osc52Out
	osc52Out = &out
	defer func() { osc52Out = old }()
	rep := health.Report{Checks: []health.Check{{ID: "a", Title: "A\x1b[2J", Group: health.GroupSystem, State: health.Look,
		Detail: "bad \x1b]0;pwned\x07 text", Copy: "echo \x1b[31m hi", Link: "https://x/\x1b[2J",
		Items: []health.Item{{Key: "a", Label: "a\x1b[2J"}}}}}
	m := healthModel(t, rep)
	next, cmd := m.Update(key("c"))
	runCmd(t, cmd)
	m = next.(model)
	m = update(m, key("o"))
	if strings.ContainsRune(m.View(), 0x1b) {
		t.Fatalf("an escape got through to the screen: %q", m.View())
	}
	payload := strings.TrimSuffix(strings.TrimPrefix(out.String(), "\x1b]52;c;"), "\x07")
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil || strings.ContainsRune(string(raw), 0x1b) || !strings.Contains(string(raw), "echo") {
		t.Fatalf("clipboard payload %q (%v)", raw, err)
	}
}

// TestHealthTeatest drives the whole program: down to the entry, enter, the
// rows appear, copy, back.
func TestHealthTeatest(t *testing.T) {
	var out bytes.Buffer
	oldOut, oldRun := osc52Out, healthRun
	osc52Out = &out
	healthRun = func(context.Context) health.Report { return healthFixture() }
	defer func() { osc52Out, healthRun = oldOut, oldRun }()

	tm := teatest.NewTestModel(t, fixedModel(), teatest.WithInitialTermSize(80, 24))
	waitForText(t, tm, "System health")
	for range len(menu()) - 4 {
		tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	}
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitForText(t, tm, "Failed services")
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	waitForText(t, tm, "journalctl -b -u cups.service")
	tm.Send(tea.KeyMsg{Type: tea.KeyEsc})
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	fm := tm.FinalModel(t, teatest.WithFinalTimeout(3*time.Second)).(model)
	if fm.screen != screenMenu {
		t.Fatalf("screen %d, want the menu", fm.screen)
	}
	if !strings.HasPrefix(out.String(), "\x1b]52;c;") {
		t.Fatalf("no OSC 52 reached the terminal: %q", out.String())
	}
}
