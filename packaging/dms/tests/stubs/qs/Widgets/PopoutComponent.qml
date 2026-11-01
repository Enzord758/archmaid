import QtQuick

// Stand-in for DMS's PopoutComponent: a 340 px wide frame whose children go
// into a content Column below the header.
Item {
    id: frame
    property string headerText: ""
    property string detailsText: ""
    property bool showCloseButton: false
    property var parentPopout: null
    property var closePopout: null
    default property alias content: body.data
    width: 340
    implicitHeight: body.implicitHeight + 40
    height: implicitHeight

    Item {
        id: body
        x: 16
        y: 40
        width: parent.width - 32
        implicitHeight: childrenRect.height
    }
}
