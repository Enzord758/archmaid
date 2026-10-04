package ui

// #cgo pkg-config: gtk4
// #include <stdint.h>
// #include <string.h>
// #include <gtk/gtk.h>
//
// static const char *archmaid_editable_text(uintptr_t e) {
// 	return gtk_editable_get_text((GtkEditable *)e);
// }
// static void archmaid_editable_clear(uintptr_t e) {
// 	gtk_editable_set_text((GtkEditable *)e, "");
// }
import "C"

import (
	"unsafe"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
)

// editableBytes copies an editable's text (a password entry's) into a
// fresh byte slice the caller zeroes after use. It reads the C string
// directly: gotk4's Text() would make a Go string, which can't be wiped.
// An AdwPasswordEntryRow keeps the text in GTK's password buffer
// (non-pageable, wiped as it is freed).
func editableBytes(e coreglib.Objector) []byte {
	p := C.archmaid_editable_text(C.uintptr_t(coreglib.InternObject(e).Native()))
	if p == nil {
		return nil
	}
	n := int(C.strlen(p))
	b := make([]byte, n)
	copy(b, unsafe.Slice((*byte)(unsafe.Pointer(p)), n))
	return b
}

// wipeEditable empties an editable; GTK's password buffer wipes the old
// text as it frees it.
func wipeEditable(e coreglib.Objector) {
	C.archmaid_editable_clear(C.uintptr_t(coreglib.InternObject(e).Native()))
}
