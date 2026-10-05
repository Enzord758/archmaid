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

    function test_launchPlan() {
        compare(State.launchPlan(true, "kitty", true).argv, ["archmaid-gui"]);
        compare(State.launchPlan(false, "kitty", true).argv, ["kitty", "-e", "archmaid"]);
        compare(State.launchPlan(false, "  foot  --server ", false).argv, ["foot", "--server", "-e", "archmaid"]);
        compare(State.launchPlan(false, "", true).argv, ["xdg-terminal-exec", "archmaid"]);
        compare(State.launchPlan(false, undefined, true).argv, ["xdg-terminal-exec", "archmaid"]);
        const none = State.launchPlan(false, "   ", false);
        verify(none.argv === undefined);
        verify(none.error.indexOf("TERMINAL") >= 0);
    }
}
