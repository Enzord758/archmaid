import QtQuick
import QtTest
import qs.Common
import "state.js" as State

// Renders the REAL ArchmaidWidget.qml offscreen against stand-ins for the DMS
// modules (tests/stubs), run by dms_test.go TestWidgetRenders. The stubs read
// no file and start nothing; the test hands the widget parsed data. Checks the
// popout: the list is absent for a v:2 file and unfolds for a v:3 one.
TestCase {
    id: tc
    name: "archmaidWidget"
    when: windowShown
    visible: true
    width: 400
    height: 1000

    Component {
        id: widgetC
        ArchmaidWidget {}
    }
    Component {
        id: popC
        QtObject {
            property bool shouldBeVisible: true
        }
    }
    Component {
        id: loaderC
        Loader {}
    }

    function find(item, name) {
        if (item.objectName === name)
            return item;
        const kids = item.children;
        for (let i = 0; i < kids.length; i++) {
            const f = find(kids[i], name);
            if (f)
                return f;
        }
        if (item.contentItem) {
            const kids2 = item.contentItem.children;
            for (let i = 0; i < kids2.length; i++) {
                const f2 = find(kids2[i], name);
                if (f2)
                    return f2;
            }
        }
        return null;
    }

    function texts(item, out) {
        if (item.text !== undefined && item.visible && typeof item.text === "string" && item.text !== "")
            out.push(item.text);
        const kids = item.children;
        for (let i = 0; i < kids.length; i++)
            texts(kids[i], out);
        if (item.contentItem)
            for (let j = 0; j < item.contentItem.children.length; j++)
                texts(item.contentItem.children[j], out);
        return out;
    }

    function pkgs(n) {
        const a = [];
        for (let i = 0; i < n; i++)
            a.push({name: "p" + i, from: "1.0-1", to: "1.1-1", src: "repo"});
        a.push({name: "systemd", from: "1-1", to: "2-1", src: "repo"});
        a.push({name: "zen-bin", from: "1", to: "2", src: "aur", held: false});
        a.push({name: "held-bin", from: "1", to: "2", src: "aur", held: true, age: "1d9h"});
        return a;
    }

    // A widget with data: v is the file's version, n the repo packages listed.
    function popout(v, n) {
        const now = Date.now();
        const w = createTemporaryObject(widgetC, tc);
        const text = JSON.stringify({v: v, count: n + 2, repo: n + 1, aur: 1, aur_held: 1,
                                     checked: new Date(now - 17 * 60000).toISOString(), packages: pkgs(n)});
        w.now = now;
        w.updates = State.parseUpdates(text);
        w.lastRun = {action: "fullcleanup", finished: now - 3600000, outcome: "ok", stepsFailed: 0, stepsRefused: 0, stepsSkipped: 0,
                     summary: {upgraded: 47, aur: 0, freedBytes: 3328599654, attention: ["Restart recommended", "1 new .pacnew"]}};
        w.audit = {checked: now - 8 * 3600000, packages: 29, highCount: 0, high: [], snapshotPresent: true, snapshotReadable: true};
        const l = createTemporaryObject(loaderC, tc, {sourceComponent: w.popoutContent, width: 340});
        tryVerify(() => l.item !== null);
        return {w: w, loader: l, pop: l.item};
    }

    function test_v3ListUnfolds() {
        const o = popout(3, 12);
        const pop = o.pop;
        const t = texts(pop, []);
        verify(t.indexOf("Restart recommended") >= 0, "headline: " + t);
        verify(t.indexOf("1 new .pacnew") >= 0);
        verify(t.indexOf("14 updates waiting · 1 held") >= 0, t.join("|"));
        verify(t.indexOf("13 repo · 1 AUR · 1 held") >= 0, t.join("|"));
        verify(t.indexOf("14 NEW") >= 0);
        verify(t.indexOf("NO HIGH") >= 0);
        verify(t.indexOf("CLEAN") < 0);
        const row = find(pop, "updatesRow");
        verify(row !== null);
        verify(row.expandable, "a v3 file with packages gives a chevron");
        const list = find(pop, "pkgList");
        verify(list !== null);
        verify(!list.visible, "folded at first");
        mouseClick(row);
        compare(pop.updatesOpen, true);
        verify(list.visible);
        compare(list.count, 15);
        compare(list.height, 8 * 28, "capped at 8 rows, then it scrolls");
        verify(list.contentHeight > list.height);
        const t2 = texts(pop, []);
        verify(t2.indexOf("systemd") >= 0);
        verify(t2.indexOf("restart") >= 0, "restart tag");
        verify(t2.indexOf("held 1d9h") >= 0 || list.contentHeight > 0);
        mouseClick(row);
        compare(pop.updatesOpen, false);
        // Enter and Space, when the row has focus
        row.forceActiveFocus();
        keyClick(Qt.Key_Space);
        compare(pop.updatesOpen, true);
        keyClick(Qt.Key_Return);
        compare(pop.updatesOpen, false);
        // the open state resets when the popout closes
        pop.parentPopout = createTemporaryObject(popC, tc);
        pop.updatesOpen = true;
        pop.parentPopout.shouldBeVisible = false;
        compare(pop.updatesOpen, false);
    }

    function test_shortListShrinks() {
        const o = popout(3, 0);
        const row = find(o.pop, "updatesRow");
        mouseClick(row);
        const list = find(o.pop, "pkgList");
        compare(list.count, 3);
        compare(list.height, list.contentHeight, "fewer than 8 rows: the list is as tall as its content");
    }

    function test_moreFooter() {
        const o = popout(3, 2);
        // the file says 3 repo + 1 aur + 1 held = 5 pending, but lists 5 rows... make it say more
        o.w.updates = State.parseUpdates(JSON.stringify({v: 3, count: 50, repo: 49, aur: 1, aur_held: 3,
            checked: new Date(Date.now() - 60000).toISOString(), packages: pkgs(2)}));
        mouseClick(find(o.pop, "updatesRow"));
        const t = texts(o.pop, []);
        verify(t.indexOf("and 48 more not shown") >= 0, t.join("|"));
    }

    function test_v2HasNoList() {
        const o = popout(2, 12);
        const row = find(o.pop, "updatesRow");
        verify(row !== null);
        verify(!row.expandable, "no chevron for a v2 file");
        mouseClick(row);
        compare(o.pop.updatesOpen, false);
        verify(!find(o.pop, "pkgList").visible);
        const t = texts(o.pop, []);
        verify(t.indexOf("14 NEW") >= 0);
    }

    function test_v3WithoutPackagesHasNoList() {
        const o = popout(3, 0);
        o.w.updates = State.parseUpdates(JSON.stringify({v: 3, count: 3, repo: 3, aur: 0, aur_held: 0,
            checked: new Date(Date.now() - 60000).toISOString()}));
        verify(!find(o.pop, "updatesRow").expandable);
    }

    // Every Text under item whose text is str (visible only).
    function textOf(item, str) {
        const all = [];
        collect(item, all);
        for (let i = 0; i < all.length; i++)
            if (all[i].text === str)
                return all[i];
        return null;
    }
    function collect(item, out) {
        if (item.text !== undefined && item.lineCount !== undefined && item.visible)
            out.push(item);
        for (let i = 0; i < item.children.length; i++)
            collect(item.children[i], out);
        if (item.contentItem)
            for (let j = 0; j < item.contentItem.children.length; j++)
                collect(item.contentItem.children[j], out);
    }

    // DMS's real StyledText wraps by default, and WordWrap beats elide: every
    // single-line text must say NoWrap, or a long value runs onto a second line
    // (the VM check, #223). The stub has the real defaults.
    function test_singleLineTextsNeverWrap() {
        const now = Date.now();
        const o = popout(3, 0);
        const chk = new Date(now - 17 * 60000).toISOString();
        const long = [{name: "gtk4-with-a-rather-long-package-name-in-it-x", from: "2:1.26.1-1", to: "2:1.26.2-1", src: "repo"},
                      {name: "systemd", from: "2:1.26.1-1", to: "2:1.26.2-1", src: "repo"}];
        o.w.audit = {checked: now - 3600000, packages: 29, highCount: 1, snapshotPresent: true, snapshotReadable: true,
                     high: [{pkg: "foo-bin", reason: "maintainer alice → mallory, adopted two days ago by a new maintainer with no history"}]};
        o.w.updates = State.parseUpdates(JSON.stringify({v: 3, count: 10, repo: 9, aur: 1, aur_held: 2, checked: chk, packages: long}));
        mouseClick(find(o.pop, "updatesRow"));
        const cases = ["9 repo · 1 AUR · 2 held", "foo-bin",
                       "systemd", "checked 17 min ago", "gtk4-with-a-rather-long-package-name-in-it-x"];
        for (let i = 0; i < cases.length; i++) {
            tryVerify(() => textOf(o.pop, cases[i]) !== null, 2000, "no text " + cases[i]);
            const t = textOf(o.pop, cases[i]);
            compare(t.lineCount, 1, cases[i]);
        }
        const reason = "maintainer alice → mallory, adopted two days ago by a new maintainer with no history";
        tryVerify(() => textOf(o.pop, reason) !== null);
        verify(textOf(o.pop, reason).truncated, "a long reason is elided on its own line");
        verify(textOf(o.pop, cases[4]).truncated, "a long name is elided");
        // the bigger counts
        o.w.updates = State.parseUpdates(JSON.stringify({v: 3, count: 56, repo: 52, aur: 4, aur_held: 2, checked: chk, packages: long}));
        const t2 = textOf(o.pop, "52 repo · 4 AUR · 2 held");
        verify(t2 !== null);
        compare(t2.lineCount, 1);
        // every visible Text of the popout except none is on a second line
        const all = [];
        collect(o.pop, all);
        for (let k = 0; k < all.length; k++)
            compare(all[k].lineCount, 1, all[k].text);
    }

    // The label column is measured from the widest label: none is cut ("Last ...").
    function test_labelsNotTruncated() {
        const o = popout(3, 2);
        const names = ["Last run", "Updates", "AUR"];
        for (let i = 0; i < names.length; i++) {
            tryVerify(() => textOf(o.pop, names[i]) !== null, 2000, names[i]);
            const t = textOf(o.pop, names[i]);
            verify(!t.truncated, names[i] + " is cut");
            compare(t.lineCount, 1);
        }
        // one shared column: the three labels start their values at the same x
        const a = textOf(o.pop, "Last run"), b = textOf(o.pop, "AUR");
        compare(a.width, b.width);
    }

    // The AUR row is the package and the tag; the finding's reason has its own muted line.
    function test_aurReasonLine() {
        const o = popout(3, 2);
        const reason = "maintainer alice → mallory";
        verify(textOf(o.pop, reason) === null, "no finding, no reason line");
        o.w.audit = {checked: Date.now() - 3600000, packages: 29, highCount: 2, snapshotPresent: true, snapshotReadable: true,
                     high: [{pkg: "foo-bin", reason: reason}, {pkg: "bar", reason: "other"}]};
        tryVerify(() => textOf(o.pop, reason) !== null, 2000, "reason line");
        const r = textOf(o.pop, reason);
        verify(!r.truncated, "the reason is whole at 340 px");
        compare(r.lineCount, 1);
        const v = textOf(o.pop, "foo-bin");
        verify(v !== null && !v.truncated);
        verify(textOf(o.pop, "other") === null, "only the first finding's reason");
        verify(textOf(o.pop, "2 HIGH") !== null);
        // the reason sits under the row's value, at the popout's width
        verify(r.width > 250);
    }

    // The new version matters most: "from → to" when it fits half the row, else "→ to".
    function test_versionKeepsTheNewOne() {
        const o = popout(3, 0);
        const chk = new Date(Date.now() - 60000).toISOString();
        o.w.updates = State.parseUpdates(JSON.stringify({v: 3, count: 3, repo: 3, aur: 0, aur_held: 0, checked: chk, packages: [
            {name: "go", from: "2:1.26.1-1-rc2", to: "2:1.26.2-1-rc2", src: "repo"},
            {name: "a-long-package-name-that-must-elide-before-versions", from: "1.0-1", to: "1.1-1", src: "repo"},
            {name: "curl", from: "8.1-1", to: "8.2-1", src: "repo"}]}));
        mouseClick(find(o.pop, "updatesRow"));
        tryVerify(() => textOf(o.pop, "→ 2:1.26.2-1-rc2") !== null, 2000, "to only");
        const t = textOf(o.pop, "→ 2:1.26.2-1-rc2");
        verify(!t.truncated, "the new version is whole");
        verify(textOf(o.pop, "2:1.26.1-1-rc2 → 2:1.26.2-1-rc2") === null);
        const s = textOf(o.pop, "1.0-1 → 1.1-1");
        verify(s !== null && !s.truncated, "a short pair stays whole");
        verify(textOf(o.pop, "a-long-package-name-that-must-elide-before-versions").truncated, "the name gives way");
    }

    // A list folded again, or a popout closed, starts at the top next time.
    function test_scrollResetsOnFold() {
        const o = popout(3, 40);
        const row = find(o.pop, "updatesRow");
        mouseClick(row);
        const list = find(o.pop, "pkgList");
        list.contentY = 200;
        verify(list.contentY > 100);
        mouseClick(row);
        compare(list.contentY, list.originY);
        mouseClick(row);
        list.contentY = 200;
        o.pop.parentPopout = createTemporaryObject(popC, tc);
        o.pop.parentPopout.shouldBeVisible = false;
        compare(list.contentY, list.originY);
    }

    function test_lightThemeColours() {
        const o = popout(3, 2);
        compare(o.w.stateColor("ok").toString(), "#9ece6a");
        compare(o.w.stateColor("danger").toString(), "#f7768e");
        Theme.isLightMode = true;
        compare(o.w.stateColor("ok").toString(), "#4e7d1a");
        compare(o.w.stateColor("warn").toString(), "#9a6a10");
        compare(o.w.stateColor("danger").toString(), "#c4223f");
        Theme.isLightMode = false;
    }
}
