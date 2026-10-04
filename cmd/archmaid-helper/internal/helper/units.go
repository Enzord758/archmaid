package helper

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// ---- disable-unit ----------------------------------------------------------

// maxVetted caps how many units one disable-unit request may pull in through
// Also= before it is refused as unreasonable.
const maxVetted = 4 * maxNames

// alsoPattern is what a unit name read from an [Install] section must look
// like before the helper queries systemctl about it.
var alsoPattern = regexp.MustCompile(`^[A-Za-z0-9:_.@-]{1,250}\.[a-z]{1,16}$`)

func runDisableUnit(s *session, args []string) int {
	if len(args) == 0 || len(args) > maxNames {
		return s.usage("disable-unit takes 1–%d unit names", maxNames)
	}
	for _, a := range args {
		if !unitPattern.MatchString(a) {
			return s.usage("invalid unit name %q (must match %s)", a, unitPattern)
		}
	}
	units := slices.Compact(slices.Sorted(slices.Values(args)))
	for _, u := range units {
		if deniedUnit(u) {
			return s.finish(ExitRefused, fmt.Sprintf("refusing to disable %s: it is on the denylist", u))
		}
	}
	release, code := s.lock()
	if code != ExitOK {
		return code
	}
	defer release()

	// Resolve each unit's names (aliases included) before touching anything:
	// a denied unit must not be reachable through an alias.
	states := make(map[string]unitState, len(units))
	for _, u := range units {
		st, err := s.unitState(u)
		if err != nil {
			return s.finish(ExitFailed, err.Error())
		}
		if why := st.denied(); why != "" {
			return s.finish(ExitRefused, fmt.Sprintf("refusing to disable %s: %s", u, why))
		}
		states[u] = st
	}
	var todo []string
	for _, u := range units {
		st := states[u]
		switch {
		case st.load != "loaded":
			s.skip(u, "unit not found ("+st.load+")")
		case st.fileState != "enabled":
			s.skip(u, "not enabled ("+st.fileState+")")
		case st.active != "inactive" && st.active != "failed":
			s.skip(u, "currently running ("+st.active+")")
		default:
			todo = append(todo, u)
		}
	}
	if len(todo) == 0 {
		return s.finish(ExitOK, "")
	}
	if code, msg := s.vetInstall(todo, states); code != ExitOK {
		return s.finish(code, msg)
	}
	ok, msg := s.step(1, 1, fmt.Sprintf("Disabling %d service(s)", len(todo)), disableArgv(todo))
	if !ok {
		return s.finish(ExitFailed, msg)
	}
	return s.finish(ExitOK, "")
}

// vetInstall checks what `systemctl disable` would really touch for the
// units in todo: each one's [Install] section (Alias=, WantedBy=, …) and,
// transitively, every unit named in an Also= line, which systemctl disables
// along with it. Any of them being denied refuses the whole request.
func (s *session) vetInstall(todo []string, states map[string]unitState) (int, string) {
	type item struct{ unit, from string } // from: the requested unit that pulled it in
	queue := make([]item, 0, len(todo))
	for _, u := range todo {
		queue = append(queue, item{u, u})
	}
	seen := map[string]bool{}
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		if seen[it.unit] {
			continue
		}
		seen[it.unit] = true
		if len(seen) > maxVetted {
			return ExitRefused, fmt.Sprintf("refusing to disable %s: Also= pulls in more than %d units", it.from, maxVetted)
		}
		st, ok := states[it.unit]
		if !ok { // reached through Also=
			var err error
			if st, err = s.unitState(it.unit); err != nil {
				return ExitFailed, err.Error()
			}
		}
		if st.load == "loaded" {
			if err := s.readInstall(it.unit, &st); err != nil {
				return ExitFailed, err.Error()
			}
		}
		if why := st.denied(); why != "" {
			if it.unit == it.from {
				return ExitRefused, fmt.Sprintf("refusing to disable %s: %s", it.unit, why)
			}
			return ExitRefused, fmt.Sprintf("refusing to disable %s: its Also= disables %s too, and %s", it.from, it.unit, why)
		}
		for _, a := range st.also {
			if !alsoPattern.MatchString(a) {
				// Specifiers (%i, …) or anything odd: we can't tell what
				// systemctl would resolve it to, so we don't guess.
				return ExitRefused, fmt.Sprintf("refusing to disable %s: cannot vet Also=%s", it.from, a)
			}
			queue = append(queue, item{a, it.from})
		}
	}
	return ExitOK, ""
}

// unitState is what the helper knows about one unit.
type unitState struct {
	// names: the argument, Id, Names and any [Install] Alias=.
	names []string
	// wantedBy: WantedBy/RequiredBy/UpheldBy, both the runtime property and
	// the [Install] lines (the property only lists units that are loaded,
	// so on its own it misses e.g. WantedBy=systemd-suspend.service).
	wantedBy                []string
	also                    []string // [Install] Also=
	load, fileState, active string
}

// denied returns why the unit must not be disabled, or "".
func (st unitState) denied() string {
	for _, n := range st.names {
		if deniedUnit(n) {
			return "it is " + n + ", which is on the denylist"
		}
	}
	for _, w := range st.wantedBy {
		if slices.Contains(sleepUnits, w) {
			return "it is wanted by " + w + " (suspend/resume handling)"
		}
	}
	return ""
}

