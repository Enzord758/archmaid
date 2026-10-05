pragma ComponentBehavior: Bound
import QtQuick
import Quickshell
import Quickshell.Io
import qs.Common
import qs.Widgets
import qs.Modules.Plugins
import "./state.js" as State

// archmaid bar widget. It only READS three small files under
// $XDG_STATE_HOME/archmaid (default ~/.local/state/archmaid):
//   last-run.json        written by archmaid after every run
//   audit-state.json     written by the AUR audit
//   updates-cache.json   written by the archmaid-updates-check user timer
// It never writes them, never calls sudo/pkexec and never parses logs. All the
// interpretation lives in state.js (tested by packaging/dms/dms_test.go).
// "Run Full Cleanup" only starts a UI; authentication happens there.
PluginComponent {
    id: root
    layerNamespacePlugin: "archmaid"

    readonly property string stateDir: {
        const xdg = Quickshell.env("XDG_STATE_HOME");
        const base = (xdg && xdg.startsWith("/")) ? xdg : (Quickshell.env("HOME") || "") + "/.local/state";
        return base + "/archmaid";
    }

    property var lastRun: null
    property var audit: null
    property var updates: null
    property double now: Date.now()
    property bool hasGui: false
    property bool hasXdgTerm: false
    property string launchError: ""

    readonly property var summary: State.summarize(lastRun, audit, updates, now)
    readonly property string level: summary.level

    readonly property color tint: {
        switch (level) {
        case "error":
            return Theme.error;
        case "warn":
            return Theme.warning;
        case "nodata":
            return Theme.surfaceVariantText;
        default:
            return Theme.surfaceText;
        }
    }
    readonly property string pillText: summary.updates !== null && summary.updates > 0 ? String(summary.updates) : ""

    popoutWidth: 340
    popoutHeight: 0

    function reloadAll() {
        now = Date.now();
        lastRunFile.reload();
        auditFile.reload();
        updatesFile.reload();
    }

    function probeTools() {
        Proc.runCommand("archmaid.which-gui", ["which", "archmaid-gui"], (out, code) => {
            root.hasGui = code === 0;
        }, 0, 3000, root);
        Proc.runCommand("archmaid.which-xdgterm", ["which", "xdg-terminal-exec"], (out, code) => {
            root.hasXdgTerm = code === 0;
        }, 0, 3000, root);
    }

    function runFullCleanup() {
        const plan = State.launchPlan(root.hasGui, Quickshell.env("TERMINAL") || "", root.hasXdgTerm);
        if (plan.error) {
            root.launchError = plan.error;
            return;
        }
        root.launchError = "";
        Quickshell.execDetached(plan.argv);
    }

    function openLogFolder() {
        Quickshell.execDetached(["xdg-open", root.stateDir]);
    }

    // A missing or unreadable file is "no data", never an error.
    FileView {
        id: lastRunFile
        path: root.stateDir + "/last-run.json"
        printErrors: false
        onLoaded: root.lastRun = State.parseLastRun(text())
        onLoadFailed: root.lastRun = null
    }
    FileView {
        id: auditFile
        path: root.stateDir + "/audit-state.json"
        printErrors: false
        onLoaded: root.audit = State.parseAudit(text())
        onLoadFailed: root.audit = null
    }
    FileView {
        id: updatesFile
        path: root.stateDir + "/updates-cache.json"
        printErrors: false
        onLoaded: root.updates = State.parseUpdates(text())
        onLoadFailed: root.updates = null
    }

    // The writers replace files by rename, so re-reading on a timer is
    // simpler and sturdier than watching inodes. Three small files per minute.
    Timer {
        interval: 60000
        running: true
        repeat: true
        triggeredOnStart: true
        onTriggered: root.reloadAll()
    }
    Component.onCompleted: root.probeTools()

    horizontalBarPill: Component {
        Row {
            spacing: Theme.spacingXS
            DankIcon {
                name: "cleaning_services"
                size: root.iconSize
                color: root.tint
                anchors.verticalCenter: parent.verticalCenter
            }
            StyledText {
                textFormat: Text.PlainText // state-file strings are data, never markup
                visible: root.pillText !== ""
                text: root.pillText
                font.pixelSize: Theme.fontSizeSmall
                color: root.tint
                anchors.verticalCenter: parent.verticalCenter
            }
        }
    }

    verticalBarPill: Component {
        Column {
            spacing: Theme.spacingXS
            DankIcon {
                name: "cleaning_services"
                size: root.iconSize
                color: root.tint
                anchors.horizontalCenter: parent.horizontalCenter
            }
            StyledText {
                textFormat: Text.PlainText // state-file strings are data, never markup
                visible: root.pillText !== ""
                text: root.pillText
                font.pixelSize: Theme.fontSizeSmall
                color: root.tint
                anchors.horizontalCenter: parent.horizontalCenter
            }
        }
    }

    popoutContent: Component {
        PopoutComponent {
            id: pop
            headerText: "archmaid"
            detailsText: root.level === "nodata" ? "No data yet. Run archmaid once." : ""
            showCloseButton: true

            Component.onCompleted: {
                root.reloadAll();
                root.probeTools();
            }

            Column {
                width: parent.width
                spacing: Theme.spacingS

                Repeater {
                    model: root.summary.issues
                    StyledText {
                        textFormat: Text.PlainText // state-file strings are data, never markup
                        required property var modelData
                        width: parent.width
                        text: modelData.text
                        wrapMode: Text.WordWrap
                        font.pixelSize: Theme.fontSizeMedium
                        color: (modelData.kind === "aur" || modelData.kind === "failed") ? Theme.error : Theme.warning
                    }
                }

                StyledText {
                    textFormat: Text.PlainText // state-file strings are data, never markup
                    visible: root.lastRun !== null
                    width: parent.width
                    wrapMode: Text.WordWrap
                    color: Theme.surfaceText
                    font.pixelSize: Theme.fontSizeMedium
                    text: root.lastRun ? ("Last run: " + root.lastRun.action + ", " + root.lastRun.outcome + ", " + State.ago(root.lastRun.finished, root.now)) : ""
                }

                StyledText {
                    textFormat: Text.PlainText // state-file strings are data, never markup
                    visible: root.summary.updates !== null
                    width: parent.width
                    color: Theme.surfaceText
                    font.pixelSize: Theme.fontSizeMedium
                    text: root.updates ? ("Pending updates: " + root.summary.updates + " (checked " + State.ago(root.updates.checked, root.now) + ")") : ""
                }

                StyledText {
                    textFormat: Text.PlainText // state-file strings are data, never markup
                    visible: root.audit !== null
                    width: parent.width
                    wrapMode: Text.WordWrap
                    color: Theme.surfaceText
                    font.pixelSize: Theme.fontSizeMedium
                    text: root.audit ? ("AUR audit: " + root.audit.packages + " packages" + (root.audit.checked !== null ? ", " + State.ago(root.audit.checked, root.now) : "")) : ""
                }

                Repeater {
                    model: root.audit ? root.audit.high.slice(0, 5) : []
                    StyledText {
                        textFormat: Text.PlainText // state-file strings are data, never markup
                        required property var modelData
                        width: parent.width
                        wrapMode: Text.WordWrap
                        color: Theme.error
                        font.pixelSize: Theme.fontSizeSmall
                        text: modelData.pkg + ": " + modelData.reason
                    }
                }

                StyledText {
                    textFormat: Text.PlainText // state-file strings are data, never markup
                    visible: root.launchError !== ""
                    width: parent.width
                    wrapMode: Text.WordWrap
                    color: Theme.warning
                    font.pixelSize: Theme.fontSizeSmall
                    text: root.launchError
                }

                Row {
                    spacing: Theme.spacingS
                    DankButton {
                        text: "Run Full Cleanup"
                        onClicked: {
                            root.runFullCleanup();
                            if (root.launchError === "" && pop.closePopout)
                                pop.closePopout();
                        }
                    }
                    DankButton {
                        text: "Log folder"
                        onClicked: root.openLogFolder()
                    }
                }
            }
        }
    }
}
