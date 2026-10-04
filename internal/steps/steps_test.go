package steps

import (
	"errors"
	"slices"
	"testing"

	"github.com/Enzord758/archmaid/internal/logstore"
)

func TestLogCategoryDefaultsToCleanup(t *testing.T) {
	if got := (Step{}).LogCategory(); got != logstore.Cleanup {
		t.Fatalf("zero Step.LogCategory() = %q, want %q", got, logstore.Cleanup)
	}
	if got := (Step{Category: logstore.Orphans}).LogCategory(); got != logstore.Orphans {
		t.Fatalf("LogCategory() = %q, want %q", got, logstore.Orphans)
	}
}

func TestCommandAndArgs(t *testing.T) {
	s := Step{Argv: []string{"sudo", "paccache", "-r"}}
	if s.Command() != "sudo" || !slices.Equal(s.Args(), []string{"paccache", "-r"}) {
		t.Fatalf("Command()=%q Args()=%q", s.Command(), s.Args())
	}
	if (Step{}).Command() != "" || (Step{}).Args() != nil {
		t.Fatal("an in-process step has no command line")
	}
	if one := (Step{Argv: []string{"true"}}); one.Command() != "true" || len(one.Args()) != 0 {
		t.Fatalf("Command()=%q Args()=%q", one.Command(), one.Args())
	}
}

// TestCheckForbidsConfirmAndInsert: a result may gate the next step or insert
// one, never both — the confirm would land on the inserted step.
func TestCheckForbidsConfirmAndInsert(t *testing.T) {
	both := Result{ConfirmNext: "sure?", InsertNext: &Step{ID: IDOrphansRemove}}
	if err := both.Check(); !errors.Is(err, ErrConfirmAndInsert) {
		t.Fatalf("Check() = %v, want ErrConfirmAndInsert", err)
	}
	failed := both
	failed.Err = errors.New("boom")
	if err := failed.Check(); !errors.Is(err, ErrConfirmAndInsert) {
		t.Fatalf("Check() on a failed result = %v, want ErrConfirmAndInsert", err)
	}
	for _, ok := range []Result{{}, {ConfirmNext: "sure?"}, {InsertNext: &Step{}}, {Err: errors.New("x")}} {
		if err := ok.Check(); err != nil {
			t.Fatalf("Check(%+v) = %v, want nil", ok, err)
		}
	}
}
