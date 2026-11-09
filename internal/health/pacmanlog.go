package health

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Enzord758/archmaid/internal/report"
	"github.com/Enzord758/archmaid/internal/sanitize"
)

// maxLogTail is how much of the end of pacman.log is read. The last full
// upgrade and the newest .pacnew warnings are at the end; a log longer than
// this has older history that no longer matters here.
const maxLogTail = 32 << 20

// logTimeLayout is pacman.log's timestamp: [2026-11-08T10:12:45+0100].
const logTimeLayout = "2006-01-02T15:04:05-0700"

// openLogTail opens the last maxLogTail bytes of path. The caller closes the
// returned file; the reader can be read from the start more than once.
func openLogTail(path string) (*io.SectionReader, *os.File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	start := max(fi.Size()-maxLogTail, 0)
	return io.NewSectionReader(f, start, fi.Size()-start), f, nil
}

// LastFullUpgrade returns the time of the last successful full system upgrade
// in a pacman.log read from r, and whether there was one.
//
// The rule, on real log lines:
//
//	[T] [PACMAN] Running 'pacman -Syu'
//	[T] [PACMAN] starting full system upgrade     <- pending
//	[T] [ALPM] transaction started
//	[T] [ALPM] upgraded foo (1 -> 2)
//	[T] [ALPM] transaction completed              <- this time counts
//
// A "starting full system upgrade" line (pacman -Su/-Syu, also yay and
// topgrade) arms a pending upgrade. The next "[ALPM] transaction completed"
// sets the result to that line's time and disarms it. A "[ALPM] transaction
// failed", a new "[PACMAN] Running" line, or another "starting full system
// upgrade" disarms it without a result: the upgrade was aborted, failed, or
// was declined at the prompt. So only a completed transaction counts; a run
// that found nothing to do (no transaction) does not move the date, which
// can only make the news check say Look more often, never less. A completed
// transaction of a plain `pacman -S foo` (no "starting full system upgrade")
// is not a full upgrade. Lines of other shapes are ignored.
func LastFullUpgrade(r io.Reader) (time.Time, bool) {
	var last time.Time
	var found, pending bool
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if len(line) < 3 || line[0] != '[' {
			continue
		}
		end := strings.IndexByte(line, ']')
		if end < 0 {
			continue
		}
		rest := line[end+1:]
		switch {
		case strings.HasPrefix(rest, " [PACMAN] starting full system upgrade"):
			pending = true
		case strings.HasPrefix(rest, " [PACMAN] Running"):
			pending = false
		case strings.HasPrefix(rest, " [ALPM] transaction failed"):
			pending = false
		case pending && strings.HasPrefix(rest, " [ALPM] transaction completed"):
			pending = false
			if t, err := time.Parse(logTimeLayout, line[1:end]); err == nil {
				last, found = t, true
			}
		}
	}
	return last, found
}

// pacFiles are the .pacnew and .pacsave files pacman.log warned about and
// that still exist, pacnew first. A file merged and deleted since is dropped.
func pacFiles(ctx context.Context, d Deps) ([]string, error) {
	sr, f, err := openLogTail(d.PacmanLog)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	ch, err := report.ParseLog(sr)
	if err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	exists := d.Exists
	if exists == nil {
		return nil, errors.New("no file check")
	}
	var out []string
	for _, p := range append(ch.Pacnew, ch.Pacsave...) {
		if strings.HasPrefix(p, "/") && exists(p) {
			out = append(out, p)
		}
	}
	return out, nil
}

const pacShown = 3

func checkPacnew(ctx context.Context, d Deps) Check {
	files, err := pacFiles(ctx, d)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return unknown(IDPacnew, TitlePacnew, "no pacman log")
		}
		return unknown(IDPacnew, TitlePacnew, "can't read the pacman log")
	}
	if len(files) == 0 {
		return Check{State: OK, Detail: "No .pacnew or .pacsave files"}
	}
	shown := files
	if len(shown) > pacShown {
		shown = shown[:pacShown]
	}
	noun := "files"
	if len(files) == 1 {
		noun = "file"
	}
	detail := fmt.Sprintf("%d %s: %s", len(files), noun, strings.Join(shown, ", "))
	if len(files) > len(shown) {
		detail += fmt.Sprintf(" +%d more", len(files)-len(shown))
	}
	return Check{State: Look, Detail: sanitize.Field(detail, DetailMax), Copy: "sudo pacdiff"}
}
