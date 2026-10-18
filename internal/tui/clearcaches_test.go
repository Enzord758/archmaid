package tui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
)

func cachesPickEvent(disabled bool) engine.NeedsSelection {
	pkg := actions.PickRow{Label: "Entire package cache — 7.3 GiB (1398 files)",
		Note: "Can't reinstall or downgrade those packages without internet"}
	if disabled {
		pkg = actions.PickRow{Label: "Entire package cache", Note: "Empty: nothing to delete", Disabled: true}
	}
	return engine.NeedsSelection{ID: 7, Title: "Clear caches — tick what to delete", Go: "clear",
		Items: []string{actions.PickUserCache, actions.PickPkgCache},
		Rows:  []actions.PickRow{{Label: "~/.cache — 2.3 GiB"}, pkg}}
}

// TestClearCachesPickView: the pick lists both rows with nothing ticked,
// the sizes, and the package-cache warning on screen; the key hint names
// the verb.
func TestClearCachesPickView(t *testing.T) {
	m, _ := engineModel()
	m = feed(m, cachesPickEvent(false))
	if m.screen != screenServices {
		t.Fatalf("screen %d", m.screen)
	}
	v := m.View()
	for _, want := range []string{"Clear caches — tick what to delete:", "[ ] ~/.cache — 2.3 GiB",
		"[ ] Entire package cache — 7.3 GiB (1398 files)",
		"Can't reinstall or downgrade those packages without internet", "enter clear"} {
		if !strings.Contains(v, want) {
			t.Errorf("the pick lacks %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "[x]") {
		t.Errorf("a row is ticked by default:\n%s", v)
	}
}

// TestClearCachesPickSendsTheTicked: space ticks, enter sends the ticked
// keys; with nothing ticked it sends an empty pick (which runs nothing).
func TestClearCachesPickSendsTheTicked(t *testing.T) {
	m, f := engineModel()
	m = feed(m, cachesPickEvent(false))
	m = update(m, key("enter"))
	if got := lastSent(t, f); !reflect.DeepEqual(got, engine.Select{Prompt: 7}) {
		t.Errorf("nothing ticked sent %#v", got)
	}
	m = feed(m, cachesPickEvent(false))
	m = update(m, key("down"))
	m = update(m, key(" "))
	m = update(m, key("enter"))
	if got := lastSent(t, f); !reflect.DeepEqual(got, engine.Select{Prompt: 7, Picked: []string{actions.PickPkgCache}}) {
		t.Errorf("sent %#v", got)
	}
}

// TestClearCachesDisabledRowCannotBeTicked: a disabled row shows its reason,
// has no box, and space on it ticks nothing.
func TestClearCachesDisabledRowCannotBeTicked(t *testing.T) {
	m, f := engineModel()
	m = feed(m, cachesPickEvent(true))
	v := m.View()
	if !strings.Contains(v, "[-] Entire package cache") || !strings.Contains(v, "Empty: nothing to delete") {
		t.Errorf("the disabled row:\n%s", v)
	}
	m = update(m, key("down"))
	m = update(m, key(" "))
	if m.serviceOn[1] {
		t.Error("space ticked a disabled row")
	}
	m = update(m, key("enter"))
	if got := lastSent(t, f); !reflect.DeepEqual(got, engine.Select{Prompt: 7}) {
		t.Errorf("sent %#v", got)
	}
}

// TestServicesPickKeepsItsWording: the services pick, which sends no rows,
// title or verb, reads as it always did.
func TestServicesPickKeepsItsWording(t *testing.T) {
	m, _ := engineModel()
	m = feed(m, engine.NeedsSelection{ID: 1, Items: []string{"a.service"}})
	v := m.View()
	if !strings.Contains(v, "Enabled but not running — select services to disable:") || !strings.Contains(v, "enter disable") {
		t.Errorf("services pick:\n%s", v)
	}
}
