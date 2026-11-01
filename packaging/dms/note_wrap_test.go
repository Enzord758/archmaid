package dms

import (
	"os"
	"strings"
	"testing"
)

// TestTextElides: draft A's rule, text elides and never wraps mid-phrase.
// Every StyledText that shows what the state files say (the hero's lines, the
// rows, the list) elides; the one text allowed to wrap is the launch error,
// which is our own sentence and can be long. Like TestWidgetTextIsPlain this
// reads the source: the widget can't be loaded outside DMS.
func TestTextElides(t *testing.T) {
	b, err := os.ReadFile("archmaid/ArchmaidWidget.qml")
	if err != nil {
		t.Fatal(err)
	}
	data := []string{"text: root.view.", "text: sr.row.", "text: pkgRow.", "text: attRow.modelData", "text: pop.upList", "text: secBox.section", `text: pop.upList ?`}
	n := 0
	for _, blk := range strings.Split(string(b), "StyledText {")[1:] {
		// The block's own properties: up to its first nested '{' or its '}'.
		end := strings.IndexAny(blk, "{}")
		if end < 0 {
			end = len(blk)
		}
		blk = blk[:end]
		if strings.Contains(blk, "wrapMode: Text.WordWrap") && !strings.Contains(blk, "text: root.launchError") {
			t.Errorf("a StyledText wraps and is not the launch error:%s", blk)
		}
		for _, d := range data {
			if strings.Contains(blk, d) {
				n++
				if !strings.Contains(blk, "elide: Text.ElideRight") {
					t.Errorf("a StyledText showing %q does not elide:%s", d, blk)
				}
				break
			}
		}
	}
	// The Kicker component's own uses are checked through the same rule.
	if n < 8 {
		t.Errorf("only %d data texts found: the test reads the wrong shape", n)
	}
}
