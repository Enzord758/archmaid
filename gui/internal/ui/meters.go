package ui

import (
	"fmt"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/Enzord758/archmaid/internal/sysstat"
)

// The sidebar's meters (P8 row 9; spec 1.1, A1): disk free of the disk,
// RAM available of the total, the package cache's size of the disk. Each
// is a name, the status line's value and a thin bar. A count the engine
// couldn't read (0) leaves the bar empty and the value as sysstat wrote it
// ("N/A"). Hidden in a short window (shortHeight), where the footer's
// "free …" stays.

// shortHeight is where the meters hide (spec 1.8). Not 480: the wide
// layout's sidebar asks for 449 to 508 px of height with the meters in it
// (the KDE check measured 508), and a window between 480 and that logged
// "AdwToastOverlay exceeds AdwApplicationWindow height" at every resize
// step.
const shortHeight = "max-height: 540sp"

type meter struct {
	value *gtk.Label
	bar   *gtk.LevelBar
}

// meters are the sidebar's three, in order: disk, RAM, cache.
type meters struct {
	box              *gtk.Box
	disk, ram, cache meter
}

func newMeter(box *gtk.Box, name string) meter {
	head := gtk.NewBox(gtk.OrientationHorizontal, 6)
	n := gtk.NewLabel(name)
	n.SetXAlign(0)
	n.SetHExpand(true)
	n.AddCSSClass("am-meter-name")
	v := gtk.NewLabel("")
	v.SetXAlign(1)
	v.AddCSSClass("am-meter-value")
	v.AddCSSClass("numeric")
	head.Append(n)
	head.Append(v)
	bar := gtk.NewLevelBar()
	bar.SetMinValue(0)
	bar.SetMaxValue(1)
	bar.AddCSSClass("am-meter-bar")
	// No offsets: the bar's colour is ours (style.css), not low/high/full.
	bar.RemoveOffsetValue(gtk.LEVEL_BAR_OFFSET_LOW)
	bar.RemoveOffsetValue(gtk.LEVEL_BAR_OFFSET_HIGH)
	bar.RemoveOffsetValue(gtk.LEVEL_BAR_OFFSET_FULL)
	m := gtk.NewBox(gtk.OrientationVertical, 4)
	m.AddCSSClass("am-meter")
	m.Append(head)
	m.Append(bar)
	box.Append(m)
	return meter{value: v, bar: bar}
}

func newMeters() *meters {
	box := gtk.NewBox(gtk.OrientationVertical, 10)
	box.AddCSSClass("am-meters")
	kicker := gtk.NewLabel("SYSTEM")
	kicker.SetXAlign(0)
	kicker.AddCSSClass("am-kicker")
	box.Append(kicker)
	ms := &meters{box: box}
	ms.disk = newMeter(box, "Disk free")
	ms.ram = newMeter(box, "RAM available")
	ms.cache = newMeter(box, "Package cache")
	return ms
}

// show draws s.
func (ms *meters) show(s sysstat.Status) {
	ms.disk.set(s.Free, s.FreeBytes, s.DiskBytes)
	ms.ram.set(s.RAM, s.RAMAvailBytes, s.RAMBytes)
	ms.cache.set(s.Cache, s.CacheBytes, s.DiskBytes)
}

func (m meter) set(text string, part, whole uint64) {
	m.value.SetText(text)
	m.bar.SetValue(fraction(part, whole))
}

// fraction is part of whole in [0, 1]; 0 when whole is unknown.
func fraction(part, whole uint64) float64 {
	if whole == 0 {
		return 0
	}
	return min(float64(part)/float64(whole), 1)
}

// text is what the meters show, for the harness: "name value fraction"
// per meter, fractions to two places.
func (ms *meters) text() []string {
	var out []string
	for _, m := range []struct {
		name string
		m    meter
	}{{"disk", ms.disk}, {"ram", ms.ram}, {"cache", ms.cache}} {
		out = append(out, fmt.Sprintf("%s %s %.2f", m.name, m.m.value.Text(), m.m.bar.Value()))
	}
	return out
}
