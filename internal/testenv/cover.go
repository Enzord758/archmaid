package testenv

import (
	"flag"
	"os"
	"testing"
)

// coverChildren makes a test binary that a test re-executes as a test
// (`os.Args[0] -test.run=…`: the TUI's TTY child running the real tui.Run,
// the GUI's display child) count toward coverage like its parent. go test
// hands the top binary both -test.gocoverdir and GOCOVERDIR; a re-executed
// one inherits only GOCOVERDIR, and without the flag the testing package
// writes nothing for it. So when the binary is cover-instrumented and the
// flag wasn't given, point it at GOCOVERDIR. An explicit -test.gocoverdir
// still wins: it is parsed afterwards, in m.Run.
//
// Coverage only: what the tests run and see is unchanged. A child that exits
// before m.Run (a TestMain dispatch) needs none of this; the runtime writes
// its counters to GOCOVERDIR on exit.
func coverChildren() {
	d := os.Getenv("GOCOVERDIR")
	if d == "" || testing.CoverMode() == "" {
		return
	}
	if f := flag.Lookup("test.gocoverdir"); f != nil && f.Value.String() == "" {
		_ = f.Value.Set(d)
	}
}
