import QtQuick
import QtTest
import "state.js" as State
import "fixtures.js" as F

// Run by dms_test.go TestStateJS, which generates fixtures.js from the real Go
// writers and copies state.js and this file beside it. Run it by hand through
// `go test ./packaging/dms/`.
TestCase {
    name: "archmaidState"

    readonly property double t0: Date.parse(F.fx.t0)
    readonly property double day: 86400000

    function sum(last, aud, upd, now) {
        return State.summarize(State.parseLastRun(last), State.parseAudit(aud), State.parseUpdates(upd), now);
    }
    function kinds(s) {
        return s.issues.map(i => i.kind).join(",");
    }

    function test_writersParse() {
        const r = State.parseLastRun(F.fx.lastOK);
        verify(r !== null);
        compare(r.outcome, "ok");
        compare(r.action, "fullcleanup");
        compare(r.finished, t0);
        compare(r.stepsFailed, 0);
        const a = State.parseAudit(F.fx.auditHigh);
        compare(a.highCount, 2);
        compare(a.high[0].pkg, "foo");
        verify(a.high[0].reason.indexOf("mallory") >= 0);
        compare(a.packages, 27);
        compare(a.snapshotPresent, true);
        compare(State.parseAudit(F.fx.auditClean).highCount, 0);
        compare(State.parseUpdates(F.fx.updates3).count, 3);
        compare(State.parseUpdates(F.fx.updatesNone).count, 0);
    }

    // Not authorised: only a cancelled run with steps_refused > 0. The bar's
    // level is "warn" for it and for a plain cancel alike (the chip tells them
    // apart); the words are the Overview's and the Run page's, singular and
    // plural.
    function test_notAuthorisedEdges() {
        const na3 = sum(F.fx.lastNA3, null, null, t0);
        compare(na3.level, "warn");
        compare(kinds(na3), "notauthorised");
        compare(na3.issues[0].text, "Last run not authorised · nothing ran (3 steps not authorised)");
        compare(sum(F.fx.lastCancelled, null, null, t0).level, na3.level);
        // an older archmaid's record has no steps_refused, nor a damaged one a count: a plain cancel
        compare(State.parseLastRun(F.fx.lastCancelledAbsent).stepsRefused, 0);
        const bad = ["lastCancelledAbsent", "lastCancelledStr", "lastCancelledNeg", "lastCancelledFrac"];
        for (let i = 0; i < bad.length; i++) {
            const s = sum(F.fx[bad[i]], null, null, t0);
            compare(kinds(s), "cancelled", bad[i]);
            const p = State.popout(s, t0);
            compare(p.lastRun.tag, "CANCELLED", bad[i]);
            compare(p.lastRun.tint, "muted", bad[i]);
            compare(p.lastRun.notes.length, 0, bad[i]);
        }
        // steps_refused next to a failure or an abort is said too (the GUI's words)
        const fs = sum(F.fx.lastFailedRefused, null, null, t0);
        compare(fs.issues[0].text, "Last run failed (1 step) \u00b7 2 steps not authorised");
        compare(sum(F.fx.lastFailedRefused5, null, null, t0).issues[0].text, "Last run failed (1 step) \u00b7 5 steps not authorised");
        compare(sum(F.fx.lastFailed2Refused1, null, null, t0).issues[0].text, "Last run failed (2 steps) \u00b7 1 step not authorised");
        const f = State.popout(fs, t0);
        compare(f.lastRun.tag, "FAILED");
        compare(f.lastRun.tint, "error");
        compare(f.lastRun.notes[0].text, "Last run failed (1 step) \u00b7 2 steps not authorised");
        const as = sum(F.fx.lastAbortedRefused, null, null, t0);
        compare(kinds(as), "aborted");
        compare(as.issues[0].text, "Last run aborted \u00b7 2 steps not authorised");
        const a = State.popout(as, t0);
        compare(a.lastRun.tag, "ABORTED");
        compare(a.lastRun.tint, "warning");
        compare(State.outcomeTag("incomplete", 2), "INCOMPLETE");
        compare(State.outcomeTag("ok", 5), "OK");
        compare(State.outcomeTint("failed", 5), "error");
        // the count is optional: none is a plain cancel
        compare(State.outcomeTag("cancelled"), "CANCELLED");
        compare(State.outcomeTint("cancelled"), "muted");
        compare(State.outcomeTag("cancelled", 0), "CANCELLED");
        compare(State.outcomeTag("cancelled", 1), "NOT AUTHORISED");
        compare(State.outcomeTint("cancelled", 1), "warning");
        // the popout's note for it is a warning, like the incomplete one
        compare(State.popout(na3, t0).lastRun.notes.map(n => n.tint), ["warning"]);
    }

    function test_neutralOnBadInput() {
        const bad = [undefined, null, "", "{", "[]", "null", "42", F.fx.lastTorn, F.fx.lastV2, '{"v":1}', '{"v":"1","finished":"2026-10-04T10:00:00Z"}', '{"v":1,"finished":"yesterday"}'];
        for (let i = 0; i < bad.length; i++) {
            compare(State.parseLastRun(bad[i]), null, "lastRun " + i);
            compare(State.parseAudit(bad[i]), null, "audit " + i);
            compare(State.parseUpdates(bad[i]), null, "updates " + i);
        }
        compare(State.parseAudit('{"v":1,"high":"x"}'), null);
        compare(State.parseUpdates('{"v":1,"count":-1,"checked":"2026-10-04T10:00:00Z"}'), null);
        compare(State.parseUpdates('{"v":1,"count":1.5,"checked":"2026-10-04T10:00:00Z"}'), null);
        compare(State.parseLastRun(F.fx.lastDry), null);
        const s = State.summarize(null, null, null, t0);
        compare(s.level, "nodata");
        compare(s.updates, null);
    }

    function test_unknownFieldsAndOutcome() {
        compare(State.parseLastRun(F.fx.lastUnknownField).outcome, "ok");
        const odd = State.parseLastRun('{"v":1,"finished":"2026-10-04T10:00:00Z","outcome":"brand-new"}');
        compare(odd.outcome, "unknown");
        const a = State.parseAudit('{"v":1,"high":[{"pkg":"x"},7,null,{"reason":"no pkg"}],"extra":true}');
        compare(a.highCount, 1);
        compare(a.high[0].reason, "");
    }

    function test_levels() {
        compare(sum(F.fx.lastOK, F.fx.auditClean, F.fx.updates3, t0 + 1000).level, "ok");
        const f = sum(F.fx.lastFailed, F.fx.auditClean, null, t0);
        compare(f.level, "error");
        compare(kinds(f), "failed");
        compare(f.issues[0].text, "Last run failed (2 steps)");
        compare(sum(F.fx.lastFailed1, null, null, t0).issues[0].text, "Last run failed (1 step)");
        const h = sum(F.fx.lastOK, F.fx.auditHigh, null, t0);
        compare(h.level, "error");
        compare(h.issues[0].text, "2 AUR warnings");
        const inc = sum(F.fx.lastIncomplete, null, null, t0);
        compare(inc.level, "warn");
        compare(kinds(inc), "incomplete");
        verify(inc.issues[0].text.indexOf("(2)") >= 0);
        compare(sum(F.fx.lastAborted, null, null, t0).level, "warn");
        compare(sum(F.fx.lastCancelled, null, null, t0).level, "warn");
        compare(sum(F.fx.lastCancelled, null, null, t0).issues[0].text, "Last run cancelled, nothing ran");
        // every step refused: cancelled with steps_refused, said as the GUI says it
        const na = sum(F.fx.lastNotAuthorised, null, null, t0);
        compare(na.level, "warn");
        compare(kinds(na), "notauthorised");
        compare(na.issues[0].text, "Last run not authorised \u00b7 nothing ran (1 step not authorised)");
        // a discovery that found nothing to do is a clean run
        const nf = sum(F.fx.lastNothing, null, null, t0);
        compare(State.parseLastRun(F.fx.lastNothing).outcome, "nothing-found");
        compare(nf.level, "ok");
        compare(kinds(nf), "");
        const nfp = State.popout(nf, t0);
        compare(nfp.lastRun.tag, "NOTHING FOUND");
        compare(nfp.lastRun.tint, "primary");
        // audit-only (never ran a cleanup) is not an alarm
        compare(sum(undefined, F.fx.auditClean, undefined, t0).level, "ok");
    }

    function test_staleness() {
        compare(sum(F.fx.lastOK, null, null, t0 + 14 * day).level, "ok");
        const s = sum(F.fx.lastOK, null, null, t0 + 15 * day);
        compare(s.level, "warn");
        compare(kinds(s), "stale");
        compare(s.issues[0].text, "No cleanup for 15 days");
        // errors sort before warnings
        compare(kinds(sum(F.fx.lastFailed, null, null, t0 + 20 * day)), "failed,stale");
        // a clock behind the record is not "stale" and not an error
        compare(sum(F.fx.lastOK, null, null, t0 - 5 * day).level, "ok");
    }

    function test_updatesAge() {
        const u = State.parseUpdates(F.fx.updates3);
        const when = u.checked;
        compare(State.summarize(null, null, u, when + 1000).updates, 3);
        compare(State.summarize(null, null, u, when + 4 * day).updates, null);
        compare(State.summarize(null, null, State.parseUpdates(F.fx.updatesNone), when).updates, 0);
    }

    function test_ago() {
        compare(State.ago(t0, t0 + 5000), "just now");
        compare(State.ago(t0, t0 + 10 * 60000), "10 min ago");
        compare(State.ago(t0, t0 + 3 * 3600000), "3 h ago");
        // the hour starts at 60 min, as people read it ("60 MIN AGO" read oddly)
        compare(State.ago(t0, t0 + 59 * 60000), "59 min ago");
        compare(State.ago(t0, t0 + 3599000), "59 min ago");
        compare(State.ago(t0, t0 + 60 * 60000), "1 h ago");
        compare(State.ago(t0, t0 + 63 * 60000), "1 h ago");
        compare(State.ago(t0, t0 + 119 * 60000), "1 h ago");
        compare(State.ago(t0, t0 + 120 * 60000), "2 h ago");
        compare(State.ago(t0, t0 + 12 * day), "12 days ago");
        compare(State.ago(t0, t0 + 36 * 3600000), "1 day ago");
        compare(State.ago(t0, t0 - 1000), "just now");
    }

    function test_outcomeTagAndTint() {
        const want = [["ok", "OK", "primary"], ["failed", "FAILED", "error"], ["incomplete", "INCOMPLETE", "warning"],
                      ["aborted", "ABORTED", "warning"], ["cancelled", "CANCELLED", "muted"], ["unknown", "UNKNOWN", "muted"]];
        for (let i = 0; i < want.length; i++) {
            compare(State.outcomeTag(want[i][0]), want[i][1], want[i][0]);
            compare(State.outcomeTint(want[i][0]), want[i][2], want[i][0]);
        }
        // Every outcome the parser keeps has its own tag.
        for (let i = 0; i < State.KNOWN_OUTCOMES.length; i++)
            verify(State.outcomeTag(State.KNOWN_OUTCOMES[i]) !== "UNKNOWN", State.KNOWN_OUTCOMES[i]);
        compare(State.outcomeTag("constructor"), "UNKNOWN");
        compare(State.outcomeTint(undefined), "muted");
    }

    function test_actionLabel() {
        const ids = F.fx.runnable.split(",");
        verify(ids.length >= 7, F.fx.runnable);
        for (let i = 0; i < ids.length; i++)
            verify(State.actionLabel(ids[i]) !== ids[i], "no name for the runnable action " + ids[i]);
        compare(State.actionLabel("fullcleanup"), "Full Cleanup");
        compare(State.actionLabel("clearcaches"), "Clear caches");
        // A Packages run has no menu entry; the Go side names it too.
        compare(State.actionLabel("packages"), F.fx.packagesLabel);
        // Records from before Clear caches replaced the two cache actions.
        compare(State.actionLabel("usercache"), "Clear user cache");
        compare(State.actionLabel("wipecache"), "Delete entire package cache");
        compare(State.actionLabel("brand-new"), "brand-new");
        compare(State.actionLabel(""), "Unknown action");
        compare(State.actionLabel("toString"), "toString");
    }

    function test_stamp() {
        const then = new Date(2026, 9, 5, 0, 45).getTime(); // a Monday, local time
        compare(State.stamp(then, then + 17 * 3600000), "Mon 5 Oct, 00:45 · 17 h ago");
        compare(State.stamp(then, then + 2 * day), "Mon 5 Oct, 00:45 · 2 days ago");
        const eve = new Date(2025, 11, 31, 23, 59).getTime();
        compare(State.stamp(eve, new Date(2026, 0, 1, 8, 0).getTime()), "Wed 31 Dec 2025, 23:59 · 8 h ago");
        const morning = new Date(2026, 2, 9, 7, 5).getTime();
        compare(State.stamp(morning, morning), "Mon 9 Mar, 07:05 · just now");
    }

    function test_popout() {
        const ok = State.popout(sum(F.fx.lastOK, F.fx.auditHigh, F.fx.updates3, t0 + 2 * 3600000), t0 + 2 * 3600000);
        compare(ok.lastRun.action, "Full Cleanup");
        compare(ok.lastRun.tag, "OK");
        compare(ok.lastRun.tint, "primary");
        verify(ok.lastRun.stamp.endsWith(" · 2 h ago"), ok.lastRun.stamp);
        compare(ok.lastRun.notes.length, 0);
        compare(ok.updates.count, 3);
        verify(ok.updates.note.startsWith("3 repo · AUR unknown · checked "), ok.updates.note);
        compare(ok.aur.packages, 27);
        compare(ok.aur.note, "packages · checked 2 h ago");
        compare(ok.aur.high.length, 2);
        compare(ok.aur.high[0].pkg, "foo");
        compare(ok.aur.more, 0);

        // A failed run, past the stale mark: both notes, the AUR count stays in the AUR section.
        const late = t0 + 20 * day;
        const f = State.popout(sum(F.fx.lastFailed, F.fx.auditHigh, null, late), late);
        compare(f.lastRun.tag, "FAILED");
        compare(f.lastRun.tint, "error");
        compare(f.lastRun.notes.map(n => n.text), ["Last run failed (2 steps)", "No cleanup for 20 days"]);
        compare(f.lastRun.notes.map(n => n.tint), ["error", "warning"]);
        compare(f.updates.count, null);
        compare(f.updates.note, "Not checked yet");

        const c = State.popout(sum(F.fx.lastCancelled, null, null, t0), t0);
        compare(c.lastRun.tag, "CANCELLED");
        compare(c.lastRun.tint, "muted");
        compare(c.lastRun.notes.length, 0);
        const na = State.popout(sum(F.fx.lastNotAuthorised, null, null, t0), t0);
        compare(na.lastRun.tag, "NOT AUTHORISED");
        compare(na.lastRun.tint, "warning");
        compare(na.lastRun.notes.map(n => n.text), ["Last run not authorised \u00b7 nothing ran (1 step not authorised)"]);
        compare(c.aur.packages, null);
        compare(c.aur.note, "No audit yet");

        const inc = State.popout(sum(F.fx.lastIncomplete, null, null, t0), t0);
        compare(inc.lastRun.tint, "warning");
        compare(inc.lastRun.notes.length, 1);
        compare(inc.lastRun.notes[0].tint, "warning");

        // No record: no LAST RUN; an old updates count is "not checked for".
        const u = State.parseUpdates(F.fx.updates3);
        const old = State.popout(State.summarize(null, null, u, u.checked + 5 * day), u.checked + 5 * day);
        compare(old.lastRun, null);
        compare(old.updates.count, null);
        compare(old.updates.note, "Not checked for 5 days");
        const none = State.popout(State.summarize(null, null, State.parseUpdates(F.fx.updatesNone), t0), t0);
        compare(none.updates.count, 0);
        verify(none.updates.note.startsWith("up to date · checked "), none.updates.note);

        // Five findings listed, the rest counted; one package is singular.
        const many = State.popout(sum(null, F.fx.auditMany, null, t0), t0);
        compare(many.aur.high.length, 5);
        compare(many.aur.more, 2);
        compare(many.aur.note, "package · checked just now");

        // Nothing readable at all: every section says so.
        const empty = State.popout(State.summarize(null, null, null, t0), t0);
        compare(empty.lastRun, null);
        compare(empty.updates.note, "Not checked yet");
        compare(empty.aur.note, "No audit yet");
    }

    function test_updatesSplit() {
        // v:2 from the real script: repo 3, AUR 2 eligible + 2 held (count excludes held).
        const aur = State.parseUpdates(F.fx.updatesAur);
        compare(aur.count, 5);
        compare(aur.repo, 3);
        compare(aur.aur, 2);
        compare(aur.aurHeld, 2);
        // yay missing or failed: aur null, the repo count stands.
        const noAur = State.parseUpdates(F.fx.updatesNoAur);
        compare(noAur.count, 3);
        compare(noAur.aur, null);
        compare(noAur.repo, 3);
        // v:1 (older installs): count only.
        const v1 = State.parseUpdates(F.fx.updatesV1);
        compare(v1.count, 3);
        compare(v1.repo, null);
        compare(v1.aur, null);
        // Malformed optional fields read as unknown, not as a rejected file.
        const odd = State.parseUpdates('{"v":2,"count":4,"repo":"x","aur":-1,"aur_held":1.5,"checked":"2026-10-04T10:00:00Z"}');
        compare(odd.count, 4);
        compare(odd.repo, null);
        compare(odd.aur, null);
    }

    function test_updatesNote() {
        const at = State.parseUpdates(F.fx.updatesAur).checked + 5 * 60000;
        const note = fx => State.popout(State.summarize(null, null, State.parseUpdates(fx), at), at).updates;
        // The total (the bar pill) is repo + eligible AUR; held ones are not in it.
        compare(State.summarize(null, null, State.parseUpdates(F.fx.updatesAur), at).updates, 5);
        compare(note(F.fx.updatesAur).count, 5);
        compare(note(F.fx.updatesAur).note, "3 repo · 2 AUR (2 held, <72h) · checked 5 min ago");
        // Only held: count 0 and the note still says what is held.
        compare(note(F.fx.updatesAurHeld).count, 0);
        compare(note(F.fx.updatesAurHeld).note, "0 repo · 0 AUR (1 held, <72h) · checked 5 min ago");
        compare(note(F.fx.updatesNoAur).note, "3 repo · AUR unknown · checked 5 min ago");
        compare(note(F.fx.updatesNone).note, "up to date · checked 5 min ago");
        compare(note(F.fx.updatesNoYay).note, "0 repo · AUR unknown · checked 5 min ago");
        compare(note(F.fx.updatesV1).note, "pending · checked 5 min ago");
        // No held part when nothing is held.
        const clean = '{"v":2,"count":12,"repo":9,"aur":3,"aur_held":0,"checked":"' + new Date(at - 5 * 60000).toISOString() + '"}';
        compare(note(clean).note, "9 repo · 3 AUR · checked 5 min ago");
    }

    // The run report's summary object (added within v1 of last-run.json): the
    // widget reads old files (none), new ones from the real Go writer, and
    // damaged ones without throwing; the existing decisions don't change.
    function test_runSummary() {
        // An older archmaid's record has none.
        compare(State.parseLastRun(F.fx.lastOK).summary, null);
        compare(State.popout(sum(F.fx.lastOK, null, null, t0), t0).lastRun.summary, null);
        // ... and so no new line: the popout shows nothing for it.
        compare(State.popout(sum(F.fx.lastOK, null, null, t0), t0).lastRun.summaryLine, "");
        compare(State.popout(sum(F.fx.lastOK, null, null, t0), t0).lastRun.warnLine, "");
        // A new record: what the Go writer put there.
        const s = State.parseLastRun(F.fx.lastSummary);
        verify(s !== null);
        compare(s.outcome, "ok");
        compare(s.summary.upgraded, 47);
        compare(s.summary.aur, 6);
        compare(s.summary.freedBytes, 3328599654);
        compare(s.summary.attention, ["Restart recommended", "1 new .pacnew"]);
        const p = State.popout(sum(F.fx.lastSummary, null, null, t0), t0);
        compare(p.lastRun.summary.upgraded, 47);
        // The popout's two lines: the numbers, and the warning only when the
        // run left something that needs the user.
        compare(p.lastRun.summaryLine, "47 upgraded \u00b7 3.1 GiB freed");
        compare(p.lastRun.warnLine, "\u26a0 Restart recommended \u00b7 1 new .pacnew");
        const q = State.popout(sum(F.fx.lastSummaryQuiet, null, null, t0), t0).lastRun;
        compare(q.summaryLine, "6 upgraded \u00b7 410 MiB freed");
        compare(q.warnLine, "", "a quiet run has no warning line");
        compare(p.lastRun.tag, "OK");
        compare(sum(F.fx.lastSummary, null, null, t0).level, "ok");
        // A run with an empty attention list (written as []).
        compare(State.parseLastRun(F.fx.lastSummaryQuiet).summary.attention, []);
        // Damaged: the record still parses, the summary degrades.
        const bad = ["lastSummaryStr", "lastSummaryArray", "lastSummaryNull"];
        for (let i = 0; i < bad.length; i++) {
            const r = State.parseLastRun(F.fx[bad[i]]);
            verify(r !== null, bad[i]);
            compare(r.outcome, "ok", bad[i]);
            compare(r.summary, null, bad[i]);
        }
        const odd = State.parseLastRun(F.fx.lastSummaryOdd).summary;
        compare(odd.upgraded, 0);
        compare(odd.aur, 0);
        compare(odd.freedBytes, 0);
        compare(odd.attention, ["fine"]);
        // At most MAX_ATTENTION items of at most 60 characters.
        const many = State.parseLastRun(F.fx.lastSummaryMany).summary;
        compare(many.attention.length, 4);
        compare(many.attention[0].length, 60);
    }

    // humanBytes writes sizes as the Go side's report.HumanBytes does.
    function test_humanBytes() {
        const cases = [[0, "0 B"], [1023, "1023 B"], [1024, "1 KiB"], [54 << 20, "54 MiB"], [212 << 20, "212 MiB"],
                       [1964000000, "1.83 GiB"], [4509715661, "4.2 GiB"], [3328599654, "3.1 GiB"], [1048575 * 1024 + 900, "1 GiB"]];
        for (let i = 0; i < cases.length; i++)
            compare(State.humanBytes(cases[i][0]), cases[i][1], String(cases[i][0]));
    }

    // summaryLine and warnLine: each part only when there is something to say.
    function test_summaryLines() {
        compare(State.summaryLine(null), "");
        compare(State.summaryLine({upgraded: 0, aur: 0, freedBytes: 0, attention: []}), "");
        compare(State.summaryLine({upgraded: 1, aur: 0, freedBytes: 0, attention: []}), "1 upgraded");
        compare(State.summaryLine({upgraded: 0, aur: 0, freedBytes: 54 << 20, attention: []}), "54 MiB freed");
        compare(State.warnLine(null), "");
        compare(State.warnLine({upgraded: 0, aur: 0, freedBytes: 0, attention: []}), "");
        compare(State.warnLine({upgraded: 0, aur: 0, freedBytes: 0, attention: ["Restart recommended"]}), "\u26a0 Restart recommended");
    }

    function test_launchPlan() {
        compare(State.launchPlan(true, "kitty", true).argv, ["archmaid-gui", "--confirm", "fullcleanup"]);
        compare(State.launchPlan(false, "kitty", true).argv, ["kitty", "-e", "archmaid"]);
        compare(State.launchPlan(false, "  foot  --server ", false).argv, ["foot", "--server", "-e", "archmaid"]);
        compare(State.launchPlan(false, "", true).argv, ["xdg-terminal-exec", "archmaid"]);
        compare(State.launchPlan(false, undefined, true).argv, ["xdg-terminal-exec", "archmaid"]);
        const none = State.launchPlan(false, "   ", false);
        verify(none.argv === undefined);
        verify(none.error.indexOf("TERMINAL") >= 0);
    }

    // ---- updates-cache.json v:3: the package list, the hero, the rows ----

    function upd3(extra, checkedMs, v) {
        const o = {v: v === undefined ? 3 : v, count: 10, repo: 9, aur: 1, aur_held: 2,
                   checked: new Date(checkedMs === undefined ? t0 : checkedMs).toISOString()};
        for (const k in extra)
            o[k] = extra[k];
        return JSON.stringify(o);
    }
    function pk(name, src, extra) {
        const o = {name: name, from: "1.0-1", to: "1.1-1", src: src};
        for (const k in (extra || {}))
            o[k] = extra[k];
        return o;
    }
    readonly property var busyPkgs: [
        pk("zsh", "repo"), pk("systemd", "repo"), pk("curl", "repo"), pk("linux-cachyos", "repo"),
        pk("mesa", "repo"), pk("nvidia-settings", "repo"), pk("lib32-mesa", "repo"),
        pk("zen-browser-bin", "aur", {held: false}),
        pk("paru-bin", "aur", {held: true, age: "1d9h"}), pk("abc-bin", "aur", {held: true})
    ]

    // v:1..3 parse; the list exists for v:3 only, and only as a well-formed array.
    function test_parseV3() {
        const u = State.parseUpdates(upd3({packages: busyPkgs}));
        compare(u.packages.length, 10);
        compare(u.packages[8].held, true);
        compare(u.packages[8].age, "1d9h");
        compare(u.packages[0].held, false);
        compare(u.packages[0].age, "");
        verify(State.parseUpdates(upd3({})).packages === null, "v3 without packages");
        verify(State.parseUpdates(upd3({packages: busyPkgs}, t0, 2)).packages === null, "v2 ignores packages");
        verify(State.parseUpdates(upd3({packages: busyPkgs}, t0, 1)).packages === null, "v1 ignores packages");
        compare(State.parseUpdates(upd3({packages: busyPkgs}, t0, 1)).count, 10);
        verify(State.parseUpdates(upd3({}, t0, 4)) === null, "v4 is unknown");
        const bad = ["x", 5, {a: 1}, null, true];
        for (let i = 0; i < bad.length; i++)
            verify(State.parseUpdates(upd3({packages: bad[i]})).packages === null, "malformed packages " + i);
        compare(State.parseUpdates(upd3({packages: []})).packages.length, 0);
    }

    // The file is user-writable: every entry is checked again.
    function test_packageValidation() {
        const long = "a".repeat(65), longv = "1".repeat(49);
        const list = [
            pk("ok", "repo"),
            pk("has space", "repo"), pk("", "repo"), pk(long, "repo"), pk("semi;colon", "repo"), pk("<b>x</b>", "repo"),
            {name: "nofrom", to: "1", src: "repo"},
            pk("badfrom", "repo", {from: "1 0"}), pk("badto", "repo", {to: "1\n2"}), pk("longv", "repo", {to: longv}),
            pk("badsrc", "flatpak"), {name: "nosrc", from: "1", to: "2"},
            pk("badheld", "aur", {held: "yes"}), pk("badheld2", "aur", {held: 1}),
            pk("badage", "aur", {held: true, age: "1x"}), pk("badage2", "aur", {held: true, age: "d"}),
            pk("badage3", "aur", {held: true, age: "1d 2h"}),
            pk("goodage", "aur", {held: true, age: "12h30m5s"}),
            pk("repoheld", "repo", {held: true, age: "1d"}),
            pk("extra", "repo", {future: {x: 1}}),
            "str", 7, null, [1], {},
            pk("a@b._+-c", "repo", {from: "2:1.0~rc1+git-1", to: "1.0.r5.g1a2b3c-2"})
        ];
        const u = State.parseUpdates(upd3({packages: list}));
        compare(u.packages.map(p => p.name).join(","), "ok,badage,badage2,badage3,goodage,repoheld,extra,a@b._+-c");
        const by = {};
        u.packages.forEach(p => by[p.name] = p);
        compare(by.badage.held, true);
        compare(by.badage.age, "", "a bad age is dropped, the entry stays");
        compare(by.badage3.age, "");
        compare(by.goodage.age, "12h30m5s");
        compare(by.repoheld.held, false, "held is for the AUR only");
        compare(by.repoheld.age, "");
        verify(by.extra.future === undefined);
    }

    function test_packageCap() {
        const many = [];
        for (let i = 0; i < 250; i++)
            many.push(pk("p" + i, "repo"));
        compare(State.parseUpdates(upd3({packages: many})).packages.length, 200);
        const mix = [];
        for (let i = 0; i < 300; i++)
            mix.push(i % 2 === 0 ? pk("p" + i, "repo") : pk("bad name", "repo"));
        compare(State.parseUpdates(upd3({packages: mix})).packages.length, 150);
    }

    // The list lives inside the same 3-day window as the count.
    function test_listStaleWindow() {
        const text = upd3({packages: busyPkgs});
        const fresh = State.summarize(null, null, State.parseUpdates(text), t0 + 2.9 * day);
        verify(State.updateList(fresh, t0 + 2.9 * day) !== null);
        const old = State.summarize(null, null, State.parseUpdates(text), t0 + 3.1 * day);
        verify(old.updatesPackages === null);
        verify(State.updateList(old, t0 + 3.1 * day) === null);
        verify(State.popout(old, t0 + 3.1 * day).updateList === null);
        const v2 = State.summarize(null, null, State.parseUpdates(upd3({packages: busyPkgs}, t0, 2)), t0);
        verify(State.updateList(v2, t0) === null, "v2: no list");
        const none = State.summarize(null, null, State.parseUpdates(upd3({})), t0);
        verify(State.updateList(none, t0) === null, "v3 without packages: no list");
        const empty = State.summarize(null, null, State.parseUpdates(upd3({packages: []})), t0);
        verify(State.updateList(empty, t0) === null, "empty list: nothing to unfold");
    }

    function test_updateListGroups() {
        const now = t0 + 17 * 60000;
        const s = State.summarize(null, null, State.parseUpdates(upd3({packages: busyPkgs})), now);
        const l = State.updateList(s, now);
        compare(l.sections, ["Repo", "AUR", "Held · cooldown 72 h"]);
        compare(l.items.map(i => i.name).join(","),
                "lib32-mesa,linux-cachyos,mesa,systemd,curl,nvidia-settings,zsh,zen-browser-bin,abc-bin,paru-bin");
        compare(l.items.map(i => i.tag).join("|"), "restart|restart|restart|restart|||||held|held 1d9h");
        compare(l.items[0].section, "Repo");
        compare(l.items[7].section, "AUR");
        compare(l.items[8].section, "Held · cooldown 72 h");
        compare(l.items[0].from, "1.0-1");
        compare(l.listed, 10);
        compare(l.more, 2, "9 + 1 + 2 held - 10 listed");
        compare(l.checked, "checked 17 min ago");
        const only = State.summarize(null, null, State.parseUpdates(upd3({count: 2, repo: 2, aur: 0, aur_held: 0, packages: [pk("b", "repo"), pk("a", "repo")]})), now);
        const l2 = State.updateList(only, now);
        compare(l2.sections, ["Repo"]);
        compare(l2.more, 0);
        compare(l2.items.map(i => i.name).join(","), "a,b");
        const over = State.summarize(null, null, State.parseUpdates(upd3({count: 1, repo: 1, aur: 0, aur_held: 0, packages: [pk("b", "repo"), pk("a", "repo")]})), now);
        compare(State.updateList(over, now).more, 0);
    }

    function test_updateListMore() {
        const many = [];
        for (let i = 0; i < 200; i++)
            many.push(pk("p" + i, "repo"));
        const s = State.summarize(null, null, State.parseUpdates(upd3({count: 260, repo: 250, aur: 10, aur_held: 3, packages: many})), t0);
        compare(State.updateList(s, t0).more, 63);
    }

    // The restart rule: Go's restart.go computes the expected answer for each name.
    function test_restartParity() {
        const names = Object.keys(JSON.parse(F.fx.restartTable));
        verify(names.length > 20);
        for (let i = 0; i < names.length; i++)
            compare(State.isRestartPkg(names[i]), JSON.parse(F.fx.restartTable)[names[i]], names[i]);
        verify(State.isRestartPkg("systemd"));
        verify(!State.isRestartPkg("linux"));
        verify(!State.isRestartPkg(undefined));
    }

    // The list's kernel rule (name only; the Go rule leaves kernels out).
    function test_kernelNames() {
        const yes = ["linux", "linux-lts", "linux-zen", "linux-hardened", "linux-cachyos", "linux-cachyos-lts",
                     "linux-cachyos-nvidia-open", "linux-cachyos-bore", "linux-rt"];
        const no = ["linux-headers", "linux-cachyos-headers", "linux-api-headers", "linux-docs", "linux-cachyos-docs",
                    "linux-firmware", "linux-firmware-nvidia", "linux-firmware-whence", "linux-tools", "linux-tools-meta",
                    "linuxdoc", "linux_x", "Linux", "linux-", "lib32-linux", "xlinux", "", "systemd", "mesa"];
        for (let i = 0; i < yes.length; i++)
            verify(State.isKernelPkg(yes[i]), yes[i]);
        for (let j = 0; j < no.length; j++)
            verify(!State.isKernelPkg(no[j]), no[j]);
        verify(!State.isKernelPkg(undefined));
        verify(!State.isRestartPkg("linux-cachyos"), "isRestartPkg stays Go's rule");
    }

    function test_heroQuiet() {
        const now = t0 + 2 * 3600000;
        const s = sum(F.fx.lastOK, F.fx.auditClean, upd3({count: 0, repo: 0, aur: 0, aur_held: 0, packages: []}, now - 300000), now);
        const p = State.popout(s, now);
        compare(p.hero.tone, "ok");
        compare(p.hero.kicker, "LAST RUN · 2 H AGO");
        compare(p.hero.headline, "Everything is up to date");
        compare(p.hero.sub, "Cleaned 2 h ago, nothing pending");
        compare(p.hero.attention, []);
        compare(p.rows.updates.value, "Up to date");
        compare(p.rows.updates.tag, "UP TO DATE");
        compare(p.rows.aur.tag, "NO HIGH");
        compare(p.rows.aur.tone, "ok");
        compare(p.rows.lastRun.value, "Full Cleanup");
        compare(p.rows.lastRun.tag, "OK");
        compare(p.rows.lastRun.tone, "ok");
    }

    function test_heroBusy() {
        const now = t0 + 17 * 3600000;
        const s = sum(F.fx.lastSummary, F.fx.auditClean, upd3({packages: busyPkgs}, now - 17 * 60000), now);
        const p = State.popout(s, now);
        compare(p.hero.tone, "warn");
        compare(p.hero.kicker, "LAST RUN · 17 H AGO");
        compare(p.hero.headline, "Restart recommended");
        compare(p.hero.sub, "10 updates waiting · 2 held");
        compare(p.hero.attention, ["1 new .pacnew"], "the headline is not repeated as the first line");
        compare(p.rows.updates.value, "9 repo · 1 AUR · 2 held");
        compare(p.rows.updates.tag, "10 NEW");
        compare(p.rows.updates.tone, "muted");
        verify(p.updateList !== null);
        const w = sum(F.fx.lastOK, F.fx.auditClean, upd3({}, now - 60000), now);
        const h = State.popout(w, now).hero;
        compare(h.tone, "neutral");
        const q = sum(F.fx.lastOK, F.fx.auditClean, upd3({count: 0, repo: 0, aur: 0, aur_held: 0, packages: []}, now - 60000), now);
        compare(State.popout(q, now).hero.tone, "ok", "green is for up to date only");
        compare(h.headline, "10 updates waiting");
        compare(h.sub, "Cleaned 17 h ago · 2 held");
    }

    function test_heroProblem() {
        const now = t0 + 2 * 3600000;
        const s = sum(F.fx.lastFailed1, F.fx.auditHigh, upd3({count: 5, repo: 5, aur: 0, aur_held: 0}, now - 600000), now);
        const p = State.popout(s, now);
        compare(p.hero.tone, "danger");
        compare(p.hero.headline, "Last run failed", "a failed run outranks AUR warnings");
        compare(p.hero.sub, "1 step failed · 2 AUR warnings", "the Updates row says the rest");
        compare(p.hero.attention, [], "the AUR finding is the AUR row's, not repeated in the hero");
        compare(p.rows.aur.tag, "2 HIGH");
        compare(p.rows.aur.tone, "danger");
        compare(p.rows.aur.value, "foo");
        compare(p.rows.aur.reason, "maintainer alice → mallory");
        compare(p.rows.lastRun.tag, "FAILED");
        compare(p.rows.lastRun.tone, "danger");
        const a = sum(F.fx.lastOK, F.fx.auditHigh, null, now);
        compare(State.popout(a, now).hero.headline, "2 AUR warnings");
    }

    function test_heroStale() {
        const now = t0 + 16 * day;
        const s = sum(F.fx.lastOK, F.fx.auditClean, upd3({}, t0), now);
        const p = State.popout(s, now);
        compare(p.hero.tone, "warn");
        compare(p.hero.kicker, "LAST RUN · 16 DAYS AGO");
        compare(p.hero.headline, "No cleanup for 16 days");
        compare(p.hero.sub, "Update check is 16 days old");
        compare(p.rows.updates.value, "Unknown");
        compare(p.rows.updates.tag, "STALE");
        compare(p.rows.updates.tone, "warn");
        verify(p.updateList === null);
    }

    function test_heroNoData() {
        const p = State.popout(sum(null, null, null, t0), t0);
        compare(p.hero.tone, "muted");
        compare(p.hero.headline, "No data yet");
        compare(p.hero.kicker, "NO RUN YET");
        compare(p.rows.lastRun.value, "No run recorded");
        compare(p.rows.updates.value, "Not checked yet");
        compare(p.rows.updates.tag, "");
        compare(p.rows.aur.value, "No audit yet");
        compare(p.rows.aur.tag, "");
        verify(p.updateList === null);
    }

    // An AUR audit with no high finding says NO HIGH, never CLEAN.
    function test_noHigh() {
        const p = State.popout(sum(null, F.fx.auditClean, null, t0), t0);
        compare(p.rows.aur.tag, "NO HIGH");
        compare(p.rows.aur.value.indexOf("27 audited"), 0);
    }
}
