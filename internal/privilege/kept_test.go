package privilege

import (
	"context"
	"slices"
	"testing"
	"time"
)

// TestKeptPassword: a kept password is there until Wipe, which zeroes it
// (read back before unmapping) and ends the run's keeping: nothing is kept
// again, not even a fresh password. Fail does the same.
func TestKeptPassword(t *testing.T) {
	for _, end := range []string{"wipe", "fail"} {
		k := NewKeptPassword(time.Hour)
		if !k.Keep([]byte("hunter2")) || !k.Has() {
			t.Fatalf("%s: nothing kept", end)
		}
		if k.Keep([]byte("other")) {
			t.Fatalf("%s: a second password replaced the first", end)
		}
		pw, err := k.Copy()
		if err != nil || string(pw) != "hunter2" {
			t.Fatalf("%s: Copy = %q, %v", end, pw, err)
		}
		clear(pw)
		if again, _ := k.Copy(); string(again) != "hunter2" {
			t.Fatalf("%s: clearing a copy cleared the kept password", end)
		}
		if end == "wipe" {
			k.Wipe()
		} else {
			k.Fail()
		}
		if k.Has() || !k.Wiped() {
			t.Fatalf("%s: still held, or not read back as zero", end)
		}
		if pw, err := k.Copy(); err != ErrNotKept || pw != nil {
			t.Fatalf("%s: Copy after the end = %q, %v", end, pw, err)
		}
		if k.Keep([]byte("hunter2")) {
			t.Fatalf("%s: kept a password after the run's end", end)
		}
	}
}

// TestKeptPasswordCap: past its cap the password wipes itself.
func TestKeptPasswordCap(t *testing.T) {
	k := NewKeptPassword(50 * time.Millisecond)
	if !k.Keep([]byte("hunter2")) {
		t.Fatal("nothing kept")
	}
	deadline := time.Now().Add(5 * time.Second)
	for k.Has() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if k.Has() || !k.Wiped() {
		t.Fatal("still held past the cap")
	}
}

// TestKeptPasswordRefuses: nothing empty, nothing with a line break,
// nothing on a zero KeptPassword.
func TestKeptPasswordRefuses(t *testing.T) {
	k := NewKeptPassword(time.Hour)
	for _, pw := range []string{"", "a\nb", "a\rb"} {
		if k.Keep([]byte(pw)) {
			t.Errorf("kept %q", pw)
		}
	}
	var zero KeptPassword
	if zero.Keep([]byte("hunter2")) {
		t.Error("the zero KeptPassword kept a password")
	}
	var none *KeptPassword
	if _, err := none.Copy(); none.Keep([]byte("x")) || none.Has() || err != ErrNotKept {
		t.Error("a nil KeptPassword held something")
	}
	none.Wipe()
	// A run that ends before anything was kept keeps nothing afterwards.
	ended := NewKeptPassword(time.Hour)
	ended.Wipe()
	if ended.Keep([]byte("hunter2")) || ended.Has() {
		t.Error("kept a password after the run ended with nothing kept")
	}
}

// TestAnswerBytes: as Answer, from bytes: su first, then the password and
// a newline to the helper; nothing written when su refuses.
func TestAnswerBytes(t *testing.T) {
	dir := shimAuth(t, 0, 0, "")
	req, helper := pipeRequest(t)
	got := make(chan string, 1)
	go func() {
		pw, _ := ReadAnswer(helper, nil) // the relay's side, handshake and all
		got <- pw
	}()
	if detail, err := req.AnswerBytes(context.Background(), []byte("secret")); err != nil || detail != "" {
		t.Fatalf("AnswerBytes = %q, %v", detail, err)
	}
	if pw := recv(t, got, "the password"); pw != "secret\n" {
		t.Fatalf("helper received %q", pw)
	}
	requireSuOnly(t, dir, "secret")

	shimAuth(t, 1, 0, "")
	req, helper = pipeRequest(t)
	if _, err := req.AnswerBytes(context.Background(), []byte("wrong")); err == nil {
		t.Fatal("AnswerBytes passed a password su refused")
	}
	requireNothingWritten(t, helper)

	req, _ = unverifiedPipeRequest(t)
	if _, err := req.AnswerBytes(context.Background(), []byte("secret")); err != ErrUnverified {
		t.Fatalf("unverified: %v", err)
	}
}

// TestBytesRefuseALineBreak: CheckPasswordBytes and AnswerBytes refuse a
// password with a line break before su runs (su and sudo each read one
// line, so each would check something else), and AnswerBytes writes
// nothing to sudo's helper.
func TestBytesRefuseALineBreak(t *testing.T) {
	for _, pw := range []string{"a\nb", "a\rb", "secret\n"} {
		dir := shimAuth(t, 0, 0, "")
		if _, err := CheckPasswordBytes(context.Background(), []byte(pw)); err != ErrLineBreak {
			t.Errorf("CheckPasswordBytes(%q) = %v, want ErrLineBreak", pw, err)
		}
		req, helper := pipeRequest(t)
		if _, err := req.AnswerBytes(context.Background(), []byte(pw)); err != ErrLineBreak {
			t.Errorf("AnswerBytes(%q) = %v, want ErrLineBreak", pw, err)
		}
		if argv, ok := called(t, dir, "su"); ok {
			t.Fatalf("su ran (%q) for %q", argv, pw)
		}
		requireNothingWritten(t, helper)
	}
}

// The chain Verify reports runs from sudo's caller up to the step, and the
// kept password may answer only when it is all topgrade and an AUR helper
// (paru or yay, at their package paths).
func TestRequesterChain(t *testing.T) {
	req, _ := unverifiedPipeRequest(t)
	from, err := verifyAgainst(t, req, fakeTree())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"/usr/bin/yay", "/usr/bin/topgrade"}; !slices.Equal(from.Chain, want) || !from.KeptMayAnswer() {
		t.Fatalf("chain %q (may answer: %v), want %q", from.Chain, from.KeptMayAnswer(), want)
	}
	for _, chain := range [][]string{
		nil,
		{"/usr/bin/bash", "/usr/bin/yay", "/usr/bin/topgrade"},
		{"/usr/bin/yay", "", "/usr/bin/topgrade"},
		{"/usr/bin/makepkg"},
		{"/home/me/topgrade"},
		// A paru or yay that is not the package's: found earlier on PATH, or
		// just named alike.
		{"/home/me/.local/bin/paru", "/usr/bin/topgrade"},
		{"/usr/local/bin/paru", "/usr/bin/topgrade"},
		{"/usr/local/bin/yay", "/usr/bin/topgrade"},
		{"paru", "/usr/bin/topgrade"},
		{"/usr/bin/paru-bin", "/usr/bin/topgrade"},
		{"/usr/bin/paruu", "/usr/bin/topgrade"},
		{"/usr/bin/pikaur", "/usr/bin/topgrade"},
		{"/usr/bin/trizen", "/usr/bin/topgrade"},
		{"/usr/bin/paru", "/usr/bin/makepkg", "/usr/bin/topgrade"},
	} {
		if (Requester{Chain: chain}).KeptMayAnswer() {
			t.Errorf("chain %q may have the kept password", chain)
		}
	}
}
