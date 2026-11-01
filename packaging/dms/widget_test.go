package dms

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestPopoutReloadsOnOpen: opening the popout re-reads the state files, so
// it never shows what the 60 s timer last saw (the VM run: a run's result
// appeared up to a minute late, even after reopening). DMS loads the
// popout's content once, so the reload hangs on the popout's
// shouldBeVisible, not only on Component.onCompleted; the timer stays for
// the bar pill. Like TestWidgetTextIsPlain, this reads the source: the
// widget can't be loaded outside DMS.
func TestPopoutReloadsOnOpen(t *testing.T) {
	b, err := os.ReadFile("archmaid/ArchmaidWidget.qml")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	i := strings.Index(src, "popoutContent:")
	if i < 0 {
		t.Fatal("no popoutContent in the widget")
	}
	pop := src[i:]
	conn := strings.Index(pop, "target: pop.parentPopout")
	if conn < 0 {
		t.Fatal("the popout has no Connections on its parentPopout")
	}
	handler := pop[conn:]
	if end := strings.Index(handler, "\n            }\n"); end > 0 {
		handler = handler[:end]
	}
	for _, want := range []string{"function onShouldBeVisibleChanged()", "parentPopout.shouldBeVisible", "root.reloadAll()"} {
		if !strings.Contains(handler, want) {
			t.Errorf("the popout's open handler lacks %q:\n%s", want, handler)
		}
	}
	for _, want := range []string{"interval: 60000", "onTriggered: root.reloadAll()"} {
		if !strings.Contains(src[:i], want) {
			t.Errorf("the bar's 60 s reload timer lacks %q", want)
		}
	}
}

// TestWidgetTextIsPlain: every text item of the widget is plain text. The
// strings come from the state files (package names, audit reasons, the
// last run's action), and a Text's default AutoText renders anything that
// looks like markup as rich text: tags, links, even remote images. The
// widget itself can't be loaded under qmltestrunner (it imports DMS's own
// modules), so this reads its source: each StyledText block must set
// textFormat to Text.PlainText.
func TestWidgetTextIsPlain(t *testing.T) {
	src, err := os.ReadFile("archmaid/ArchmaidWidget.qml")
	if err != nil {
		t.Fatal(err)
	}
	blocks := strings.Split(string(src), "StyledText {")[1:]
	if len(blocks) == 0 {
		t.Fatal("no StyledText in the widget: the test reads the wrong file")
	}
	for i, b := range blocks {
		// The block's own properties: up to its first nested '{' or its '}'.
		end := strings.IndexAny(b, "{}")
		if end < 0 {
			end = len(b)
		}
		if !strings.Contains(b[:end], "textFormat: Text.PlainText") {
			t.Errorf("StyledText #%d has no textFormat: Text.PlainText:%s", i+1, b[:end])
		}
	}
}

