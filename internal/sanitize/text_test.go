package sanitize

import (
	"strings"
	"testing"
)

func TestClipTail(t *testing.T) {
	if got := ClipTail("/home/u/.cache/yay/some-package/src/build/bin/helper", 20); got != "…rc/build/bin/helper" {
		t.Fatalf("ClipTail = %q", got)
	}
	if got := ClipTail("/usr/bin/yay", 20); got != "/usr/bin/yay" {
		t.Fatalf("ClipTail shortened a short path: %q", got)
	}
}

func TestOneLineAndClip(t *testing.T) {
	if got := OneLine("a\x1b[31m b\n\t c\r\n"); got != "a b c" {
		t.Fatalf("OneLine = %q", got)
	}
	if got := Clip("abcdef", 4); got != "abc…" {
		t.Fatalf("Clip = %q", got)
	}
	if got := Clip("abcd", 4); got != "abcd" {
		t.Fatalf("Clip short = %q", got)
	}
}

func TestQuoteName(t *testing.T) {
	if got := QuoteName("plain.txt", 60); got != "plain.txt" {
		t.Fatalf("QuoteName = %q", got)
	}
	if got := QuoteName("a\nb", 60); got != `"a\nb"` {
		t.Fatalf("QuoteName newline = %q", got)
	}
	if got := QuoteName(strings.Repeat("x", 70), 60); got != strings.Repeat("x", 59)+"…" {
		t.Fatalf("QuoteName long = %q", got)
	}
}

func TestField(t *testing.T) {
	if got := Field("mallory\ralice", 64); got != "malloryalice" {
		t.Fatalf("Field = %q", got)
	}
	if got := Field(strings.Repeat("x", 100), 64); got != strings.Repeat("x", 64)+"…" {
		t.Fatalf("Field long = %q", got)
	}
}
