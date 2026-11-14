package healthview

import (
	"testing"
	"time"
)

func ignoreReport() Report {
	return Report{Checks: []Check{
		{ID: "failed-units", Title: "Failed services", State: Look, Group: "system", Detail: "2 failed: a, b",
			Items: []Item{{"unit:a", "a.service"}, {"unit:b", "b.service"}}},
		{ID: "restart", Title: "Restart recommended", State: Look, Group: "system", Detail: "running 1 · installed 2",
			Items: []Item{{"restart:2", "Running 1"}}},
		{ID: "disk", Title: "Disk space", State: OK, Group: "hardware", Detail: "fine"},
		{ID: "mirror", Title: "Mirror", State: Unknown, Group: "packages", Detail: "can't reach it"},
	}}
}

func TestApplyIgnores(t *testing.T) {
	at := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)

	r := ignoreReport()
	r.ApplyIgnores(nil)
	if r.Attention() != 2 || len(r.IgnoredList) != 0 || len(r.IgnoredRows()) != 0 {
		t.Fatalf("nothing ignored: attention %d, list %v", r.Attention(), r.IgnoredList)
	}

	// Some of a check's items: still Look, with the active ones and a count.
	r = ignoreReport()
	r.ApplyIgnores(map[string]time.Time{"unit:a": at})
	c := r.Checks[0]
	if c.State != Look || len(c.Items) != 1 || c.Items[0].Key != "unit:b" || len(c.IgnoredItems) != 1 || c.Detail != "2 failed: a, b · 1 ignored" {
		t.Fatalf("partly ignored: %+v", c)
	}
	if r.Attention() != 2 || len(r.IgnoredList) != 1 || r.IgnoredList[0].Since != at || r.IgnoredList[0].Title != "Failed services" {
		t.Fatalf("partly ignored: attention %d, list %+v", r.Attention(), r.IgnoredList)
	}

	// All of them: Ignored, counted nowhere, not a row of its group.
	r = ignoreReport()
	r.ApplyIgnores(map[string]time.Time{"unit:a": at, "unit:b": at, "restart:2": at})
	if r.Checks[0].State != Ignored || r.Checks[1].State != Ignored || r.Checks[0].Items != nil || len(r.Checks[0].IgnoredItems) != 2 {
		t.Fatalf("all ignored: %+v %+v", r.Checks[0], r.Checks[1])
	}
	if r.Attention() != 0 {
		t.Fatalf("an ignored check counts: %d", r.Attention())
	}
	for _, s := range r.Sections() {
		for _, c := range s.Checks {
			if c.State == Ignored {
				t.Errorf("%s: an ignored check is a row of %q", c.ID, s.Header)
			}
		}
	}
	if got := len(r.IgnoredRows()); got != 3 {
		t.Fatalf("%d ignored rows, want 3", got)
	}
	if c := CellFor(&r, false); c.Emphasised || c.Line != "" {
		t.Fatalf("with everything ignored the cell is %+v, want calm", c)
	}

	// A key that is not ignored (a new unit) alerts again beside the ignored.
	r = ignoreReport()
	r.Checks[0].Items = append(r.Checks[0].Items, Item{"unit:c", "c.service"})
	r.ApplyIgnores(map[string]time.Time{"unit:a": at, "unit:b": at})
	if r.Checks[0].State != Look || len(r.Checks[0].Items) != 1 || r.Checks[0].Items[0].Key != "unit:c" {
		t.Fatalf("a new item does not alert: %+v", r.Checks[0])
	}
}

// A source that fills only the checks' ignored items still gets its rows.
func TestIgnoredRowsFromChecksAlone(t *testing.T) {
	r := Report{Checks: []Check{{ID: "restart", Title: "Restart recommended", State: Ignored, IgnoredItems: []Item{{"restart:2", "Running 1"}}}}}
	rows := r.IgnoredRows()
	if len(rows) != 1 || rows[0].Title != "Restart recommended" || rows[0].Item.Label != "Running 1" || !rows[0].Since.IsZero() {
		t.Fatalf("rows %+v", rows)
	}
}
