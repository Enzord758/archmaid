package helper

import (
	"slices"
	"strings"
	"testing"
)

// ---- disable-unit ----------------------------------------------------------

func unitFake(states map[string]string) *fakeRunner {
	f := newFake()
	for u, s := range states {
		f.on(unitShowArgv(u), reply{out: s})
	}
	return f
}

func show(id, names, file, active string) string {
	return "Id=" + id + "\nNames=" + names + "\nLoadState=loaded\nUnitFileState=" + file + "\nActiveState=" + active + "\n"
}

func TestDisableUnit(t *testing.T) {
	f := unitFake(map[string]string{
		"idle.service":    show("idle.service", "idle.service", "enabled", "inactive"),
		"crashed.service": show("crashed.service", "crashed.service", "enabled", "failed"),
		"busy.service":    show("busy.service", "busy.service", "enabled", "active"),
		"off.service":     show("off.service", "off.service", "disabled", "inactive"),
		"static.service":  show("static.service", "static.service", "static", "inactive"),
		"ghost.service":   "Id=ghost.service\nNames=ghost.service\nLoadState=not-found\nUnitFileState=\nActiveState=inactive\n",
	})
	o, out := rootOpts(t, f)
	args := []string{"disable-unit", "idle.service", "busy.service", "off.service", "crashed.service", "static.service", "ghost.service"}
	if code := Run(args, o); code != ExitOK {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if got := join(f.streams); !slices.Equal(got, []string{"/usr/bin/systemctl disable --now -- crashed.service idle.service"}) {
		t.Errorf("ran %q", got)
	}
	sk := skippedNames(t, out)
	for _, u := range []string{"busy.service", "off.service", "static.service", "ghost.service"} {
		if sk[u] == "" {
			t.Errorf("%s not reported as skipped (%v)", u, sk)
		}
	}
}

func TestDisableUnitDenylistIsExit4(t *testing.T) {
	for _, u := range []string{
		"systemd-resolved.service", "dbus.service", "dbus-broker.service",
		"dbus-daemon.service", "polkit.service",
		"getty@tty1.service", "sddm.service", "gdm.service", "lightdm.service", "ly.service",
		"ly@tty2.service", "greetd.service", "display-manager.service",
		"NetworkManager.service", "NetworkManager-wait-online.service",
		"sshd.service", "sshd@0.service", "coolercontrold.service",
		"firewalld.service", "nftables.service", "iptables.service", "ip6tables.service",
		"ufw.service", "apparmor.service", "auditd.service",
		"nvidia-suspend.service", "nvidia-resume.service", "nvidia-hibernate.service",
		"nvidia-suspend-then-hibernate.service",
	} {
		t.Run(u, func(t *testing.T) {
			f := unitFake(map[string]string{"fine.service": show("fine.service", "fine.service", "enabled", "inactive")})
			o, _ := rootOpts(t, f)
			if code := Run([]string{"disable-unit", "fine.service", u}, o); code != ExitRefused {
				t.Fatalf("exit %d, want 4", code)
			}
			if len(f.queries)+len(f.streams) != 0 {
				t.Errorf("a denied name must be refused before touching systemctl: %q %q", f.queries, f.streams)
			}
		})
	}
}

func TestDisableUnitDenylistThroughAlias(t *testing.T) {
	// "mydm.service" is an alias whose real Id is sddm.service.
	f := unitFake(map[string]string{"mydm.service": show("sddm.service", "sddm.service mydm.service", "enabled", "inactive")})
	o, _ := rootOpts(t, f)
	if code := Run([]string{"disable-unit", "mydm.service"}, o); code != ExitRefused {
		t.Fatalf("exit %d, want 4", code)
	}
	if len(f.streams) != 0 {
		t.Errorf("ran %q", f.streams)
	}
}

func TestDisableUnitDBusActivationAliasIsNotDenied(t *testing.T) {
	// D-Bus activation aliases come both from the runtime Names and from
	// the [Install] Alias= that disabling would remove.
	for unit, alias := range map[string]string{
		"bluetooth.service":    "dbus-org.bluez.service",
		"avahi-daemon.service": "dbus-org.freedesktop.Avahi.service",
	} {
		for _, arg := range []string{unit, alias} {
			t.Run(arg, func(t *testing.T) {
				f := installFake(
					map[string]string{arg: show(unit, unit+" "+alias, "enabled", "inactive")},
					map[string]string{unit: "[Install]\nWantedBy=multi-user.target\nAlias=" + alias + "\n"},
				)
				f.on(unitCatArgv(alias), reply{out: "[Install]\nWantedBy=multi-user.target\nAlias=" + alias + "\n"})
				o, out := rootOpts(t, f)
				if code := Run([]string{"disable-unit", arg}, o); code != ExitOK {
					t.Fatalf("exit %d:\n%s", code, out)
				}
				if got := join(f.streams); !slices.Equal(got, []string{"/usr/bin/systemctl disable --now -- " + arg}) {
					t.Errorf("ran %q", got)
				}
			})
		}
	}
}

func TestDisableUnitDeniedRealUnitBehindHarmlessAlias(t *testing.T) {
	t.Run("D-Bus activation alias of a denied unit", func(t *testing.T) {
		// The literal name passes; its Id is systemd-logind.service.
		f := unitFake(map[string]string{"dbus-org.freedesktop.login1.service": show(
			"systemd-logind.service", "systemd-logind.service dbus-org.freedesktop.login1.service", "enabled", "inactive")})
		if msg := refusedWithoutRunning(t, f, "dbus-org.freedesktop.login1.service"); !strings.Contains(msg, "systemd-logind.service") {
			t.Errorf("error %q does not name the real unit", msg)
		}
	})
	t.Run("denied unit with a harmless [Install] alias", func(t *testing.T) {
		f := installFake(
			map[string]string{"firewalld.service": idle("firewalld.service")},
			map[string]string{"firewalld.service": "[Install]\nWantedBy=multi-user.target\nAlias=dbus-org.fedoraproject.FirewallD1.service\n"},
		)
		refusedWithoutRunning(t, f, "firewalld.service")
	})
	t.Run("denied unit asked for by its harmless alias", func(t *testing.T) {
		f := installFake(
			map[string]string{"dbus-org.fedoraproject.FirewallD1.service": show(
				"firewalld.service", "firewalld.service dbus-org.fedoraproject.FirewallD1.service", "enabled", "inactive")},
			map[string]string{"dbus-org.fedoraproject.FirewallD1.service": "[Install]\nAlias=dbus-org.fedoraproject.FirewallD1.service\n"},
		)
		refusedWithoutRunning(t, f, "dbus-org.fedoraproject.FirewallD1.service")
	})
	t.Run("the bus socket through Also=", func(t *testing.T) {
		f := installFake(
			map[string]string{"a.service": idle("a.service"), "dbus.socket": idle("dbus.socket")},
			map[string]string{"a.service": "[Install]\nAlso=dbus.socket\n"},
		)
		refusedWithoutRunning(t, f, "a.service")
	})
	t.Run("a unit claiming the bus alias", func(t *testing.T) {
		f := installFake(
			map[string]string{"mybus.service": idle("mybus.service")},
			map[string]string{"mybus.service": "[Install]\nAlias=dbus.service\n"},
		)
		refusedWithoutRunning(t, f, "mybus.service")
	})
}

func TestDisableUnitValidation(t *testing.T) {
	for _, bad := range []string{"foo", "foo.timer", "a b.service", "../x.service", "x.service;", "", strings.Repeat("a", 251) + ".service"} {
		t.Run(bad, func(t *testing.T) {
			f := newFake()
			o, _ := rootOpts(t, f)
			if code := Run([]string{"disable-unit", bad}, o); code != ExitUsage {
				t.Errorf("exit %d, want 2", code)
			}
			if len(f.queries)+len(f.streams) != 0 {
				t.Errorf("ran commands for invalid input")
			}
		})
	}
}

// installFake is unitFake plus `systemctl cat` output per unit.
func installFake(states, cats map[string]string) *fakeRunner {
	f := unitFake(states)
	for u, c := range cats {
		f.on(unitCatArgv(u), reply{out: c})
	}
	return f
}

func idle(u string) string { return show(u, u, "enabled", "inactive") }

// refusedWithoutRunning runs disable-unit, requires exit 4 with no
// systemctl disable run, and returns the result's error.
func refusedWithoutRunning(t *testing.T, f *fakeRunner, units ...string) string {
	t.Helper()
	o, out := rootOpts(t, f)
	if code := Run(append([]string{"disable-unit"}, units...), o); code != ExitRefused {
		t.Fatalf("exit %d, want 4:\n%s", code, out)
	}
	if len(f.streams) != 0 {
		t.Errorf("ran %q", f.streams)
	}
	return result(t, out)["error"].(string)
}

func TestDisableUnitRefusesSleepHooks(t *testing.T) {
	for _, target := range sleepUnits {
		t.Run("install "+target, func(t *testing.T) {
			// The runtime WantedBy property is empty (the target isn't
			// loaded); only the [Install] section says it.
			f := installFake(
				map[string]string{"hook.service": idle("hook.service")},
				map[string]string{"hook.service": "# /etc/systemd/system/hook.service\n[Unit]\nDescription=x\n\n[Install]\nWantedBy=" + target + "\n"},
			)
			if msg := refusedWithoutRunning(t, f, "hook.service"); !strings.Contains(msg, target) {
				t.Errorf("error %q does not name %s", msg, target)
			}
		})
	}
	t.Run("runtime RequiredBy", func(t *testing.T) {
		f := unitFake(map[string]string{"hook.service": idle("hook.service") + "RequiredBy=suspend.target\n"})
		refusedWithoutRunning(t, f, "hook.service")
	})
	t.Run("drop-in WantedBy with continuation", func(t *testing.T) {
		f := installFake(
			map[string]string{"hook.service": idle("hook.service")},
			map[string]string{"hook.service": "# /usr/lib/systemd/system/hook.service\n[Install]\nWantedBy=multi-user.target\n\n# /etc/systemd/system/hook.service.d/x.conf\n[Install]\nWantedBy=\\\n  hibernate.target\n"},
		)
		refusedWithoutRunning(t, f, "hook.service")
	})
	t.Run("only the [Install] section counts", func(t *testing.T) {
		f := installFake(
			map[string]string{"ok.service": idle("ok.service")},
			map[string]string{"ok.service": "[Unit]\nWantedBy=sleep.target\nAlso=sshd.socket\n[Install]\nWantedBy=multi-user.target\n"},
		)
		o, out := rootOpts(t, f)
		if code := Run([]string{"disable-unit", "ok.service"}, o); code != ExitOK {
			t.Fatalf("exit %d:\n%s", code, out)
		}
	})
}

func TestDisableUnitRefusesDeniedInstallAlias(t *testing.T) {
	// Disabling it would remove the display-manager.service alias symlink.
	f := installFake(
		map[string]string{"mydm.service": idle("mydm.service")},
		map[string]string{"mydm.service": "[Install]\nAlias=display-manager.service\n"},
	)
	refusedWithoutRunning(t, f, "mydm.service")
}

func TestDisableUnitAlso(t *testing.T) {
	t.Run("denied by prefix", func(t *testing.T) {
		f := installFake(
			map[string]string{"helper.service": idle("helper.service"), "sshd.socket": idle("sshd.socket")},
			map[string]string{"helper.service": "[Install]\nWantedBy=multi-user.target\nAlso=sshd.socket\n"},
		)
		if msg := refusedWithoutRunning(t, f, "helper.service"); !strings.Contains(msg, "Also=") || !strings.Contains(msg, "sshd.socket") {
			t.Errorf("error %q does not explain the Also=", msg)
		}
	})
	t.Run("denied by exact name of another type", func(t *testing.T) {
		f := installFake(
			map[string]string{"fw-ui.service": idle("fw-ui.service"), "firewalld.socket": idle("firewalld.socket")},
			map[string]string{"fw-ui.service": "[Install]\nAlso=firewalld.socket\n"},
		)
		refusedWithoutRunning(t, f, "fw-ui.service")
	})
	t.Run("denied through the Also unit's alias", func(t *testing.T) {
		f := installFake(
			map[string]string{"a.service": idle("a.service"), "b.service": show("b.service", "b.service gdm.service", "enabled", "active")},
			map[string]string{"a.service": "[Install]\nAlso=b.service\n"},
		)
		refusedWithoutRunning(t, f, "a.service")
	})
	t.Run("denied by the Also unit's sleep target", func(t *testing.T) {
		f := installFake(
			map[string]string{"a.service": idle("a.service"), "a-resume.service": idle("a-resume.service")},
			map[string]string{"a.service": "[Install]\nAlso=a-resume.service\n", "a-resume.service": "[Install]\nWantedBy=systemd-suspend.service\n"},
		)
		refusedWithoutRunning(t, f, "a.service")
	})
	t.Run("transitive", func(t *testing.T) {
		f := installFake(
			map[string]string{"a.service": idle("a.service"), "a.socket": idle("a.socket"), "ufw.service": idle("ufw.service")},
			map[string]string{"a.service": "[Install]\nAlso=a.socket\n", "a.socket": "[Install]\nAlso=ufw.service\n"},
		)
		refusedWithoutRunning(t, f, "a.service")
	})
	t.Run("specifier", func(t *testing.T) {
		f := installFake(
			map[string]string{"a@x.service": idle("a@x.service")},
			map[string]string{"a@x.service": "[Install]\nAlso=b@%i.service\n"},
		)
		refusedWithoutRunning(t, f, "a@x.service")
	})
	t.Run("allowed, and a cycle terminates", func(t *testing.T) {
		f := installFake(
			map[string]string{"avahi.service": idle("avahi.service"), "avahi.socket": show("avahi.socket", "avahi.socket", "enabled", "active")},
			map[string]string{"avahi.service": "[Install]\nWantedBy=multi-user.target\nAlso=avahi.socket\n", "avahi.socket": "[Install]\nWantedBy=sockets.target\nAlso=avahi.service\n"},
		)
		o, out := rootOpts(t, f)
		if code := Run([]string{"disable-unit", "avahi.service"}, o); code != ExitOK {
			t.Fatalf("exit %d:\n%s", code, out)
		}
		if got := join(f.streams); !slices.Equal(got, []string{"/usr/bin/systemctl disable --now -- avahi.service"}) {
			t.Errorf("ran %q", got)
		}
		if !slices.ContainsFunc(f.queries, func(q []string) bool { return slices.Equal(q, unitCatArgv("avahi.socket")) }) {
			t.Errorf("the Also= unit's [Install] was not read: %q", f.queries)
		}
	})
	t.Run("a missing Also unit is not read", func(t *testing.T) {
		f := installFake(
			map[string]string{"a.service": idle("a.service"), "gone.socket": "Id=gone.socket\nNames=gone.socket\nLoadState=not-found\n"},
			map[string]string{"a.service": "[Install]\nAlso=gone.socket\n"},
		)
		f.on(unitCatArgv("gone.socket"), reply{err: "No files found for gone.socket.", exit: 1})
		o, out := rootOpts(t, f)
		if code := Run([]string{"disable-unit", "a.service"}, o); code != ExitOK {
			t.Fatalf("exit %d:\n%s", code, out)
		}
	})
	t.Run("a cat failure fails", func(t *testing.T) {
		f := unitFake(map[string]string{"a.service": idle("a.service")})
		f.on(unitCatArgv("a.service"), reply{err: "boom", exit: 1})
		o, out := rootOpts(t, f)
		if code := Run([]string{"disable-unit", "a.service"}, o); code != ExitFailed {
			t.Fatalf("exit %d, want 1:\n%s", code, out)
		}
		if len(f.streams) != 0 {
			t.Errorf("ran %q", f.streams)
		}
	})
}

func TestDisableUnitOnlyReadsInstallOfUnitsItDisables(t *testing.T) {
	// A running unit is skipped, so its [Install] is irrelevant.
	f := unitFake(map[string]string{"busy.service": show("busy.service", "busy.service", "enabled", "active")})
	o, out := rootOpts(t, f)
	if code := Run([]string{"disable-unit", "busy.service"}, o); code != ExitOK {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	for _, q := range f.queries {
		if slices.Contains(q, "cat") {
			t.Errorf("read the [Install] of a skipped unit: %q", q)
		}
	}
}

func TestParseInstall(t *testing.T) {
	got := parseInstall("# /usr/lib/systemd/system/a.service\n[Unit]\nAlso=no.service\n[Install]\n; c\n" +
		"WantedBy=multi-user.target  graphical.target\nAlso = a.socket\n\n" +
		"# /etc/systemd/system/a.service.d/o.conf\n[Install]\nAlso=\nAlso=b.socket \\\n c.socket\n[Service]\nAlso=no2.service\n")
	if w := []string{"multi-user.target", "graphical.target"}; !slices.Equal(got["WantedBy"], w) {
		t.Errorf("WantedBy = %q, want %q", got["WantedBy"], w)
	}
	if w := []string{"a.socket", "b.socket", "c.socket"}; !slices.Equal(got["Also"], w) {
		t.Errorf("Also = %q, want %q (resets ignored, continuation joined)", got["Also"], w)
	}
}
