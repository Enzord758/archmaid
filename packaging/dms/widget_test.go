package dms

import (
	"os"
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
