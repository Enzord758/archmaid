package ui

// #cgo pkg-config: gtk4
// #include <gdk/gdk.h>
// #include <glib-object.h>
// #include <string.h>
// #include <stdlib.h>
// #include <stdint.h>
//
// // Declared here, not through <gdk/wayland/gdkwayland.h>, which needs
// // wayland-client.h at build time; the symbol is in libgtk-4 (GTK 4.4 and
// // up, GDK built with Wayland).
// extern gboolean gdk_wayland_display_query_registry(GdkDisplay *display, const char *global);
//
// // archmaid_registry_has asks the Wayland compositor's registry for a
// // global: known is 0 when the display isn't a Wayland one.
// static int archmaid_registry_has(uintptr_t display, const char *global, int *known) {
// 	GdkDisplay *d = (GdkDisplay *)display;
// 	*known = 0;
// 	if (d == NULL || strcmp(G_OBJECT_TYPE_NAME(d), "GdkWaylandDisplay") != 0)
// 		return 0;
// 	*known = 1;
// 	return gdk_wayland_display_query_registry(d, global) ? 1 : 0;
// }
import "C"

import (
	"unsafe"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
)

// backgroundEffectGlobal is the Wayland protocol a compositor advertises
// when it can blur behind a window (ext-background-effect-v1). GTK 4.24
// binds it and, for a window whose CSS has a backdrop-filter, sends the
// blur region (glass.css); a compositor that gets none treats the client
// as one that doesn't want blur. GTK can't ask whether blur is switched on
// (Hyprland's decoration:blur): only whether the compositor can.
const backgroundEffectGlobal = "ext_background_effect_manager_v1"

// registryHas says whether the compositor advertises the global; known is
// false when the display isn't Wayland (X11, Broadway) and the registry
// can't be asked. A variable, so tests can fake both answers.
var registryHas = func() (has, known bool) {
	display := gdk.DisplayGetDefault()
	if display == nil {
		return false, false
	}
	cs := C.CString(backgroundEffectGlobal)
	defer C.free(unsafe.Pointer(cs))
	var k C.int
	h := C.archmaid_registry_has(C.uintptr_t(coreglib.InternObject(display).Native()), cs, &k)
	return h != 0, k != 0
}