// TestWidgetFilesSurviveAtomicReplace: the writers replace the state files by
// rename (archmaid-updates-check: mktemp + mv), so a FileView that failed once
// or watches the old inode kept the popout on "Not checked yet" though a fresh
// updates-cache.json was there (owner's checklist, 2026-10-11). Every state
// file's view must re-resolve its path on each reload, follow changes, and
// retry a failed read. Like the tests above this reads the source: the widget
// can't be loaded outside DMS.
func TestWidgetFilesSurviveAtomicReplace(t *testing.T) {
	b, err := os.ReadFile("archmaid/ArchmaidWidget.qml")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, id := range []string{"lastRunFile", "auditFile", "updatesFile"} {
		i := strings.Index(src, "id: "+id+"\n")
		if i < 0 {
			t.Fatalf("no FileView %s", id)
		}
		blk := src[i:]
		if end := strings.Index(blk, "\n    }\n"); end > 0 {
			blk = blk[:end]
		}
		for _, want := range []string{"path: root.filesOn ?", "watchChanges: true", "onFileChanged: reload()", "root.loadFailedRetry()"} {
			if !strings.Contains(blk, want) {
				t.Errorf("%s lacks %q:\n%s", id, want, blk)
			}
		}
		// A reload's path flip fires onLoadFailed with path "": the guard
		// must come first, or the flip itself counts as a failed read.
		lf := strings.Index(blk, "onLoadFailed: {")
		if lf < 0 {
			t.Errorf("%s has no onLoadFailed block", id)
			continue
		}
		fb := blk[lf:]
		g := strings.Index(fb, `if (path === "")`)
		r := strings.Index(fb, "return;")
		h := strings.Index(fb, "root.loadFailedRetry()")
		if g < 0 || r < g || h < r {
			t.Errorf(`%s: onLoadFailed needs the 'if (path === "") return;' guard before loadFailedRetry:\n%s`, id, fb)
		}
	}
	rl := src[strings.Index(src, "function reloadFiles()"):]
	rl = rl[:strings.Index(rl, "\n    }\n")]
	for _, want := range []string{"filesOn = false", "filesOn = true", "updatesFile.reload()"} {
		if !strings.Contains(rl, want) {
			t.Errorf("reloadFiles lacks %q", want)
		}
	}
	// Order: off, on, then the explicit reloads (a reload before the flip
	// reads the old inode).
	off, on := strings.Index(rl, "filesOn = false"), strings.Index(rl, "filesOn = true")
	if off < 0 || on < off {
		t.Error("reloadFiles must set filesOn = false before filesOn = true")
	}
	for _, f := range []string{"lastRunFile", "auditFile", "updatesFile"} {
		if i := strings.Index(rl, f+".reload()"); i < on || on < 0 {
			t.Errorf("reloadFiles must call %s.reload() after filesOn = true", f)
		}
	}
	ra := src[strings.Index(src, "function reloadAll()"):]
	ra = ra[:strings.Index(ra, "\n    }\n")]
	rr, rf := strings.Index(ra, "retries = 0"), strings.Index(ra, "reloadFiles()")
	if rr < 0 || rf < rr {
		t.Error("reloadAll must reset retries = 0 before reloadFiles()")
	}
	if !strings.Contains(src, "id: retryTimer") {
		t.Error("no retry timer for a failed read")
	}
}

// TestWidgetRetriesUntilFileAppears: F1 of VM batch 4. The updates timer's
// first write creates updates-cache.json AND its directory after DMS started;
// a FileView cannot watch a missing directory, and the old budget (two tries,
// each of the three failing views counting one) was spent within ~4 s. The
// retry must count one try per round, keep going at <= 4 s steps for most of
// the 60 s cycle, and stay bounded. (Source check only. Worst case: 12 tries,
// the first two 2 s apart and the rest 4 s, cover ~44 s after a tick; a file
// created 45-60 s after it shows at the next tick, <= ~16 s later. Not "always
// within 4 s": that holds only while tries remain.)
func TestWidgetRetriesUntilFileAppears(t *testing.T) {
	b, err := os.ReadFile("archmaid/ArchmaidWidget.qml")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	i := strings.Index(src, "function loadFailedRetry()")
	if i < 0 {
		t.Fatal("no loadFailedRetry")
	}
	fn := src[i:]
	fn = fn[:strings.Index(fn, "\n    }\n")]
	m := regexp.MustCompile(`retries < (\d+)`).FindStringSubmatch(fn)
	if m == nil {
		t.Fatalf("no retry bound in:\n%s", fn)
	}
	n, _ := strconv.Atoi(m[1])
	iv := regexp.MustCompile(`: (\d+)`).FindAllStringSubmatch(fn, -1)
	maxIv := 0
	for _, x := range iv {
		v, _ := strconv.Atoi(x[1])
		if v > maxIv {
			maxIv = v
		}
	}
	if maxIv == 0 || maxIv > 4000 {
		t.Errorf("retry step %d ms: a created file must show at the next try, <= 4 s while tries remain", maxIv)
	}
	if n*maxIv < 40000 {
		t.Errorf("%d retries of <= %d ms cover under 40 s of the 60 s cycle; a file created later is missed", n, maxIv)
	}
	if n > 20 {
		t.Errorf("%d retries per cycle is polling, not a bounded retry", n)
	}
	if !strings.Contains(fn, "!retryTimer.running") {
		t.Error("three failing views must count as one try (guard on retryTimer.running)")
	}
}

