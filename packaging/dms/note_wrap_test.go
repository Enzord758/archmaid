package dms

import (
	"os"
	"strings"
	"testing"
)

// TestNotesWrap: the UPDATES and AUR notes sit in a Row next to their big
// number and are long ("N repo · M AUR (K held, <72h) · checked …"): each
// must wrap inside the width the number leaves, or the popout clips it at
// its right edge (the VM round). Like TestWidgetTextIsPlain this reads the
// source: the widget can't be loaded outside DMS.
func TestNotesWrap(t *testing.T) {
	b, err := os.ReadFile("archmaid/ArchmaidWidget.qml")
	if err != nil {
		t.Fatal(err)
	}
	for _, note := range []string{"root.view.updates.note", "root.view.aur.note"} {
		var block string
		for _, blk := range strings.Split(string(b), "StyledText {")[1:] {
			if end := strings.Index(blk, "}"); end >= 0 && strings.Contains(blk[:end], "text: "+note+"\n") {
				block = blk[:end]
			}
		}
		if block == "" {
			t.Fatalf("no StyledText shows %s", note)
		}
		for _, want := range []string{"wrapMode: Text.WordWrap", "width: parent.width - "} {
			if !strings.Contains(block, want) {
				t.Errorf("the %s text lacks %q:%s", note, want, block)
			}
		}
	}
}
