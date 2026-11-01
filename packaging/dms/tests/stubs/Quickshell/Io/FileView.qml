import QtQuick

// Stand-in for Quickshell.Io's FileView: reads nothing (the test sets the
// widget's parsed data itself), so the widget never touches a real file.
QtObject {
    property string path: ""
    property bool printErrors: true
    property bool watchChanges: false
    signal loaded
    signal loadFailed(var error)
    signal fileChanged
    function reload() {
    }
    function text() {
        return "";
    }
}
