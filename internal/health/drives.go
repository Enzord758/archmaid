package health

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Enzord758/archmaid/internal/sanitize"
)

// UDisksMax caps the busctl JSON that is parsed.
const UDisksMax = 4 << 20

// udisks D-Bus names.
const (
	ifaceDrive = "org.freedesktop.UDisks2.Drive"
	ifaceAta   = "org.freedesktop.UDisks2.Drive.Ata"
	ifaceNVMe  = "org.freedesktop.UDisks2.NVMe.Controller"
)

// driveCmd is the one call: UDisks2's ObjectManager, which holds every drive's
// cached SMART properties. Read-only on the system bus, no polkit. --auto-start=no
// stops D-Bus from starting udisksd just to answer (it would be a change to the
// system); --timeout bounds the call.
var driveCmd = []string{"--system", "--json=short", "--auto-start=no", "--timeout=5", "call",
	"org.freedesktop.UDisks2", "/org/freedesktop/UDisks2",
	"org.freedesktop.DBus.ObjectManager", "GetManagedObjects"}

func checkDiskHealth(ctx context.Context, d Deps) Check {
	if d.Query == nil {
		return unknown(IDDiskHealth, TitleDiskHealth, "udisks2 isn't running")
	}
	out, err := d.Query(ctx, "busctl", driveCmd...)
	if err != nil && len(out) == 0 {
		return unknown(IDDiskHealth, TitleDiskHealth, "udisks2 isn't running")
	}
	if len(out) > UDisksMax {
		return unknown(IDDiskHealth, TitleDiskHealth, "drive data too large")
	}
	drives, err := parseDrives(out)
	if err != nil {
		return unknown(IDDiskHealth, TitleDiskHealth, "can't read drive data")
	}
	var bad, good []string
	for _, dr := range drives {
		if p := dr.problem(); p != "" {
			bad = append(bad, dr.name()+": "+p)
		} else if dr.hasData() {
			good = append(good, dr.name())
		}
	}
	switch {
	case len(bad) > 0:
		return Check{State: Look, Detail: sanitize.Field(strings.Join(bad, " · "), DetailMax)}
	case len(good) > 0:
		return Check{State: OK, Detail: sanitize.Field("SMART is fine: "+strings.Join(good, ", "), DetailMax)}
	}
	return unknown(IDDiskHealth, TitleDiskHealth, "no SMART data yet")
}

type drive struct {
	model, id string
	// ATA
	ataSupported, ataFailing bool
	ataUpdated               bool
	ataBad                   int64
	ataAttrsFailing          int64
	hasATA                   bool
	// NVMe
	nvme        bool
	nvmeUpdated bool
	nvmeWarn    []string
}

func (d drive) name() string {
	n := sanitize.Field(strings.TrimSpace(d.model), 40)
	if n == "" {
		n = sanitize.Field(d.id, 40)
	}
	return n
}

// hasData: SMART data was read at least once (SmartUpdated != 0); a drive
// whose properties are still the defaults says nothing.
func (d drive) hasData() bool {
	return (d.hasATA && d.ataSupported && d.ataUpdated) || (d.nvme && d.nvmeUpdated)
}

// problem is why the drive needs a look ("" = nothing).
func (d drive) problem() string {
	if !d.hasData() {
		return ""
	}
	switch {
	case d.hasATA && d.ataFailing:
		return "SMART says it's failing"
	case d.nvme && len(d.nvmeWarn) > 0:
		return "critical warning: " + strings.Join(d.nvmeWarn, ", ")
	case d.hasATA && d.ataAttrsFailing > 0:
		return fmt.Sprintf("%d SMART attributes failing", d.ataAttrsFailing)
	case d.hasATA && d.ataBad > 0:
		return fmt.Sprintf("%d bad sectors", d.ataBad)
	}
	return ""
}

// variant is busctl's JSON of a D-Bus variant: {"type":"b","data":true}.
type variant struct {
	Data json.RawMessage `json:"data"`
}

type managed struct {
	Data []map[string]map[string]map[string]variant `json:"data"`
}

// parseDrives reads GetManagedObjects' JSON (busctl --json=short): data[0] maps
// each object path to its interfaces and their properties. Only drive objects
// (those with the Drive interface) are read; every value is type-checked, a
// property of the wrong type is ignored, and unknown shapes never panic.
func parseDrives(b []byte) ([]drive, error) {
	var m managed
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	if len(m.Data) == 0 {
		return nil, fmt.Errorf("no data")
	}
	var paths []string
	for p := range m.Data[0] {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var out []drive
	for _, p := range paths {
		ifs := m.Data[0][p]
		base, ok := ifs[ifaceDrive]
		if !ok {
			continue
		}
		dr := drive{id: p[strings.LastIndex(p, "/")+1:]}
		dr.model = str(base["Model"])
		if a, ok := ifs[ifaceAta]; ok {
			dr.hasATA = true
			dr.ataSupported = boolean(a["SmartSupported"])
			dr.ataFailing = boolean(a["SmartFailing"])
			dr.ataUpdated = num(a["SmartUpdated"]) > 0
			dr.ataBad = num(a["SmartNumBadSectors"])
			dr.ataAttrsFailing = num(a["SmartNumAttributesFailing"])
		}
		if n, ok := ifs[ifaceNVMe]; ok {
			dr.nvme = true
			dr.nvmeUpdated = num(n["SmartUpdated"]) > 0
			dr.nvmeWarn = strs(n["SmartCriticalWarning"], 8)
		}
		out = append(out, dr)
	}
	return out, nil
}

func str(v variant) string {
	var s string
	if json.Unmarshal(v.Data, &s) != nil {
		return ""
	}
	return s
}

func boolean(v variant) bool {
	var b bool
	return json.Unmarshal(v.Data, &b) == nil && b
}

// num reads an integer property (t, x, i, q); a float or anything else is 0.
func num(v variant) int64 {
	var n int64
	if json.Unmarshal(v.Data, &n) != nil {
		return 0
	}
	return n
}

func strs(v variant, max int) []string {
	var s []string
	if json.Unmarshal(v.Data, &s) != nil {
		return nil
	}
	var out []string
	for _, x := range s {
		if x = sanitize.Field(x, 30); x != "" && len(out) < max {
			out = append(out, x)
		}
	}
	return out
}
