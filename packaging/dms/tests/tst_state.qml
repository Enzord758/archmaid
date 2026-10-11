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
        // steps_refused on any other outcome changes nothing
        const fs = sum(F.fx.lastFailedRefused, null, null, t0);
        compare(fs.issues[0].text, "Last run failed (1 step)");
        const f = State.popout(fs, t0);
        compare(f.lastRun.tag, "FAILED");
        compare(f.lastRun.tint, "error");
        const as = sum(F.fx.lastAbortedRefused, null, null, t0);
        compare(kinds(as), "aborted");
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
}
