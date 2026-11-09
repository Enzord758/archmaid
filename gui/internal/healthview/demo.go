package healthview

import (
	"context"
	"time"
)

// Demo is the stub's Source (archmaid-gui --stub): canned answers, nothing
// read from the machine.
func Demo() Source { return demo{} }

type demo struct{}

func (demo) Run(context.Context) Report {
	return Report{At: time.Now(), Checks: []Check{
		{ID: "failed-units", Title: "Failed services", State: Look, Group: "system", Detail: "1 failed: bluetooth.service", Copy: "journalctl -b -u bluetooth.service"},
		{ID: "restart", Title: "Restart recommended", State: Look, Group: "system", Detail: "running 7.3.1-1-cachyos · installed 7.3.3-1-cachyos"},
		{ID: "pacnew", Title: "Config files to merge", State: OK, Group: "packages", Detail: "No .pacnew or .pacsave files"},
		{ID: "news", Title: "Arch news", State: Look, Group: "packages", Detail: "1 new since your last update: Manual intervention for pacman 7.0.0 and local repositories required", Link: "https://archlinux.org/news/manual-intervention-for-pacman-700-and-local-repositories-required/"},
		{ID: "disk", Title: "Disk space", State: OK, Group: "hardware", Detail: "/ 118 G free · /boot 412 M free"},
	}}
}

func (demo) UnreadNews(context.Context) (Unread, error) {
	return Unread{Since: time.Now().Add(-5 * 24 * time.Hour), Items: []NewsItem{{
		Title: "Manual intervention for pacman 7.0.0 and local repositories required",
		Link:  "https://archlinux.org/news/manual-intervention-for-pacman-700-and-local-repositories-required/",
		Date:  time.Now().Add(-48 * time.Hour)}}}, nil
}
