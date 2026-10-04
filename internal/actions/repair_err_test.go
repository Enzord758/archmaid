package actions

import (
	"context"
	"errors"
	"testing"
)

// A Repair scan that times out must not claim the audit's "no answer within
// 20s" wording.
func TestRepairErrTimeoutIsNotTheAuditMessage(t *testing.T) {
	if got := clipErr(context.DeadlineExceeded); got != "timed out" {
		t.Fatalf("clipErr(deadline) = %q", got)
	}
	if got := clipErr(errors.New("boom")); got != "boom" {
		t.Fatalf("clipErr(other) = %q", got)
	}
}
