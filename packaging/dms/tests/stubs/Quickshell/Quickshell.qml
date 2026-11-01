pragma Singleton
import QtQuick

// Stand-in for the Quickshell singleton: no environment, nothing started.
QtObject {
    property var started: []
    function env(name) {
        return "";
    }
    function execDetached(argv) {
        started.push(argv);
    }
}
