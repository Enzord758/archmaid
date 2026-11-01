import QtQuick

Rectangle {
    property string text: ""
    property color backgroundColor: "pink"
    property color textColor: "black"
    signal clicked
    width: 140
    height: 36
    color: backgroundColor
}