func unitShowArgv(u string) []string {
	return []string{Systemctl, "show", "--property=Id,Names,LoadState,UnitFileState,ActiveState,WantedBy,RequiredBy,UpheldBy", "--", u}
}

func unitCatArgv(u string) []string { return []string{Systemctl, "cat", "--", u} }

func (s *session) unitState(u string) (unitState, error) {
	out, errOut, exit, err := s.o.Runner.Query(unitShowArgv(u))
	if err != nil {
		return unitState{}, fmt.Errorf("systemctl show %s: %w", u, err)
	}
	if exit != 0 {
		return unitState{}, fmt.Errorf("systemctl show %s exited %d: %s", u, exit, firstLine(errOut))
	}
	st := unitState{names: []string{u}}
	for _, l := range nonEmptyLines(out) {
		k, v, _ := strings.Cut(l, "=")
		switch k {
		case "Id":
			st.names = append(st.names, v)
		case "Names":
			st.names = append(st.names, strings.Fields(v)...)
		case "LoadState":
			st.load = v
		case "UnitFileState":
			st.fileState = v
		case "ActiveState":
			st.active = v
		case "WantedBy", "RequiredBy", "UpheldBy":
			st.wantedBy = append(st.wantedBy, strings.Fields(v)...)
		}
	}
	return st, nil
}

// readInstall adds u's [Install] section (from `systemctl cat`: the unit file
// and its drop-ins) to st. `systemctl show` has no Also= property, so this
// is the only way to see it.
func (s *session) readInstall(u string, st *unitState) error {
	out, errOut, exit, err := s.o.Runner.Query(unitCatArgv(u))
	if err != nil {
		return fmt.Errorf("systemctl cat %s: %w", u, err)
	}
	if exit != 0 {
		return fmt.Errorf("systemctl cat %s exited %d: %s", u, exit, firstLine(errOut))
	}
	in := parseInstall(out)
	st.names = append(st.names, in["Alias"]...)
	st.wantedBy = append(st.wantedBy, in["WantedBy"]...)
	st.wantedBy = append(st.wantedBy, in["RequiredBy"]...)
	st.wantedBy = append(st.wantedBy, in["UpheldBy"]...)
	st.also = append(st.also, in["Also"]...)
	return nil
}

// parseInstall collects the [Install] values of every file in `systemctl
// cat` output. It deliberately ignores list resets (an empty `Also=` in a
// drop-in) and takes the union: over-reading can only refuse more.
func parseInstall(cat string) map[string][]string {
	m := map[string][]string{}
	inInstall := false
	var cont string
	for l := range strings.SplitSeq(cat, "\n") {
		l = strings.TrimSpace(l)
		if cont != "" {
			l, cont = cont+" "+l, ""
		}
		if strings.HasSuffix(l, `\`) {
			cont = strings.TrimSuffix(l, `\`)
			continue
		}
		switch {
		case l == "", l[0] == '#', l[0] == ';':
		case l[0] == '[':
			inInstall = l == "[Install]"
		case inInstall:
			k, v, ok := strings.Cut(l, "=")
			if !ok {
				continue
			}
			k = strings.TrimSpace(k)
			m[k] = append(m[k], strings.Fields(v)...)
		}
	}
	return m
}

// Units the helper never disables, whatever the caller asks. Exact entries
// are unit names without the type suffix, so they also cover e.g. a
// .socket of the same name reached through Also=; prefixes match any name.
//
// The bus itself is denied by exact name, not by a "dbus" prefix: services
// carry D-Bus activation aliases such as dbus-org.bluez.service
// (bluetooth) or dbus-org.freedesktop.Avahi.service, and those must not
// get an ordinary unit refused. The real unit behind such an alias is still
// checked by its Id and other names, so dbus-org.freedesktop.login1.service
// is refused as systemd-logind.service.
var (
	unitDenyPrefixes = []string{
		"systemd-", "polkit", "getty@", "NetworkManager", "sshd", "coolercontrold",
		"ly@",
	}
	unitDenyExact = []string{
		// the system bus (dbus.service is an alias of one of the other two)
		"dbus", "dbus-broker", "dbus-daemon",
		// login
		"sddm", "gdm", "lightdm", "ly", "greetd", "display-manager",
		// security controls
		"firewalld", "nftables", "iptables", "ip6tables", "ufw", "apparmor", "auditd",
		// sleep handling
		"nvidia-suspend", "nvidia-resume", "nvidia-hibernate", "nvidia-suspend-then-hibernate",
	}
	// sleepUnits: a unit wanted or required by any of these runs around
	// suspend/hibernate; disabling it can break resume.
	sleepUnits = []string{
		"sleep.target", "suspend.target", "hibernate.target", "hybrid-sleep.target",
		"suspend-then-hibernate.target",
		"systemd-suspend.service", "systemd-hibernate.service", "systemd-hybrid-sleep.service",
		"systemd-suspend-then-hibernate.service",
	}
)

func deniedUnit(u string) bool {
	stem := u
	if i := strings.LastIndexByte(u, '.'); i > 0 {
		stem = u[:i]
	}
	if slices.Contains(unitDenyExact, stem) {
		return true
	}
	for _, p := range unitDenyPrefixes {
		if strings.HasPrefix(u, p) {
			return true
		}
	}
	return false
}
