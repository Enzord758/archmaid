package helperclient

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/steps"
)

const startPlanProtected = `{"v":1,"t":"start","verb":"plan-protected","version":"0.1.0"}`

func protectedVia(t *testing.T, body string) ([]string, string, error) {
	t.Helper()
	helper, argv := fakeHelper(t, "cat <<'EOF'\n"+body+"\nEOF")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	got, err := protectedNames(ctx, helper)
	return got, argv, err
}

func TestProtected(t *testing.T) {
	got, argv, err := protectedVia(t, startPlanProtected+"\n"+`{"v":1,"t":"protected","names":["glibc","linux-cachyos","sudo"]}`+"\n"+resultLine(0, ""))
	if err != nil || !slices.Equal(got, []string{"glibc", "linux-cachyos", "sudo"}) {
		t.Fatalf("got %q, err %v", got, err)
	}
	if a := readArgv(t, argv); !slices.Equal(a, []string{"plan-protected"}) {
		t.Errorf("helper argv = %q", a)
	}
	// an empty set is a valid answer
	if got, _, err := protectedVia(t, startPlanProtected+"\n"+`{"v":1,"t":"protected","names":[]}`+"\n"+resultLine(0, "")); err != nil || len(got) != 0 {
		t.Errorf("empty: %q, %v", got, err)
	}
}

// TestProtectedFailsClosed: anything but exactly one valid event and a good
// result is an error, never a partial list.
func TestProtectedFailsClosed(t *testing.T) {
	ev := `{"v":1,"t":"protected","names":["sudo"]}`
	for name, body := range map[string]string{
		"no event":         startPlanProtected + "\n" + resultLine(0, ""),
		"no result":        startPlanProtected + "\n" + ev,
		"two events":       startPlanProtected + "\n" + ev + "\n" + ev + "\n" + resultLine(0, ""),
		"no names":         startPlanProtected + "\n" + `{"v":1,"t":"protected"}` + "\n" + resultLine(0, ""),
		"null names":       startPlanProtected + "\n" + `{"v":1,"t":"protected","names":null}` + "\n" + resultLine(0, ""),
		"a flag":           startPlanProtected + "\n" + `{"v":1,"t":"protected","names":["--root=/"]}` + "\n" + resultLine(0, ""),
		"a name twice":     startPlanProtected + "\n" + `{"v":1,"t":"protected","names":["a","a"]}` + "\n" + resultLine(0, ""),
		"the helper fails": startPlanProtected + "\n" + ev + "\n" + resultLine(1, `"error":"pacman -Qqo: boom"`),
		"wrong verb":       `{"v":1,"t":"start","verb":"plan-orphans","version":"0.1.0"}` + "\n" + ev + "\n" + resultLine(0, ""),
	} {
		t.Run(name, func(t *testing.T) {
			if got, _, err := protectedVia(t, body); err == nil || got != nil {
				t.Errorf("got %q, err %v", got, err)
			}
		})
	}
}

// A protected event from any other verb is a protocol error.
func TestProtectedEventFromAnotherVerb(t *testing.T) {
	d := newDecoder(steps.Step{Verb: steps.VerbPlanOrphans})
	d.feed([]byte(`{"v":1,"t":"start","verb":"plan-orphans","version":"0.1.0"}`))
	d.feed([]byte(`{"v":1,"t":"protected","names":["sudo"]}`))
	if !errors.Is(d.err, ErrProtocol) || !strings.Contains(d.err.Error(), "protected event from plan-orphans") {
		t.Errorf("err = %v", d.err)
	}
}

// plan-protected runs as the user: Argv, which builds the pkexec call, has
// no polkit action for it.
func TestProtectedHasNoPolkitAction(t *testing.T) {
	if _, err := Argv(steps.Step{Verb: steps.VerbPlanProtected}); !errors.Is(err, ErrNoAction) {
		t.Errorf("Argv(plan-protected) = %v, want ErrNoAction", err)
	}
}