// TestWidgetSummaryLines: under the hero the popout shows the run report's
// numbers line (state.js's summaryLine), hidden when empty (an older
// last-run.json has no summary), as plain text, elided. The attention items
// (warnLine's content) are the hero's lines, so there is no second warning
// text. Like the tests above this reads the source: the widget can't be
// loaded outside DMS.
func TestWidgetSummaryLines(t *testing.T) {
	b, err := os.ReadFile("archmaid/ArchmaidWidget.qml")
	if err != nil {
		t.Fatal(err)
	}
	var block string
	for _, blk := range strings.Split(string(b), "StyledText {")[1:] {
		if end := strings.Index(blk, "}"); end >= 0 && strings.Contains(blk[:end], "text: root.view.lastRun ? root.view.lastRun.summaryLine :") {
			block = blk[:end]
		}
	}
	if block == "" {
		t.Fatal("no StyledText shows lastRun.summaryLine")
	}
	for _, want := range []string{"textFormat: Text.PlainText", `root.view.lastRun.summaryLine !== ""`, "elide: Text.ElideRight", "color: Theme.surfaceText"} {
		if !strings.Contains(block, want) {
			t.Errorf("the summaryLine text lacks %q:%s", want, block)
		}
	}
	if strings.Contains(string(b), "warnLine") {
		t.Error("the widget shows warnLine: the attention items are the hero's lines")
	}
}

// TestWidgetUpdatesList: the Updates row unfolds the list only when there is
// one: a chevron and a toggle only for a non-empty list, a ListView capped at
// 8 rows with sticky section headers, the footer for what the cap cut, plain
// text in the delegates, no clickable row inside the list, and the open state
// reset whenever the popout closes.
func TestWidgetUpdatesList(t *testing.T) {
	b, err := os.ReadFile("archmaid/ArchmaidWidget.qml")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, want := range []string{
		"expandable: pop.upList !== null",
		"readonly property bool listOpen: updatesOpen && upList !== null",
		"height: Math.min(contentHeight, 8 * rowH)",
		"section.property: \"section\"",
		"ViewSection.CurrentLabelAtStart",
		`"and " + pop.upList.more + " more not shown"`,
		"pop.updatesOpen = false",
		"Qt.Key_Space",
		"Qt.Key_Return",
		"Theme.isLightMode",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("the widget lacks %q", want)
		}
	}
	// Every state colour is one of the fixed Tokyo Night constants.
	for _, c := range []string{"#9ece6a", "#e0af68", "#f7768e"} {
		if !strings.Contains(src, c) {
			t.Errorf("state colour %s missing", c)
		}
	}
	// The reset sits in the handler that sees the popout hide.
	h := src[strings.Index(src, "function onShouldBeVisibleChanged()"):]
	h = h[:strings.Index(h, "\n            }\n")]
	if !strings.Contains(h, "} else {") || !strings.Contains(h, "pop.updatesOpen = false") {
		t.Errorf("the close handler must reset updatesOpen:\n%s", h)
	}
	// Read-only: the list's delegate has no MouseArea, and the only MouseArea
	// in the popout belongs to the expandable row.
	l := src[strings.Index(src, "id: pkgView"):strings.Index(src, `"and " + pop.upList.more`)]
	if strings.Contains(l, "MouseArea") || strings.Contains(l, "onClicked") || strings.Contains(l, "TapHandler") {
		t.Error("a list row must not be clickable")
	}
	if n := strings.Count(src, "MouseArea {"); n != 1 {
		t.Errorf("%d MouseAreas in the widget, want the one of the Updates row", n)
	}
}
