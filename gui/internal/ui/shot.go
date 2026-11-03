//go:build shots

package ui

// #cgo pkg-config: gtk4
// #include <stdint.h>
// #include <stdlib.h>
// #include <gtk/gtk.h>
//
// // archmaid_save_png draws widget (a realized window) with a cairo
// // renderer of its own and writes it to path as a PNG; 0 on success.
// // Not the window's renderer: the headless display (Broadway) renders
// // with its own, and cairo is what archmaid-gui renders with.
// static int archmaid_save_png(uintptr_t widget, const char *path) {
// 	GtkWidget *w = (GtkWidget *)widget;
// 	if (gtk_widget_get_native(w) == NULL)
// 		return 1;
// 	int width = gtk_widget_get_width(w), height = gtk_widget_get_height(w);
// 	if (width <= 0 || height <= 0)
// 		return 2;
// 	GskRenderer *renderer = gsk_cairo_renderer_new();
// 	if (!gsk_renderer_realize_for_display(renderer, gtk_widget_get_display(w), NULL)) {
// 		g_object_unref(renderer);
// 		return 2;
// 	}
// 	GdkPaintable *paintable = gtk_widget_paintable_new(w);
// 	GtkSnapshot *snapshot = gtk_snapshot_new();
// 	gdk_paintable_snapshot(paintable, GDK_SNAPSHOT(snapshot), width, height);
// 	GskRenderNode *node = gtk_snapshot_free_to_node(snapshot);
// 	g_object_unref(paintable);
// 	GdkTexture *texture = NULL;
// 	if (node != NULL) {
// 		texture = gsk_renderer_render_texture(renderer, node, NULL);
// 		gsk_render_node_unref(node);
// 	}
// 	gsk_renderer_unrealize(renderer);
// 	g_object_unref(renderer);
// 	if (node == NULL)
// 		return 3;
// 	if (texture == NULL)
// 		return 4;
// 	gboolean ok = gdk_texture_save_to_png(texture, path);
// 	g_object_unref(texture);
// 	return ok ? 0 : 5;
// }
//
// // archmaid_node_text serializes what GTK draws for widget (GSK's text
// // form of the render node) into a malloc'd string, or NULL.
// static char *archmaid_node_text(uintptr_t widget) {
// 	GtkWidget *w = (GtkWidget *)widget;
// 	int width = gtk_widget_get_width(w), height = gtk_widget_get_height(w);
// 	if (width <= 0 || height <= 0)
// 		return NULL;
// 	GdkPaintable *paintable = gtk_widget_paintable_new(w);
// 	GtkSnapshot *snapshot = gtk_snapshot_new();
// 	gdk_paintable_snapshot(paintable, GDK_SNAPSHOT(snapshot), width, height);
// 	GskRenderNode *node = gtk_snapshot_free_to_node(snapshot);
// 	g_object_unref(paintable);
// 	if (node == NULL)
// 		return NULL;
// 	GBytes *bytes = gsk_render_node_serialize(node);
// 	gsk_render_node_unref(node);
// 	gsize n = 0;
// 	const char *data = g_bytes_get_data(bytes, &n);
// 	char *out = g_strndup(data, n);
// 	g_bytes_unref(bytes);
// 	return out;
// }
import "C"

import (
	"fmt"
	"unsafe"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
)

// shotsBuilt: this test binary can take screenshots (built with -tags
// shots; shot_noshots_test.go is the other side).
const shotsBuilt = true

// savePNG writes widget (a realized window, as GTK draws it now) to path
// as a PNG: the headless harness's screenshots (shots_test.go). gotk4
// can't hand render nodes over (they aren't GObjects), so this goes
// through C. Built only with -tags shots (headless.sh's screenshot group
// and ARCHMAID_GUI_SHOTS), so the shipped binary carries none of it.
func savePNG(widget coreglib.Objector, path string) error {
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	if rc := C.archmaid_save_png(C.uintptr_t(coreglib.InternObject(widget).Native()), cpath); rc != 0 {
		return fmt.Errorf("screenshot %s: step %d failed", path, int(rc))
	}
	return nil
}

// nodeText is the render node of widget (a realized one, as GTK draws it
// now) in GSK's text form: the glass tests look in it for what a widget
// paints (the corner marks' 10 px strokes) without sampling pixels.
func nodeText(widget coreglib.Objector) (string, error) {
	c := C.archmaid_node_text(C.uintptr_t(coreglib.InternObject(widget).Native()))
	if c == nil {
		return "", fmt.Errorf("render node of the widget: none")
	}
	defer C.g_free(C.gpointer(c))
	return C.GoString(c), nil
}
