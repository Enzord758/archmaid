package health

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sampleReport() Report {
	return Report{At: time.Date(2026, 11, 18, 9, 30, 0, 0, time.UTC), Checks: []Check{
		{ID: IDFailedUnits, Title: "Failed services", State: Look, Detail: "2 failed", Group: GroupSystem},
		{ID: IDMirror, Title: "Mirror", State: Unknown, Detail: "offline"},
		{ID: IDPacnew, Title: "Config files to merge", State: Ignored},
		{ID: IDDisk, Title: "Disk space", State: OK, Detail: "ok"},
	}}
}

func TestReportJSONShape(t *testing.T) {
	b, err := ReportJSON(sampleReport())
	if err != nil {
		t.Fatal(err)
	}
	want := `{"v":1,"at":"2026-11-18T09:30:00Z","attention":1,"checks":[` +
		`{"id":"failed-units","title":"Failed services","state":"look","detail":"2 failed"},` +
		`{"id":"mirror","title":"Mirror","state":"unknown","detail":"offline"},` +
		`{"id":"pacnew","title":"Config files to merge","state":"ignored","detail":""},` +
		`{"id":"disk","title":"Disk space","state":"ok","detail":"ok"}]}` + "\n"
	if string(b) != want {
		t.Fatalf("got\n%s\nwant\n%s", b, want)
	}
}

func TestReportJSONSanitizesAndCaps(t *testing.T) {
	r := Report{At: time.Now()}
	for i := 0; i < 40; i++ {
		r.Checks = append(r.Checks, Check{ID: "x\x1b[31m", Title: "t\nu", State: Look,
			Detail: strings.Repeat("é", 500) + "\x07\r\nforged"})
	}
	b, err := ReportJSON(r)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Attention int
		Checks    []map[string]string
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Checks) != ReportMaxChecks || doc.Attention != ReportMaxChecks {
		t.Fatalf("checks %d attention %d", len(doc.Checks), doc.Attention)
	}
	c := doc.Checks[0]
	if c["id"] != "x" || c["title"] != "tu" || len([]rune(c["detail"])) > DetailMax+1 || strings.ContainsAny(c["detail"], "\x07\r\n") {
		t.Fatalf("not sanitized: %q", c)
	}
	if len(b) > ReportMaxBytes {
		t.Fatalf("%d bytes", len(b))
	}
}

func TestReportJSONOverTheByteCap(t *testing.T) {
	// 16 checks of 200 "<", each escaped to six bytes by encoding/json: over 16 KiB.
	r := Report{At: time.Now()}
	for i := 0; i < ReportMaxChecks; i++ {
		r.Checks = append(r.Checks, Check{ID: "a", Title: "b", Detail: strings.Repeat("<", DetailMax)})
	}
	if _, err := ReportJSON(r); err == nil {
		t.Fatal("no error over the byte cap")
	}
}

func TestWriteReportFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if err := WriteReportFile(dir, sampleReport()); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, ReportFile))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("%v %v", fi, err)
	}
	if di, _ := os.Stat(dir); di.Mode().Perm() != 0o700 {
		t.Fatalf("dir %v", di.Mode())
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Fatalf("leftovers: %v", ents)
	}
	if err := WriteReportFile(dir, Report{At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, ReportFile))
	if !strings.Contains(string(b), `"checks":[]`) {
		t.Fatalf("%s", b)
	}
}

func TestWriteReportFileRefusals(t *testing.T) {
	base := t.TempDir()
	if err := WriteReportFile("relative/dir", sampleReport()); err == nil {
		t.Fatal("relative dir accepted")
	}
	real := filepath.Join(base, "real")
	os.Mkdir(real, 0o700)
	link := filepath.Join(base, "link")
	os.Symlink(real, link)
	if err := WriteReportFile(link, sampleReport()); err == nil {
		t.Fatal("symlinked folder accepted")
	}
	open := filepath.Join(base, "open")
	os.Mkdir(open, 0o700)
	os.Chmod(open, 0o777)
	if err := WriteReportFile(open, sampleReport()); err == nil {
		t.Fatal("group/other-writable folder accepted")
	}
	file := filepath.Join(base, "file")
	os.WriteFile(file, nil, 0o600)
	if err := WriteReportFile(file, sampleReport()); err == nil {
		t.Fatal("a file as folder accepted")
	}
	if ents, _ := os.ReadDir(real); len(ents) != 0 {
		t.Fatalf("wrote through the symlink: %v", ents)
	}
}

func TestWriteReportFileDefaultDirUsesHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := WriteReportFile("", sampleReport()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "state", "archmaid", ReportFile)); err != nil {
		t.Fatal(err)
	}
}
