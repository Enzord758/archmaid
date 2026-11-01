import QtQuick

// Stand-in for DMS's PluginComponent: the properties the archmaid widget sets.
Item {
    property string layerNamespacePlugin: ""
    property int popoutWidth: 0
    property int popoutHeight: 0
    property int iconSize: 16
    property Component horizontalBarPill
    property Component verticalBarPill
    property Component popoutContent
}
