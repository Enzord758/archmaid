package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/cmd/archmaid-helper/internal/helper"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/helperclient"
	"github.com/Enzord758/archmaid/internal/helperclient/helpertest"
	"github.com/Enzord758/archmaid/internal/steps"
)

// TestFullCleanupTiesToClient: the full-cleanup call the GUI builds from
// Full Cleanup's polkit queue passes the helper's validation, and the
// helper's own stream for it (run in-process as a dev build with a fake
// runner) decodes in helperclient.Run into one PartStarted per later part
// and one PartDone per part but the last, in the queue's order, with a
// clean StepExited. The helper's part order and the client's are one.
func TestFullCleanupTiesToClient(t *testing.T) {
	var parts []steps.Step
	for _, s := range buildAllSteps(t) {
		if s.Combine == steps.VerbFullCleanup && s.Verb != steps.VerbRemoveOrphans {
			parts = append(parts, s)
		}
	}
	call := steps.Step{ID: steps.VerbFullCleanup, Verb: steps.VerbFullCleanup, NeedsPriv: true, Parts: parts}
	argv, err := helperclient.Argv(call)
	if err != nil {
		t.Fatalf("Argv: %v", err)
	}
	var out, errOut bytes.Buffer
	dir := t.TempDir()
	if code := helper.Run(argv[2:], helper.Options{
		Runner: anyRunner{}, Stdout: &out, Stderr: &errOut, Euid: 1000, Dev: true,
		LockPath: filepath.Join(dir, "lock"), PacmanDBLock: filepath.Join(dir, "db.lck"),
	}); code != helper.ExitOK {
		t.Fatalf("helper exit %d: %s", code, errOut.String())
	}

	// A pkexec shim replays that stream (with sh builtins only: PATH is
	// buildAllSteps' shim dir).
	stream := filepath.Join(dir, "stream")
	if err := os.WriteFile(stream, out.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pkexec"), []byte("#!/bin/sh\nwhile IFS= read -r l; do printf '%s\\n' \"$l\"; done < '"+stream+"'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	standIn := filepath.Join(dir, "archmaid-helper")
	if err := os.WriteFile(standIn, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	helpertest.UseHelperFile(t, standIn)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var started, done []string
	var exited *engine.StepExited
	helperclient.Run(ctx, 1, call, func(in engine.Input) {
		switch in := in.(type) {
		case engine.PartStarted:
			started = append(started, in.Step)
		case engine.PartDone:
			if in.Err != nil {
				t.Errorf("%s: %v", in.Step, in.Err)
			}
			done = append(done, in.Step)
		case engine.StepExited:
			exited = &in
		}
	})
	var ids []string
	for _, p := range parts {
		ids = append(ids, p.ID)
	}
	if !slices.Equal(started, ids[1:]) || !slices.Equal(done, ids[:len(ids)-1]) {
		t.Errorf("started %q, done %q; parts %q", started, done, ids)
	}
	if exited == nil || exited.Err != nil || exited.NotGranted {
		t.Errorf("StepExited = %+v", exited)
	}
}
