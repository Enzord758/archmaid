package dms

import (
	"os"
	"strings"
	"testing"
)

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
