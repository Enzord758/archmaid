pragma Singleton
import QtQuick

// Stand-in for DMS's Theme (only what the archmaid widget reads), so the
// widget's real QML can be rendered offscreen by tst_widget.qml.
QtObject {
    property bool isLightMode: false
    // DMS's blur state and popup transparency, and its corner radius.
    property bool blurLayersActive: false
    property real popupTransparency: 1.0
    property real cornerRadius: 12
    property color error: "#e57373"
    property color warning: "#ffb74d"
    property color primary: "#f0a6c8"
    property color surfaceText: "#e6e1e9"
    property color surfaceVariantText: "#9a95a3"
    property color surface: "#1c1a21"
    property color surfaceContainer: "#26232d"
    // A test sets this to make foregroundColor throw, as a DMS without it
    // (or a theme it chokes on) does.
    property bool foregroundBroken: false
    property color surfaceContainerHigh: "#2f2b37"
    property string monoFontFamily: "monospace"
    property int fontSizeSmall: 12
    property int fontSizeMedium: 14
    property int fontSizeLarge: 16
    property int fontSizeXLarge: 22
    property real spacingXS: 4
    property real spacingS: 8
    property real spacingM: 12
    // DMS blends a foreground layer's colour to a translucent one while it
    // blurs its layers, and leaves it as it is otherwise.
    function foregroundColor(c) {
        if (foregroundBroken)
            throw new Error("foregroundColor is not available");
        return blurLayersActive ? withAlpha(c, 0.5) : c;
    }
    function withAlpha(c, a) {
        return Qt.rgba(c.r, c.g, c.b, a);
    }
}
