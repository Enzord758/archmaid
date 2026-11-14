package healthview

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Demo is the stub's Source (archmaid-gui --stub): a plausible machine with
// things to look at, canned, nothing read from the real one. It feeds
// screenshots and the wiki, so it has no test data in it: hostile cases
// live in the tests. It is an Ignorer too, with its list in memory: Ignore
// and Undo work in the stub and change nothing real.
func Demo() Source { return &demo{ign: map[string]time.Time{}} }

type demo struct {
	mu  sync.Mutex
	ign map[string]time.Time
}

const demoNewsLink = "https://archlinux.org/news/manual-intervention-for-pacman-700-and-local-repositories-required/"

// demoIgnoreMax mirrors the real list's size limit.
const demoIgnoreMax = 500

func (d *demo) Ignore(key string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.ign[key]; ok {
		return nil
	}
	if len(d.ign) >= demoIgnoreMax {
		return errors.New("too many ignored items")
	}
	d.ign[key] = time.Now()
	return nil
}

func (d *demo) Unignore(key string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.ign, key)
	return nil
}

func (d *demo) Run(context.Context) Report {
	d.mu.Lock()
	ign := make(map[string]time.Time, len(d.ign))
	for k, v := range d.ign {
		ign[k] = v
	}
	d.mu.Unlock()
	r := Report{At: time.Now(), Checks: []Check{
		{ID: "failed-units", Title: "Failed services", State: Look, Group: "system", Detail: "2 failed: bluetooth.service, cups.service",
			Copy:  "journalctl -b -u bluetooth.service -u cups.service",
			Items: []Item{{"unit:bluetooth.service", "bluetooth.service"}, {"unit:cups.service", "cups.service"}}},
		{ID: "restart", Title: "Restart recommended", State: Look, Group: "system", Detail: "running 7.3.1-1-cachyos · installed 7.3.3-1-cachyos",
			Items: []Item{{"restart:7.3.3-1-cachyos", "Running 7.3.1-1-cachyos"}}},
		{ID: "time-sync", Title: "Clock sync", State: OK, Group: "system", Detail: "Clock is synchronised (systemd-timesyncd)"},
		{ID: "crashes", Title: "Crashes", State: OK, Group: "system", Detail: "No crashes in the last 24 h"},
		{ID: "pacman-lock", Title: "Package lock", State: OK, Group: "packages", Detail: "No stale pacman lock"},
		{ID: "pacnew", Title: "Config files to merge", State: OK, Group: "packages", Detail: "No .pacnew or .pacsave files"},
		{ID: "aur", Title: "AUR packages", State: OK, Group: "packages", Detail: "No foreign package was removed or orphaned in the AUR"},
		{ID: "news", Title: "Arch news", State: Look, Group: "packages", Detail: "1 new since your last update: Manual intervention for pacman 7.0.0 and local repositories required", Link: demoNewsLink,
			Items: []Item{{"news:1794000000", "Manual intervention for pacman 7.0.0 and local repositories required"}}},
		{ID: "mirror", Title: "Mirror", State: Unknown, Group: "packages", Detail: "Can't reach the mirror status page"},
		{ID: "disk", Title: "Disk space", State: OK, Group: "hardware", Detail: "/ 118 G free · /boot 412 M free"},
		{ID: "disk-health", Title: "Drive health", State: OK, Group: "hardware", Detail: "All drives report healthy (SMART)"},
	}}
	r.ApplyIgnores(ign)
	return r
}

func (*demo) UnreadNews(context.Context) (Unread, error) {
	return Unread{Since: time.Now().Add(-5 * 24 * time.Hour), Items: []NewsItem{{
		Title: "Manual intervention for pacman 7.0.0 and local repositories required",
		Link:  demoNewsLink,
		Date:  time.Now().Add(-48 * time.Hour)}}}, nil
}
