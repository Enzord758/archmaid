import QtQuick

// Stand-in for DMS's StyledText with the defaults of the real one
// (DCommon/Widgets/StyledText.qml): PlainText, WordWrap, ElideRight,
// AlignVCenter. WordWrap beats elide, so a single-line text that does not set
// wrapMode itself wraps, as it does in the real bar.
Text {
    color: "white"
    textFormat: Text.PlainText
    wrapMode: Text.WordWrap
    elide: Text.ElideRight
    verticalAlignment: Text.AlignVCenter
}
