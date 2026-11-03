package dms

// The widget's state colours and the GUI's (task #250). The GUI's light
// theme darkens its ok, warn and danger tokens until they hold 4.5:1 (WCAG AA,
// small text; gui/internal/ui/lighttags_test.go), and the widget's stateColor
// must use the same values or the two drift apart again. This reads both
// files: ArchmaidWidget.qml's stateColor and style.css's am_ok, am_warn and
// am_danger, the top-level ones for the dark theme and the ones inside the
// light @media block for a light theme.

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestWidgetStateColoursMatchTheGUI(t *testing.T) {
	qml, err := os.ReadFile("archmaid/ArchmaidWidget.qml")
	if err != nil {
		t.Fatal(err)
	}
	css, err := os.ReadFile("../../gui/internal/ui/style.css")
	if err != nil {
		t.Fatal(err)
	}
	src := string(qml)
	i := strings.Index(src, "function stateColor(tone)")
	if i < 0 {
		t.Fatal("the widget has no stateColor")
	}
	fn := src[i:]
	if end := strings.Index(fn, "\n    }\n"); end > 0 {
		fn = fn[:end]
	}
	// case "ok":\n return light ? "#light" : "#dark";
	cases := regexp.MustCompile(`case "(\w+)":\s*return light \? "(#[0-9a-fA-F]{6})" : "(#[0-9a-fA-F]{6})";`).FindAllStringSubmatch(fn, -1)
	widget := map[string][2]string{}
	for _, m := range cases {
		widget[m[1]] = [2]string{strings.ToLower(m[2]), strings.ToLower(m[3])}
	}

	define := regexp.MustCompile(`@define-color am_(ok|warn|danger|running) (#[0-9a-fA-F]{6});`)
	light := regexp.MustCompile(`(?s)@media \(prefers-color-scheme: light\) \{(.*?)\n\}`).FindSubmatch(css)
	if light == nil {
		t.Fatal("style.css has no light @media block")
	}
	gui := map[string][2]string{} // tone -> light, dark
	for _, m := range define.FindAllSubmatch(light[1], -1) {
		v := gui[string(m[1])]
		v[0] = strings.ToLower(string(m[2]))
		gui[string(m[1])] = v
	}
	dark := strings.Replace(string(css), string(light[0]), "", 1)
	for _, m := range define.FindAllStringSubmatch(dark, -1) {
		v := gui[m[1]]
		v[1] = strings.ToLower(m[2])
		gui[m[1]] = v
	}

	for _, tone := range []string{"ok", "warn", "danger"} {
		w, okW := widget[tone]
		g := gui[tone]
		if !okW || g[0] == "" || g[1] == "" {
			t.Errorf("tone %q: widget %v (found %v), GUI %v", tone, w, okW, g)
			continue
		}
		if w[0] != g[0] {
			t.Errorf("tone %q: the widget's light colour is %s, the GUI's am_%s is %s", tone, w[0], tone, g[0])
		}
		if w[1] != g[1] {
			t.Errorf("tone %q: the widget's dark colour is %s, the GUI's am_%s is %s", tone, w[1], tone, g[1])
		}
	}
}
