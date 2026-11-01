pragma Singleton
import QtQuick

// Stand-in for DMS's Proc: never runs anything.
QtObject {
    function runCommand(id, argv, cb, a, b, parent) {
    }
}
