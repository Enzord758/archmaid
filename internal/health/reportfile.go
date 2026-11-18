package health

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/sanitize"
)

const (
	// ReportFile is health.json's name in the state dir: the widget's copy of
	// a Report (docs/design/health.md, "health.json").
	ReportFile = "health.json"
	// ReportVersion is the file's "v".
	ReportVersion = 1
	// ReportMaxChecks caps the checks written; ReportMaxBytes caps the file.
	ReportMaxChecks = 16
	ReportMaxBytes  = 16 << 10
	// reportTitleMax caps an id or a title, in runes.
	reportTitleMax = 64
)

type reportCheck struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	State  string `json:"state"`
	Detail string `json:"detail"`
}

type reportDoc struct {
	V         int           `json:"v"`
	At        string        `json:"at"`
	Attention int           `json:"attention"`
	Checks    []reportCheck `json:"checks"`
}

// ReportJSON renders r as health.json's content (one line and a newline).
// Every text field is sanitized again and capped, the checks are cut to
// ReportMaxChecks, and attention counts the checks written.
func ReportJSON(r Report) ([]byte, error) {
	at := r.At
	if at.IsZero() {
		at = time.Now()
	}
	doc := reportDoc{V: ReportVersion, At: at.UTC().Format(time.RFC3339), Checks: []reportCheck{}}
	for _, c := range r.Checks {
		if len(doc.Checks) >= ReportMaxChecks {
			break
		}
		if c.State == Look {
			doc.Attention++
		}
		doc.Checks = append(doc.Checks, reportCheck{
			ID:     sanitize.Field(c.ID, reportTitleMax),
			Title:  sanitize.Field(c.Title, reportTitleMax),
			State:  c.State.String(),
			Detail: sanitize.Field(c.Detail, DetailMax),
		})
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	b = append(b, '\n')
	if len(b) > ReportMaxBytes {
		return nil, fmt.Errorf("health report is %d bytes, over %d", len(b), ReportMaxBytes)
	}
	return b, nil
}

// WriteReportFile writes r to dir/health.json (dir "" is archmaid's state
// dir): atomically (a temp file in the same folder, then rename), the file
// 0600 and the folder 0700, and never through a symlinked or foreign folder.
// It reads and writes nothing else.
func WriteReportFile(dir string, r Report) error {
	if dir == "" {
		dir = logstore.DefaultDir()
	}
	if dir == "" || !filepath.IsAbs(dir) {
		return errors.New("no state directory")
	}
	b, err := ReportJSON(r)
	if err != nil {
		return err
	}
	uid := os.Getuid()
	if di, err := os.Lstat(dir); err == nil {
		if !di.IsDir() || !ownedPrivate(di, uid) {
			return fmt.Errorf("%s is not a private folder of ours", dir)
		}
	} else if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if di, err := os.Lstat(dir); err != nil || !di.IsDir() || !ownedPrivate(di, uid) {
		return fmt.Errorf("%s is not a private folder of ours", dir)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ReportFile+".tmp*") // created 0600
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // a no-op after a successful rename
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, ReportFile))
}
