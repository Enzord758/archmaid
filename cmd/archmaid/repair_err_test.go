package main

import (
	"context"
	"errors"
	"testing"
)

// A Repair scan that times out must not claim the audit's "no answer within
// 20s" wording.
func TestRepairErrTimeoutIsNotTheAuditMessage(t *testing.T) {
	if got := repairErr(context.DeadlineExceeded); got != "timed out" {
		t.Fatalf("repairErr(deadline) = %q", got)
	}
	if got := repairErr(errors.New("boom")); got != "boom" {
		t.Fatalf("repairErr(other) = %q", got)
	}
}
