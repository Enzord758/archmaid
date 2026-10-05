package ui

// #cgo pkg-config: gtk4
// #include <stdint.h>
// #include <stdlib.h>
// #include <gtk/gtk.h>
//
// // archmaid_save_png draws widget (a realized window) through its own
// // renderer and writes it to path as a PNG; 0 on success.
// static int archmaid_save_png(uintptr_t widget, const char *path) {
// 	GtkWidget *w = (GtkWidget *)widget;
// 	GtkNative *native = gtk_widget_get_native(w);
// 	if (native == NULL)
// 		return 1;
// 	GskRenderer *renderer = gtk_native_get_renderer(native);
// 	int width = gtk_widget_get_width(w), height = gtk_widget_get_height(w);
// 	if (renderer == NULL || width <= 0 || height <= 0)
// 		return 2;
// 	GdkPaintable *paintable = gtk_widget_paintable_new(w);
// 	GtkSnapshot *snapshot = gtk_snapshot_new();
// 	gdk_paintable_snapshot(paintable, GDK_SNAPSHOT(snapshot), width, height);
// 	GskRenderNode *node = gtk_snapshot_free_to_node(snapshot);
// 	g_object_unref(paintable);
// 	if (node == NULL)
// 		return 3;
// 	GdkTexture *texture = gsk_renderer_render_texture(renderer, node, NULL);
// 	gsk_render_node_unref(node);
// 	if (texture == NULL)
// 		return 4;
// 	gboolean ok = gdk_texture_save_to_png(texture, path);
// 	g_object_unref(texture);
// 	return ok ? 0 : 5;
// }
import "C"

import (
	"fmt"
	"unsafe"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
)

// savePNG writes widget (a realized window, as GTK draws it now) to path
// as a PNG: the headless harness's screenshots (shots_test.go). gotk4
// can't hand render nodes over (they aren't GObjects), so this goes
// through C. Nothing in the app calls it.
func savePNG(widget coreglib.Objector, path string) error {
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	if rc := C.archmaid_save_png(C.uintptr_t(coreglib.InternObject(widget).Native()), cpath); rc != 0 {
		return fmt.Errorf("screenshot %s: step %d failed", path, int(rc))
	}
	return nil
}
