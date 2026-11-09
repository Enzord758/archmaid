package healthview

import (
	"context"
	"time"
)

// Demo is the stub's Source (archmaid-gui --stub): a plausible machine with
// three things to look at, canned, nothing read from the real one. It feeds
// screenshots and the wiki, so it has no test data in it: hostile cases
// live in the tests.
func Demo() Source { return demo{} }

type demo struct{}

const demoNewsLink = "https://archlinux.org/news/manual-intervention-for-pacman-700-and-local-repositories-required/"

func (demo) Run(context.Context) Report {
	return Report{At: time.Now(), Checks: []Check{
		{ID: "failed-units", Title: "Failed services", State: Look, Group: "system", Detail: "1 failed: bluetooth.service", Copy: "journalctl -b -u bluetooth.service"},
		{ID: "restart", Title: "Restart recommended", State: Look, Group: "system", Detail: "running 7.3.1-1-cachyos · installed 7.3.3-1-cachyos"},
		{ID: "time-sync", Title: "Clock sync", State: OK, Group: "system", Detail: "Clock is synchronised (systemd-timesyncd)"},
		{ID: "crashes", Title: "Crashes", State: OK, Group: "system", Detail: "No crashes in the last 24 h"},
		{ID: "pacman-lock", Title: "Package lock", State: OK, Group: "packages", Detail: "No stale pacman lock"},
		{ID: "pacnew", Title: "Config files to merge", State: OK, Group: "packages", Detail: "No .pacnew or .pacsave files"},
		{ID: "aur", Title: "AUR packages", State: OK, Group: "packages", Detail: "No foreign package was removed or orphaned in the AUR"},
		{ID: "news", Title: "Arch news", State: Look, Group: "packages", Detail: "1 new since your last update: Manual intervention for pacman 7.0.0 and local repositories required", Link: demoNewsLink},
		{ID: "mirror", Title: "Mirror", State: Unknown, Group: "packages", Detail: "Can't reach the mirror status page"},
		{ID: "disk", Title: "Disk space", State: OK, Group: "hardware", Detail: "/ 118 G free · /boot 412 M free"},
		{ID: "disk-health", Title: "Drive health", State: OK, Group: "hardware", Detail: "All drives report healthy (SMART)"},
	}}
}

func (demo) UnreadNews(context.Context) (Unread, error) {
	return Unread{Since: time.Now().Add(-5 * 24 * time.Hour), Items: []NewsItem{{
		Title: "Manual intervention for pacman 7.0.0 and local repositories required",
		Link:  demoNewsLink,
		Date:  time.Now().Add(-48 * time.Hour)}}}, nil
}
