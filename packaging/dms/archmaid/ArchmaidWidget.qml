pragma ComponentBehavior: Bound
import QtQuick
import Quickshell
import Quickshell.Io
import qs.Common
import qs.Widgets
import qs.Modules.Plugins
import "./state.js" as State

// archmaid bar widget. It only READS three small files under
// ~/.local/state/archmaid:
//   last-run.json        written by archmaid after every run
//   audit-state.json     written by the AUR audit
//   updates-cache.json   written by the archmaid-updates-check user timer
// It never writes them, never calls sudo/pkexec and never parses logs. All the
// interpretation lives in state.js (tested by packaging/dms/dms_test.go).
// "Run Full Cleanup" only starts a UI; authentication happens there.
PluginComponent {
    id: root
    layerNamespacePlugin: "archmaid"

    // Always ~/.local/state/archmaid, where archmaid, the GUI and the
    // updates timer write (XDG_STATE_HOME is not honoured by any of them).
    readonly property string stateDir: (Quickshell.env("HOME") || "") + "/.local/state/archmaid"

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

    // The state colours are fixed Tokyo Night constants (ok, warn, danger), a
    // darker set on a light theme (Theme.isLightMode, DMS's own flag); "muted"
    // and everything else come from the DMS palette.
    function stateColor(tone) {
        const light = Theme.isLightMode;
        switch (tone) {
        case "ok":
            return light ? "#3d6114" : "#9ece6a";
        case "warn":
            return light ? "#75510c" : "#e0af68";
        case "neutral":
            return Theme.surfaceText;
        case "danger":
            return light ? "#a91d36" : "#f7768e";
        default:
            return Theme.surfaceVariantText;
        }
    }
    readonly property color markColor: Theme.withAlpha(Theme.surfaceText, 0.45)
    readonly property color ruleColor: Theme.withAlpha(Theme.surfaceText, 0.12)

    // A section's small uppercase heading, as the GUI's kickers.
    component Kicker: StyledText {
        textFormat: Text.PlainText // fixed strings, plain like every text here
        wrapMode: Text.NoWrap
        font.pixelSize: Theme.fontSizeSmall
        font.capitalization: Font.AllUppercase
        font.letterSpacing: 1.2
        color: Theme.surfaceVariantText
    }

    // An outlined mono tag at the end of a row: "OK", "10 NEW", "restart".
    component Tag: Rectangle {
        id: tg
        property string text: ""
        property color ink: Theme.surfaceVariantText
        width: tagLabel.implicitWidth + 2 * Theme.spacingS
        height: tagLabel.implicitHeight + Theme.spacingXS
        color: "transparent"
        border.width: 1
        border.color: tg.ink

        StyledText {
            textFormat: Text.PlainText // state-file strings are data, never markup
            wrapMode: Text.NoWrap
            id: tagLabel
            anchors.centerIn: parent
            text: tg.text
            font.family: Theme.monoFontFamily
            font.pixelSize: Theme.fontSizeSmall
            color: tg.ink
        }
    }

    // One compact row: label, value (elided), end tag. With expandable it is
    // a button: click, or Enter/Space when it has focus, emits toggled; a
    // chevron shows the state.
    component StatRow: Item {
        id: sr
        property var row: ({ label: "", value: "", tag: "", tone: "muted" })
        property color ink: Theme.surfaceVariantText
        property bool expandable: false
        property bool expanded: false
        // The shared label column (measured from the widest label, see pop.labelW)
        property real labelWidth: 54
        // An optional muted line under the row (the AUR finding's reason)
        property string note: ""
        signal toggled

        width: parent.width
        height: 38 + (note !== "" ? 20 : 0)
        activeFocusOnTab: expandable
        Keys.onPressed: event => {
            if (sr.expandable && (event.key === Qt.Key_Return || event.key === Qt.Key_Enter || event.key === Qt.Key_Space)) {
                sr.toggled();
                event.accepted = true;
            }
        }

        Rectangle {
            anchors.fill: parent
            visible: sr.activeFocus
            color: "transparent"
            border.width: 1
            border.color: Theme.primary
        }
        MouseArea {
            anchors.fill: parent
            enabled: sr.expandable
            cursorShape: sr.expandable ? Qt.PointingHandCursor : Qt.ArrowCursor
            onClicked: sr.toggled()
        }
        Row {
            id: line
            anchors.top: parent.top
            anchors.left: parent.left
            anchors.right: parent.right
            height: 38
            anchors.leftMargin: 2
            anchors.rightMargin: 2
            spacing: Theme.spacingXS + 2

            StyledText {
                textFormat: Text.PlainText // fixed strings, plain like every text here
                wrapMode: Text.NoWrap
                width: sr.labelWidth
                elide: Text.ElideRight
                text: sr.row.label
                font.pixelSize: Theme.fontSizeMedium
                color: Theme.surfaceVariantText
                anchors.verticalCenter: parent.verticalCenter
            }
            StyledText {
                textFormat: Text.PlainText // state-file strings are data, never markup
                wrapMode: Text.NoWrap
                width: Math.max(0, line.width - sr.labelWidth - (chev.visible ? chev.width + line.spacing : 0) - (rowTag.visible ? rowTag.width + line.spacing : 0) - line.spacing)
                elide: Text.ElideRight
                text: sr.row.value
                font.pixelSize: Theme.fontSizeMedium
                color: Theme.surfaceText
                anchors.verticalCenter: parent.verticalCenter
            }
            Tag {
                id: rowTag
                visible: sr.row.tag !== ""
                text: sr.row.tag
                ink: sr.ink
                anchors.verticalCenter: parent.verticalCenter
            }
            DankIcon {
                id: chev
                visible: sr.expandable
                name: "chevron_right"
                size: Theme.fontSizeLarge
                color: Theme.surfaceVariantText
                rotation: sr.expanded ? 90 : 0
                anchors.verticalCenter: parent.verticalCenter
                Behavior on rotation {
                    NumberAnimation {
                        duration: 120
                    }
                }
            }
        }
        StyledText {
            textFormat: Text.PlainText // state-file strings are data, never markup
            wrapMode: Text.NoWrap
            visible: sr.note !== ""
            x: 2
            y: 36
            width: parent.width - 4
            elide: Text.ElideRight
            text: sr.note
            font.pixelSize: Theme.fontSizeSmall
            color: Theme.surfaceVariantText
        }
        Rectangle {
            anchors.left: parent.left
            anchors.right: parent.right
            anchors.bottom: parent.bottom
            height: 1
            color: Theme.withAlpha(Theme.surfaceText, 0.12)
        }
    }

    popoutWidth: 340
    popoutHeight: 0

    // Every read starts from the path: the files are replaced by rename
    // (mktemp + mv), and a FileView that failed once (the file was missing at
    // login, or caught in the rename window) must not stay failed. Dropping the
    // path and setting it again makes each view open the file afresh, whatever
    // inode it had; reload() then reads it.
    property bool filesOn: true
    property int retries: 0

    function reloadFiles() {
        filesOn = false;
        filesOn = true;
        lastRunFile.reload();
        auditFile.reload();
        updatesFile.reload();
    }

    function reloadAll() {
        now = Date.now();
        retries = 0;
        reloadFiles();
    }

    // A failed read is retried while the file is absent: a FileView cannot
    // watch a file whose directory does not exist yet (the updates timer's
    // first write creates both, after DMS started), so only a fresh open sees
    // it. The first two tries are 2 s apart, the rest 4 s, at most 12 per 60 s
    // cycle (the timer's tick resets the budget); the three views fail together
    // and count as one try. A file that is present never reaches this, so the
    // steady state costs nothing.
    function loadFailedRetry() {
        if (retries < 12 && !retryTimer.running) {
            retries++;
            retryTimer.interval = retries <= 2 ? 2000 : 4000;
            retryTimer.start();
        }
    }
    Timer {
        id: retryTimer
        onTriggered: root.reloadFiles()
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
        path: root.filesOn ? root.stateDir + "/last-run.json" : ""
        printErrors: false
        onLoaded: root.lastRun = State.parseLastRun(text())
        onLoadFailed: {
            if (path === "")
                return;
            root.lastRun = null;
            root.loadFailedRetry();
        }
        watchChanges: true
        onFileChanged: reload()
    }
    FileView {
        id: auditFile
        path: root.filesOn ? root.stateDir + "/audit-state.json" : ""
        printErrors: false
        onLoaded: root.audit = State.parseAudit(text())
        onLoadFailed: {
            if (path === "")
                return;
            root.audit = null;
            root.loadFailedRetry();
        }
        watchChanges: true
        onFileChanged: reload()
    }
    FileView {
        id: updatesFile
        path: root.filesOn ? root.stateDir + "/updates-cache.json" : ""
        printErrors: false
        onLoaded: root.updates = State.parseUpdates(text())
        onLoadFailed: {
            if (path === "")
                return;
            root.updates = null;
            root.loadFailedRetry();
        }
        watchChanges: true
        onFileChanged: reload()
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
                wrapMode: Text.NoWrap
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
                wrapMode: Text.NoWrap
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
            detailsText: ""
            showCloseButton: true

            // The Updates row's list is unfolded. It resets whenever the
            // popout closes (the popout is kept alive between opens) and
            // when the list goes away.
            property bool updatesOpen: false
            readonly property var upList: root.view.updateList
            readonly property bool listOpen: updatesOpen && upList !== null
            // A folded list (or a closed popout) starts at the top next time.
            onListOpenChanged: {
                if (!listOpen) {
                    pkgView.positionViewAtBeginning();
                    pkgView.contentY = pkgView.originY;
                }
            }
            // The list's model is filled only when its content changed, so the
            // 60 s refresh does not throw the scroll position away.
            property string listSig: ""

            function syncList() {
                const l = pop.upList;
                const sig = l ? l.items.map(i => i.section + "|" + i.name + "|" + i.from + "|" + i.to + "|" + i.tag).join("\n") : "";
                if (sig === pop.listSig)
                    return;
                pop.listSig = sig;
                listModel.clear();
                if (l) {
                    for (let i = 0; i < l.items.length; i++) {
                        const it = l.items[i];
                        listModel.append({
                            "section": it.section,
                            "name": it.name,
                            "from": it.from,
                            "to": it.to,
                            "tag": it.tag,
                            "restart": it.restart
                        });
                    }
                }
            }
            onUpListChanged: syncList()

            // Every open shows the files as they are now. DMS loads this
            // content once and keeps it, so Component.onCompleted only
            // covers the first open; the popout (parentPopout, a
            // DankPopout) sets shouldBeVisible on every open.
            Component.onCompleted: {
                syncList();
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
                    } else {
                        pop.updatesOpen = false;
                    }
                }
            }

            ListModel {
                id: listModel
            }

            // The rows' label column is as wide as the widest label in the
            // real font (DMS's, not a guessed number) plus a small gap.
            readonly property real labelW: Math.ceil(Math.max(probeA.implicitWidth, probeB.implicitWidth, probeC.implicitWidth)) + Theme.spacingXS
            StyledText {
                id: probeA
                textFormat: Text.PlainText // fixed strings, plain like every text here
                wrapMode: Text.NoWrap
                visible: false
                elide: Text.ElideRight
                text: root.view.rows.lastRun.label
                font.pixelSize: Theme.fontSizeMedium
            }
            StyledText {
                id: probeB
                textFormat: Text.PlainText // fixed strings, plain like every text here
                wrapMode: Text.NoWrap
                visible: false
                elide: Text.ElideRight
                text: root.view.rows.updates.label
                font.pixelSize: Theme.fontSizeMedium
            }
            StyledText {
                id: probeC
                textFormat: Text.PlainText // fixed strings, plain like every text here
                wrapMode: Text.NoWrap
                visible: false
                elide: Text.ElideRight
                text: root.view.rows.aur.label
                font.pixelSize: Theme.fontSizeMedium
            }

            // Draft A: a hero with the state in words, the run's numbers,
            // three compact rows, the buttons. Text elides, never wraps.
            Column {
                width: parent.width
                spacing: Theme.spacingM

                // The hero: the only box with corner marks.
                Rectangle {
                    id: heroBox
                    width: parent.width
                    height: heroCol.implicitHeight + 2 * Theme.spacingM
                    color: Theme.surfaceContainer
                    border.width: 1
                    border.color: Theme.withAlpha(Theme.surfaceText, 0.12)

                    Repeater {
                        model: 4
                        Item {
                            id: corner
                            required property int index
                            readonly property bool atRight: index % 2 === 1
                            readonly property bool atBottom: index >= 2
                            width: 10
                            height: 10
                            x: atRight ? heroBox.width - width : 0
                            y: atBottom ? heroBox.height - height : 0
                            Rectangle {
                                width: 10
                                height: 1
                                y: corner.atBottom ? 9 : 0
                                color: root.markColor
                            }
                            Rectangle {
                                width: 1
                                height: 10
                                x: corner.atRight ? 9 : 0
                                color: root.markColor
                            }
                        }
                    }

                    Column {
                        id: heroCol
                        x: Theme.spacingM
                        y: Theme.spacingM
                        width: parent.width - 2 * Theme.spacingM
                        spacing: Theme.spacingXS

                        Kicker {
                            width: parent.width
                            elide: Text.ElideRight
                            text: root.view.hero.kicker
                        }
                        StyledText {
                            textFormat: Text.PlainText // state-file strings are data, never markup
                            wrapMode: Text.NoWrap
                            width: parent.width
                            elide: Text.ElideRight
                            text: root.view.hero.headline
                            font.pixelSize: Theme.fontSizeXLarge
                            font.weight: Font.Bold
                            color: root.stateColor(root.view.hero.tone)
                        }
                        StyledText {
                            textFormat: Text.PlainText // state-file strings are data, never markup
                            wrapMode: Text.NoWrap
                            visible: root.view.hero.sub !== ""
                            width: parent.width
                            elide: Text.ElideRight
                            text: root.view.hero.sub
                            font.pixelSize: Theme.fontSizeMedium
                            color: Theme.surfaceVariantText
                        }
                        Repeater {
                            model: root.view.hero.attention
                            Row {
                                id: attRow
                                required property string modelData
                                width: heroCol.width
                                spacing: Theme.spacingXS
                                DankIcon {
                                    id: attIcon
                                    name: "warning"
                                    size: Theme.fontSizeMedium
                                    color: root.stateColor("warn")
                                    anchors.verticalCenter: parent.verticalCenter
                                }
                                StyledText {
                                    textFormat: Text.PlainText // state-file strings are data, never markup
                                    wrapMode: Text.NoWrap
                                    width: attRow.width - attIcon.width - attRow.spacing
                                    elide: Text.ElideRight
                                    text: attRow.modelData
                                    font.pixelSize: Theme.fontSizeMedium
                                    color: root.stateColor("warn")
                                    anchors.verticalCenter: parent.verticalCenter
                                }
                            }
                        }
                    }
                }

                // The run's numbers: "47 upgraded · 3.1 GiB freed".
                StyledText {
                    textFormat: Text.PlainText // state-file strings are data, never markup
                    wrapMode: Text.NoWrap
                    visible: root.view.lastRun !== null && root.view.lastRun.summaryLine !== ""
                    width: parent.width
                    elide: Text.ElideRight
                    text: root.view.lastRun ? root.view.lastRun.summaryLine : ""
                    font.family: Theme.monoFontFamily
                    font.pixelSize: Theme.fontSizeMedium
                    color: Theme.surfaceText
                }

                Column {
                    width: parent.width
                    spacing: 0

                    Rectangle {
                        width: parent.width
                        height: 1
                        color: root.ruleColor
                    }
                    StatRow {
                        row: root.view.rows.lastRun
                        labelWidth: pop.labelW
                        ink: root.stateColor(root.view.rows.lastRun.tone)
                    }
                    StatRow {
                        id: updRow
                        objectName: "updatesRow"
                        row: root.view.rows.updates
                        labelWidth: pop.labelW
                        ink: root.stateColor(root.view.rows.updates.tone)
                        expandable: pop.upList !== null
                        expanded: pop.listOpen
                        onToggled: pop.updatesOpen = !pop.updatesOpen
                    }

                    // The unfolded list: read-only, nothing in it opens anything.
                    Column {
                        visible: pop.listOpen
                        width: parent.width
                        spacing: Theme.spacingS
                        topPadding: Theme.spacingS
                        bottomPadding: Theme.spacingS

                        StyledText {
                            textFormat: Text.PlainText // state-file strings are data, never markup
                            wrapMode: Text.NoWrap
                            width: parent.width
                            elide: Text.ElideRight
                            text: pop.upList ? pop.upList.checked : ""
                            font.pixelSize: Theme.fontSizeSmall
                            color: Theme.surfaceVariantText
                        }
                        ListView {
                            id: pkgView
                            objectName: "pkgList"
                            readonly property int rowH: 28
                            width: parent.width
                            height: Math.min(contentHeight, 8 * rowH)
                            clip: true
                            interactive: contentHeight > height
                            boundsBehavior: Flickable.StopAtBounds
                            model: listModel
                            section.property: "section"
                            section.criteria: ViewSection.FullString
                            section.labelPositioning: ViewSection.InlineLabels | ViewSection.CurrentLabelAtStart
                            section.delegate: Rectangle {
                                id: secBox
                                required property string section
                                width: pkgView.width
                                height: pkgView.rowH
                                color: Theme.surfaceContainer
                                Kicker {
                                    anchors.left: parent.left
                                    anchors.right: parent.right
                                    anchors.verticalCenter: parent.verticalCenter
                                    elide: Text.ElideRight
                                    text: secBox.section
                                }
                            }
                            delegate: Item {
                                id: pkgRow
                                required property string name
                                required property string from
                                required property string to
                                required property string tag
                                required property bool restart
                                width: pkgView.width
                                height: pkgView.rowH

                                // The version column may take up to half the row. The new
                                // version matters most: when "from \u2192 to" does not fit,
                                // the column shows "\u2192 to".
                                readonly property real verMax: Math.floor(width / 2)
                                readonly property bool fullFits: verFull.implicitWidth <= verMax
                                StyledText {
                                    id: verFull
                                    textFormat: Text.PlainText // state-file strings are data, never markup
                                    wrapMode: Text.NoWrap
                                    visible: false
                                    elide: Text.ElideRight
                                    text: pkgRow.from + " \u2192 " + pkgRow.to
                                    font.family: Theme.monoFontFamily
                                    font.pixelSize: Theme.fontSizeSmall
                                }

                                Row {
                                    anchors.fill: parent
                                    spacing: Theme.spacingS

                                    StyledText {
                                        textFormat: Text.PlainText // state-file strings are data, never markup
                                        wrapMode: Text.NoWrap
                                        width: Math.max(0, parent.width - verText.width - tagBox.width - 2 * parent.spacing)
                                        elide: Text.ElideRight
                                        text: pkgRow.name
                                        font.pixelSize: Theme.fontSizeMedium
                                        color: Theme.surfaceText
                                        anchors.verticalCenter: parent.verticalCenter
                                    }
                                    StyledText {
                                        id: verText
                                        width: Math.min(implicitWidth, pkgRow.verMax)
                                        elide: Text.ElideRight
                                        textFormat: Text.PlainText // state-file strings are data, never markup
                                        wrapMode: Text.NoWrap
                                        text: pkgRow.fullFits ? pkgRow.from + " \u2192 " + pkgRow.to : "\u2192 " + pkgRow.to
                                        font.family: Theme.monoFontFamily
                                        font.pixelSize: Theme.fontSizeSmall
                                        color: Theme.surfaceVariantText
                                        anchors.verticalCenter: parent.verticalCenter
                                    }
                                    Item {
                                        id: tagBox
                                        width: pkgRow.tag !== "" ? listTag.width : 0
                                        height: parent.height
                                        Tag {
                                            id: listTag
                                            visible: pkgRow.tag !== ""
                                            text: pkgRow.tag
                                            ink: pkgRow.restart ? root.stateColor("warn") : Theme.surfaceVariantText
                                            anchors.verticalCenter: parent.verticalCenter
                                        }
                                    }
                                }
                            }
                        }
                        StyledText {
                            textFormat: Text.PlainText // state-file strings are data, never markup
                            wrapMode: Text.NoWrap
                            visible: pop.upList !== null && pop.upList.more > 0
                            width: parent.width
                            elide: Text.ElideRight
                            text: pop.upList ? "and " + pop.upList.more + " more not shown" : ""
                            font.pixelSize: Theme.fontSizeSmall
                            color: Theme.surfaceVariantText
                        }
                    }

                    StatRow {
                        row: root.view.rows.aur
                        labelWidth: pop.labelW
                        note: root.view.rows.aur.reason
                        ink: root.stateColor(root.view.rows.aur.tone)
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
