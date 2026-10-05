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
    // The popout's three sections (state.js popout: strings and numbers).
    readonly property var view: State.popout(summary, now)

    // tone maps state.js's colour roles to the DMS theme.
    function tone(role) {
        switch (role) {
        case "primary":
            return Theme.primary;
        case "error":
            return Theme.error;
        case "warning":
            return Theme.warning;
        default:
            return Theme.surfaceVariantText;
        }
    }

    // A section's small uppercase heading, as the GUI's kickers.
    component Kicker: StyledText {
        textFormat: Text.PlainText // fixed strings, plain like every text here
        font.pixelSize: Theme.fontSizeSmall
        font.capitalization: Font.AllUppercase
        font.letterSpacing: 1.2
        color: Theme.surfaceVariantText
    }

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
    // simpler and sturdier than watching inodes. Three small files per minute
    // keep the bar pill current; the popout re-reads on every open too.
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

            // Every open shows the files as they are now. DMS loads this
            // content once and keeps it, so Component.onCompleted only
            // covers the first open; the popout (parentPopout, a
            // DankPopout) sets shouldBeVisible on every open.
            Component.onCompleted: {
                root.reloadAll();
                root.probeTools();
            }
            Connections {
                target: pop.parentPopout
                ignoreUnknownSignals: true
                function onShouldBeVisibleChanged() {
                    if (pop.parentPopout && pop.parentPopout.shouldBeVisible) {
                        root.reloadAll();
                        root.probeTools();
                    }
                }
            }

            // Workbench's sections, in its order: LAST RUN, UPDATES, AUR.
            // The chrome stays DMS's; numbers are mono, as the GUI's meters.
            Column {
                width: parent.width
                spacing: Theme.spacingM

                Column {
                    width: parent.width
                    spacing: Theme.spacingXS

                    Kicker {
                        text: "Last run"
                    }
                    StyledText {
                        textFormat: Text.PlainText // state-file strings are data, never markup
                        visible: root.view.lastRun === null
                        text: "No run recorded yet"
                        font.pixelSize: Theme.fontSizeMedium
                        color: Theme.surfaceVariantText
                    }
                    Row {
                        visible: root.view.lastRun !== null
                        width: parent.width
                        spacing: Theme.spacingS

                        StyledText {
                            textFormat: Text.PlainText // state-file strings are data, never markup
                            width: Math.min(implicitWidth, parent.width - chip.width - parent.spacing)
                            elide: Text.ElideRight
                            text: root.view.lastRun ? root.view.lastRun.action : ""
                            font.pixelSize: Theme.fontSizeLarge
                            font.weight: Font.Medium
                            color: Theme.surfaceText
                            anchors.verticalCenter: parent.verticalCenter
                        }
                        // The outcome tag: OK, FAILED, INCOMPLETE, ABORTED, CANCELLED.
                        Rectangle {
                            id: chip
                            readonly property color ink: root.tone(root.view.lastRun ? root.view.lastRun.tint : "muted")
                            width: tagText.implicitWidth + 2 * Theme.spacingS
                            height: tagText.implicitHeight + Theme.spacingXS
                            radius: Theme.spacingXS
                            color: Theme.withAlpha(ink, 0.16)
                            border.width: 1
                            border.color: Theme.withAlpha(ink, 0.5)
                            anchors.verticalCenter: parent.verticalCenter

                            StyledText {
                                textFormat: Text.PlainText // state-file strings are data, never markup
                                id: tagText
                                anchors.centerIn: parent
                                text: root.view.lastRun ? root.view.lastRun.tag : ""
                                font.pixelSize: Theme.fontSizeSmall
                                font.weight: Font.Medium
                                font.letterSpacing: 0.8
                                color: chip.ink
                            }
                        }
                    }
                    StyledText {
                        textFormat: Text.PlainText // state-file strings are data, never markup
                        visible: root.view.lastRun !== null
                        width: parent.width
                        wrapMode: Text.WordWrap
                        text: root.view.lastRun ? root.view.lastRun.stamp : ""
                        font.pixelSize: Theme.fontSizeMedium
                        color: Theme.surfaceText
                    }
                    Repeater {
                        model: root.view.lastRun ? root.view.lastRun.notes : []
                        StyledText {
                            textFormat: Text.PlainText // state-file strings are data, never markup
                            required property var modelData
                            width: parent.width
                            wrapMode: Text.WordWrap
                            text: modelData.text
                            font.pixelSize: Theme.fontSizeSmall
                            color: root.tone(modelData.tint)
                        }
                    }
                }

                Column {
                    width: parent.width
                    spacing: Theme.spacingXS

                    Kicker {
                        text: "Updates"
                    }
                    Row {
                        width: parent.width
                        spacing: Theme.spacingS

                        StyledText {
                            textFormat: Text.PlainText // state-file strings are data, never markup
                            visible: root.view.updates.count !== null
                            text: root.view.updates.count !== null ? String(root.view.updates.count) : ""
                            font.family: Theme.monoFontFamily
                            font.pixelSize: Theme.fontSizeXLarge
                            color: root.view.updates.count > 0 ? Theme.primary : Theme.surfaceText
                            anchors.verticalCenter: parent.verticalCenter
                        }
                        StyledText {
                            textFormat: Text.PlainText // state-file strings are data, never markup
                            text: root.view.updates.note
                            font.pixelSize: Theme.fontSizeMedium
                            color: root.view.updates.count !== null ? Theme.surfaceText : Theme.surfaceVariantText
                            anchors.verticalCenter: parent.verticalCenter
                        }
                    }
                }

                Column {
                    width: parent.width
                    spacing: Theme.spacingXS

                    Kicker {
                        text: "AUR"
                    }
                    Row {
                        width: parent.width
                        spacing: Theme.spacingS

                        StyledText {
                            textFormat: Text.PlainText // state-file strings are data, never markup
                            visible: root.view.aur.packages !== null
                            text: root.view.aur.packages !== null ? String(root.view.aur.packages) : ""
                            font.family: Theme.monoFontFamily
                            font.pixelSize: Theme.fontSizeXLarge
                            color: Theme.surfaceText
                            anchors.verticalCenter: parent.verticalCenter
                        }
                        StyledText {
                            textFormat: Text.PlainText // state-file strings are data, never markup
                            text: root.view.aur.note
                            font.pixelSize: Theme.fontSizeMedium
                            color: root.view.aur.packages !== null ? Theme.surfaceText : Theme.surfaceVariantText
                            anchors.verticalCenter: parent.verticalCenter
                        }
                    }
                    // The high findings, one row each: the package (mono) and why.
                    Repeater {
                        model: root.view.aur.high
                        Row {
                            required property var modelData
                            width: parent.width
                            spacing: Theme.spacingS

                            StyledText {
                                textFormat: Text.PlainText // state-file strings are data, never markup
                                id: pkgText
                                text: modelData.pkg
                                font.family: Theme.monoFontFamily
                                font.pixelSize: Theme.fontSizeSmall
                                color: Theme.error
                            }
                            StyledText {
                                textFormat: Text.PlainText // state-file strings are data, never markup
                                width: parent.width - pkgText.width - parent.spacing
                                wrapMode: Text.WordWrap
                                text: modelData.reason
                                font.pixelSize: Theme.fontSizeSmall
                                color: Theme.surfaceText
                            }
                        }
                    }
                    StyledText {
                        textFormat: Text.PlainText // state-file strings are data, never markup
                        visible: root.view.aur.more > 0
                        text: "+" + root.view.aur.more + " more in archmaid's audit"
                        font.pixelSize: Theme.fontSizeSmall
                        color: Theme.surfaceVariantText
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
                        // Secondary: Run Full Cleanup keeps DMS's primary button colours.
                        text: "Log folder"
                        backgroundColor: Theme.surfaceContainerHigh
                        textColor: Theme.surfaceText
                        onClicked: root.openLogFolder()
                    }
                }
            }
        }
    }
}
