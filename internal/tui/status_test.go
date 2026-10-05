package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Enzord758/archmaid/internal/sysstat"
)

// BenchmarkSystemStatusCache100 is the perf guard's handle on the status
// line: systemStatus's Read with the real disk and meminfo, and a fixture
// cache of 100 packages instead of /var/cache/pacman/pkg. The walk stats
// every cached file (~1.3 µs and 6 allocs each), so on the real cache the
// number was the machine's: 28 µs just after a cache clean, 170 µs with
// 104 packages, same code. The formatting and no-fork tests live with the
// code in internal/sysstat, which benchmarks the same fixture.
func BenchmarkSystemStatusCache100(b *testing.B) {
	p := sysstat.DefaultPaths()
	p.Cache = b.TempDir()
	for i := range 100 {
		name := filepath.Join(p.Cache, fmt.Sprintf("pkg%03d-1.0-1-x86_64.pkg.tar.zst", i))
		if err := os.WriteFile(name, nil, 0o644); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for b.Loop() {
		sysstat.Read(p).Line()
	}
}
