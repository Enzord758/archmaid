.pragma library
// Pure logic of the archmaid DMS widget: parsing the state files and turning
// them into one summary. No QML types, no I/O, so packaging/dms/dms_test.go
// can run it under qmltestrunner against files the real Go writers produced.
//
// Contract (docs/dms-widget.md, section 3):
//   last-run.json        internal/logstore.RunRecord   v:1
//   audit-state.json     internal/audit.State          v:1
//   updates-cache.json   written by archmaid-updates-check   v:1
// Unknown fields are ignored. Anything unusable (missing, empty, torn, wrong
// version, wrong types) parses to null, which the widget shows as "no data".
// Nothing here throws.

var STALE_DAYS = 14;          // last run older than this: warning
var UPDATES_MAX_AGE_DAYS = 3; // a count older than this is "unknown"
var MAX_HIGH = 10;            // findings kept for the popout
var DAY_MS = 86400000;

var KNOWN_OUTCOMES = ["ok", "failed", "aborted", "cancelled", "incomplete"];

function _obj(text) {
    if (typeof text !== "string" || text.length === 0 || text.length > 1048576)
        return null;
    var v;
    try {
        v = JSON.parse(text);
    } catch (e) {
        return null;
    }
    if (v === null || typeof v !== "object" || Array.isArray(v))
        return null;
    if (v.v !== 1)
        return null;
    return v;
}

function _time(s) {
    if (typeof s !== "string")
        return null;
    var ms = Date.parse(s);
    return isNaN(ms) ? null : ms;
}

function _count(n) {
    return (typeof n === "number" && isFinite(n) && n >= 0 && Math.floor(n) === n) ? n : 0;
}

// parseLastRun: {action, finished(ms), outcome, stepsFailed, stepsRefused,
// stepsSkipped} or null. A dry-run record is not a real run: null.
function parseLastRun(text) {
    var o = _obj(text);
    if (!o)
        return null;
    var finished = _time(o.finished);
    if (finished === null || o.dry_run === true)
        return null;
    var outcome = (typeof o.outcome === "string" && KNOWN_OUTCOMES.indexOf(o.outcome) >= 0) ? o.outcome : "unknown";
    return {
        action: typeof o.action === "string" ? o.action.slice(0, 40) : "",
        finished: finished,
        outcome: outcome,
        stepsFailed: _count(o.steps_failed),
        stepsRefused: _count(o.steps_refused),
        stepsSkipped: _count(o.steps_skipped)
    };
}

// parseAudit: {checked(ms|null), packages, highCount, high:[{pkg,reason}],
// snapshotPresent, snapshotReadable} or null. high holds at most MAX_HIGH
// entries; highCount is the real number.
function parseAudit(text) {
    var o = _obj(text);
    if (!o || !Array.isArray(o.high))
        return null;
    var high = [];
    for (var i = 0; i < o.high.length; i++) {
        var f = o.high[i];
        if (f === null || typeof f !== "object" || typeof f.pkg !== "string")
            continue;
        high.push({
            pkg: f.pkg.slice(0, 80),
            reason: typeof f.reason === "string" ? f.reason.slice(0, 120) : ""
        });
    }
    return {
        checked: _time(o.checked),
        packages: _count(o.packages),
        highCount: high.length,
        high: high.slice(0, MAX_HIGH),
        snapshotPresent: o.snapshot_present === true,
        snapshotReadable: o.snapshot_readable === true
    };
}

// parseUpdates: {count, checked(ms)} or null.
function parseUpdates(text) {
    var o = _obj(text);
    if (!o)
        return null;
    var checked = _time(o.checked);
    if (checked === null || typeof o.count !== "number" || !isFinite(o.count) || o.count < 0 || Math.floor(o.count) !== o.count)
        return null;
    return {
        count: o.count,
        checked: checked
    };
}

function _ageDays(then, now) {
    return Math.max(0, now - then) / DAY_MS;
}

// summarize turns the three parsed inputs (each may be null) into what the
// widget shows.
//   level   "nodata" (nothing readable), "ok", "warn", "error"
//   issues  [{kind, text}], errors first; empty when level is ok
//   updates pending count, or null when unknown or too old
//   lastRun / audit  the parsed inputs, for the popout
//
// last-run.json holds one record, overwritten by every run, so "last
// successful cleanup" is only known when that record is itself a success.
// After a failed run the outcome issue stands in, and the age of the record
// is reported on top once it passes STALE_DAYS.
function summarize(lastRun, audit, updates, now) {
    var errors = [];
    var warns = [];

    if (!lastRun && !audit && !updates)
        return { level: "nodata", issues: [], updates: null, lastRun: null, audit: null };

    if (audit && audit.highCount > 0)
        errors.push({ kind: "aur", text: audit.highCount + (audit.highCount === 1 ? " AUR warning" : " AUR warnings") });
    if (lastRun) {
        switch (lastRun.outcome) {
        case "failed":
            errors.push({ kind: "failed", text: "Last run failed" + (lastRun.stepsFailed > 0 ? " (" + lastRun.stepsFailed + " steps)" : "") });
            break;
        case "incomplete":
            warns.push({ kind: "incomplete", text: "Last run incomplete: root steps refused" + (lastRun.stepsRefused > 0 ? " (" + lastRun.stepsRefused + ")" : "") });
            break;
        case "aborted":
            warns.push({ kind: "aborted", text: "Last run aborted" });
            break;
        case "cancelled":
            warns.push({ kind: "cancelled", text: "Last run cancelled, nothing ran" });
            break;
        }
        var age = _ageDays(lastRun.finished, now);
        if (age > STALE_DAYS)
            warns.push({ kind: "stale", text: "No cleanup for " + Math.floor(age) + " days" });
    }

    var count = null;
    if (updates && _ageDays(updates.checked, now) <= UPDATES_MAX_AGE_DAYS)
        count = updates.count;

    return {
        level: errors.length > 0 ? "error" : (warns.length > 0 ? "warn" : "ok"),
        issues: errors.concat(warns),
        updates: count,
        lastRun: lastRun,
        audit: audit
    };
}

// ago(then, now): "just now", "5 min ago", "3 h ago", "12 days ago".
function ago(then, now) {
    var s = Math.max(0, now - then) / 1000;
    if (s < 90)
        return "just now";
    if (s < 5400)
        return Math.round(s / 60) + " min ago";
    if (s < 129600)
        return Math.round(s / 3600) + " h ago";
    return Math.floor(s / 86400) + " days ago";
}

// launchPlan decides what "Run Full Cleanup" starts. Pure: the caller says
// which binaries exist and what $TERMINAL holds.
//   hasGui      archmaid-gui is on PATH
//   terminal    value of $TERMINAL ("" or undefined when unset)
//   hasXdgTerm  xdg-terminal-exec is on PATH
// Returns {argv:[...]} or {error:"..."}. argv is never run through a shell.
function launchPlan(hasGui, terminal, hasXdgTerm) {
    if (hasGui)
        return { argv: ["archmaid-gui"] };
    var words = (typeof terminal === "string" ? terminal : "").trim().split(/\s+/).filter(function (w) {
        return w.length > 0;
    });
    if (words.length > 0)
        return { argv: words.concat(["-e", "archmaid"]) };
    if (hasXdgTerm)
        return { argv: ["xdg-terminal-exec", "archmaid"] };
    return { error: "Set $TERMINAL (for example kitty) or install xdg-terminal-exec to open the archmaid TUI" };
}
