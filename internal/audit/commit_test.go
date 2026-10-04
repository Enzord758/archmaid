package audit

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"testing"
)

// ── only a completed audit commits ───────────────────────────────────────────
//
// Commit writes the snapshot that every later audit is diffed against, from
// the packages the audit saw. A failed or cancelled audit saw none: its
// commit would write an empty snapshot, erasing every package's history, and
// the next audit would take whatever it finds — a takeover included — as the
// new baseline. So a Result that isn't a completed Run's has no commit,
// whichever way it was obtained.

// requireNoCommit fails unless res has no commit (with any arguments) and the
// snapshot at path still holds want. Should a commit exist, it is run first,
// as a caller would, so the damage shows.
func requireNoCommit(t *testing.T, what string, res Result, path string, want []byte) {
	t.Helper()
	for _, args := range [][2]bool{{false, false}, {false, true}, {true, false}, {true, true}} {
		if save := res.Commit(args[0], args[1]); save != nil {
			_ = save()
			t.Errorf("%s: Commit(%v, %v) is not nil", what, args[0], args[1])
		}
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("%s: snapshot changed (err %v):\n%s\nwant:\n%s", what, err, got, want)
	}
}

// seeded is a config whose snapshot already holds a baseline, and that
// baseline's bytes.
func seeded(t *testing.T, rpcURL string, pkgs ...string) (Config, []byte) {
	t.Helper()
	cfg, _ := config(t, rpcURL, pkgs...)
	seed(t, cfg.SnapshotPath, map[string]string{"victim": "alice", "other": "bob"})
	b, err := os.ReadFile(cfg.SnapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	return cfg, b
}

// TestFailedAuditNeverCommits: the lister failing, the AUR failing, a
// cancelled Run, a cancelled prefetch, an aborted Wait and a hand-built
// Result all come without a commit, and the snapshot is left as it was.
func TestFailedAuditNeverCommits(t *testing.T) {
	url, _, _ := fakeAUR(t, map[string]Info{"victim": {Name: "victim", Maintainer: strp("mallory"), LastModified: aged(24 * 90), NumVotes: 3}})

	t.Run("Foreign fails", func(t *testing.T) {
		cfg, want := seeded(t, url, "victim")
		cfg.Foreign = func(context.Context) ([]string, error) { return nil, errors.New("pacman exploded") }
		res, err := Run(context.Background(), cfg, now)
		if err == nil {
			t.Fatal("want the lister's error")
		}
		requireNoCommit(t, "failed lister", res, cfg.SnapshotPath, want)
	})

	t.Run("no foreign packages", func(t *testing.T) {
		cfg, want := seeded(t, url)
		res, err := Run(context.Background(), cfg, now)
		if !errors.Is(err, ErrNoForeign) {
			t.Fatalf("err = %v, want ErrNoForeign", err)
		}
		requireNoCommit(t, "nothing to audit", res, cfg.SnapshotPath, want)
	})

	t.Run("AUR fails", func(t *testing.T) {
		down := serve(t, func(w http.ResponseWriter, r *http.Request) { http.Error(w, "down", http.StatusBadGateway) })
		cfg, want := seeded(t, down, "victim")
		res, err := Run(context.Background(), cfg, now)
		if err == nil {
			t.Fatal("want the AUR's error")
		}
		requireNoCommit(t, "failed lookup", res, cfg.SnapshotPath, want)
	})

	t.Run("Run cancelled", func(t *testing.T) {
		cfg, want := seeded(t, url, "victim")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		res, err := Run(ctx, cfg, now)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		requireNoCommit(t, "cancelled run", res, cfg.SnapshotPath, want)
	})

	t.Run("prefetch cancelled", func(t *testing.T) {
		hanging, arrived := hangingAUR(t)
		cfg, want := seeded(t, hanging, "victim")
		f := Prefetch(cfg)
		waitArrived(t, arrived)
		f.Cancel()
		res, err := f.Wait(context.Background())
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		requireNoCommit(t, "cancelled prefetch", res, cfg.SnapshotPath, want)
	})

	t.Run("prefetch cancelled after it completed", func(t *testing.T) {
		cfg, want := seeded(t, url, "victim")
		f := Prefetch(cfg)
		<-f.Done()
		f.Cancel() // the run was abandoned: its result must not be committed
		res, err := f.Wait(context.Background())
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		requireNoCommit(t, "abandoned prefetch", res, cfg.SnapshotPath, want)
	})

	t.Run("Wait aborted", func(t *testing.T) {
		hanging, arrived := hangingAUR(t)
		cfg, want := seeded(t, hanging, "victim")
		f := Prefetch(cfg)
		waitArrived(t, arrived)
		ctx, abort := context.WithCancel(context.Background())
		abort()
		res, err := f.Wait(ctx)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		<-f.Done()
		requireNoCommit(t, "aborted wait", res, cfg.SnapshotPath, want)
	})

	t.Run("hand-built Result", func(t *testing.T) {
		cfg, want := seeded(t, url, "victim")
		requireNoCommit(t, "zero Result", Result{}, cfg.SnapshotPath, want)
		requireNoCommit(t, "Result with a config", Result{Total: 1, cfg: cfg}, cfg.SnapshotPath, want)
	})

	t.Run("a completed audit still commits", func(t *testing.T) {
		cfg, want := seeded(t, url, "victim")
		res, err := Run(context.Background(), cfg, now)
		if err != nil {
			t.Fatal(err)
		}
		save := res.Commit(false, true)
		if save == nil {
			t.Fatal("a completed audit must have a commit")
		}
		if err := save(); err != nil {
			t.Fatal(err)
		}
		if got, _ := os.ReadFile(cfg.SnapshotPath); bytes.Equal(got, want) {
			t.Fatal("the accepted commit didn't advance the snapshot")
		}
	})
}

// TestSaveRefusesAnEmptySnapshot: even a Result that claims to be complete
// can't save a snapshot of no packages over the baseline.
func TestSaveRefusesAnEmptySnapshot(t *testing.T) {
	url, _, _ := fakeAUR(t, nil)
	cfg, want := seeded(t, url, "victim")
	for _, holdHigh := range []bool{false, true} {
		r := Result{ok: true, Baseline: BaselineOK, cfg: cfg}
		if err := r.save(holdHigh); !errors.Is(err, errEmptySnapshot) {
			t.Fatalf("save(%v) = %v, want errEmptySnapshot", holdHigh, err)
		}
		if err := r.Commit(holdHigh, false)(); !errors.Is(err, errEmptySnapshot) {
			t.Fatalf("Commit(%v)() = %v, want errEmptySnapshot", holdHigh, err)
		}
	}
	if got, _ := os.ReadFile(cfg.SnapshotPath); !bytes.Equal(got, want) {
		t.Fatalf("snapshot changed:\n%s", got)
	}
}
