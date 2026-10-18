.pragma library
// Pure logic of the archmaid DMS widget: parsing the state files and turning
// them into one summary. No QML types, no I/O, so packaging/dms/dms_test.go
// can run it under qmltestrunner against files the real Go writers produced.
//
// Contract (docs/dms-widget.md, section 3):
//   last-run.json        internal/logstore.RunRecord   v:1
//   audit-state.json     internal/audit.State          v:1
//   updates-cache.json   written by archmaid-updates-check   v:1 or v:2
// Unknown fields are ignored. Anything unusable (missing, empty, torn, wrong
// version, wrong types) parses to null, which the widget shows as "no data".
// Nothing here throws.

var STALE_DAYS = 14;          // last run older than this: warning
var UPDATES_MAX_AGE_DAYS = 3; // a count older than this is "unknown"
var MAX_HIGH = 10;            // findings kept for the popout
var DAY_MS = 86400000;

// "nothing-found": an action that ended at its discovery, which looked and
// found nothing to do (a failed look is "failed"). Not an issue.
var KNOWN_OUTCOMES = ["ok", "failed", "aborted", "cancelled", "incomplete", "nothing-found"];

function _obj(text, maxV) {
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
    if (v.v !== 1 && !(maxV === 2 && v.v === 2))
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

var MAX_ATTENTION = 4;        // run-summary items kept for the popout
var MAX_ATTENTION_LEN = 60;

// _summary: the optional "summary" object of last-run.json (the run report's
// headline numbers, added within v1: an older archmaid writes none) as
// {upgraded, aur, freedBytes, attention:[string]}, or null when absent or not
// an object. A damaged field reads as 0 / skipped, never throws.
function _summary(o) {
    if (o === null || typeof o !== "object" || Array.isArray(o))
        return null;
    var att = [];
    if (Array.isArray(o.attention)) {
        for (var i = 0; i < o.attention.length && att.length < MAX_ATTENTION; i++) {
            if (typeof o.attention[i] === "string" && o.attention[i].length > 0)
                att.push(o.attention[i].slice(0, MAX_ATTENTION_LEN));
        }
    }
    return {
        upgraded: _count(o.upgraded),
        aur: _count(o.aur),
        freedBytes: _count(o.freed_bytes),
        attention: att
    };
}

// humanBytes writes n as the Go side's report.HumanBytes does (and the
// GUI's summary): binary units, three significant digits, no trailing zeros:
// "0 B", "54 MiB", "1.83 GiB", "3.1 GiB".
function humanBytes(n) {
    var units = ["B", "KiB", "MiB", "GiB", "TiB", "PiB", "EiB"];
    var v = n, i = 0;
    while (v >= 1024 && i < units.length - 1) {
        v /= 1024;
        i++;
    }
    if (i === 0)
        return n + " B";
    var t = v >= 99.5 ? v.toFixed(0) : (v >= 9.995 ? v.toFixed(1) : v.toFixed(2));
    if (t.indexOf(".") >= 0)
        t = t.replace(/0+$/, "").replace(/\.$/, "");
    if (t === "1024" && i < units.length - 1)
        return "1 " + units[i + 1];
    return t + " " + units[i];
}

// summaryLine: the popout's "47 upgraded \u00b7 3.1 GiB freed" from a parsed
// summary (null or without numbers: ""). Each part only when not zero.
function summaryLine(sm) {
    if (!sm)
        return "";
    var parts = [];
    if (sm.upgraded > 0)
        parts.push(sm.upgraded + " upgraded");
    if (sm.freedBytes > 0)
        parts.push(humanBytes(sm.freedBytes) + " freed");
    return parts.join(" \u00b7 ");
}

// warnLine: "\u26a0 Restart recommended \u00b7 1 new .pacnew" when the run had things
// that need the user, else "".
function warnLine(sm) {
    if (!sm || sm.attention.length === 0)
        return "";
    return "\u26a0 " + sm.attention.join(" \u00b7 ");
}

// parseLastRun: {action, finished(ms), outcome, stepsFailed, stepsRefused,
// stepsSkipped, summary} or null. summary is null for a record without one.
// A dry-run record is not a real run: null.
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
        stepsSkipped: _count(o.steps_skipped),
        summary: _summary(o.summary)
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

function _natural(v) {
    return typeof v === "number" && isFinite(v) && v >= 0 && Math.floor(v) === v;
}

// parseUpdates: {count, repo, aur, aurHeld, checked(ms)} or null.
// v:1 files carry only count (repo/aur/aurHeld come back null). v:2 adds
// repo, aur (AUR updates past the cooldown, null = unknown) and aur_held
// (AUR updates inside the cooldown). A malformed optional field reads as null.
function parseUpdates(text) {
    var o = _obj(text, 2);
    if (!o)
        return null;
    var checked = _time(o.checked);
    if (checked === null || !_natural(o.count))
        return null;
    var aurKnown = _natural(o.aur);
    return {
        count: o.count,
        repo: _natural(o.repo) ? o.repo : null,
        aur: aurKnown ? o.aur : null,
        aurHeld: aurKnown && _natural(o.aur_held) ? o.aur_held : 0,
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
        return { level: "nodata", issues: [], updates: null, updatesDetail: null, updatesChecked: null, lastRun: null, audit: null };

    if (audit && audit.highCount > 0)
        errors.push({ kind: "aur", text: audit.highCount + (audit.highCount === 1 ? " AUR warning" : " AUR warnings") });
    if (lastRun) {
        switch (lastRun.outcome) {
        case "failed":
            errors.push({ kind: "failed", text: "Last run failed" + (lastRun.stepsFailed > 0 ? " (" + plural(lastRun.stepsFailed, "step", "steps") + ")" : "") + alsoRefused(lastRun.stepsRefused) });
            break;
        case "incomplete":
            warns.push({ kind: "incomplete", text: "Last run incomplete: root steps refused" + (lastRun.stepsRefused > 0 ? " (" + lastRun.stepsRefused + ")" : "") });
            break;
        case "aborted":
            warns.push({ kind: "aborted", text: "Last run aborted" + alsoRefused(lastRun.stepsRefused) });
            break;
        case "cancelled":
            // Every step refused: the engine records it cancelled (nothing
            // ran) with steps_refused; the GUI says "not authorised".
            if (lastRun.stepsRefused > 0)
                warns.push({ kind: "notauthorised", text: "Last run not authorised \u00b7 nothing ran (" + plural(lastRun.stepsRefused, "step", "steps") + " not authorised)" });
            else
                warns.push({ kind: "cancelled", text: "Last run cancelled, nothing ran" });
            break;
        }
        var age = _ageDays(lastRun.finished, now);
        if (age > STALE_DAYS)
            warns.push({ kind: "stale", text: "No cleanup for " + plural(Math.floor(age), "day", "days") });
    }

    var count = null;
    if (updates && _ageDays(updates.checked, now) <= UPDATES_MAX_AGE_DAYS)
        count = updates.count;
    // The split (v:2 files only); null when the count is unknown or from a v:1 file.
    var detail = count !== null && updates.repo !== null ? { repo: updates.repo, aur: updates.aur, aurHeld: updates.aurHeld } : null;

    return {
        level: errors.length > 0 ? "error" : (warns.length > 0 ? "warn" : "ok"),
        issues: errors.concat(warns),
        updates: count,
        updatesDetail: detail,
        updatesChecked: updates ? updates.checked : null,
        lastRun: lastRun,
        audit: audit
    };
}

function _own(map, key) {
    return typeof key === "string" && Object.prototype.hasOwnProperty.call(map, key);
}

var OUTCOME_TAGS = { ok: "OK", failed: "FAILED", incomplete: "INCOMPLETE", aborted: "ABORTED", cancelled: "CANCELLED", "nothing-found": "NOTHING FOUND" };
var OUTCOME_TINTS = { ok: "primary", failed: "error", incomplete: "warning", aborted: "warning", cancelled: "muted", "nothing-found": "primary" };

// outcomeTag(outcome, refused): the LAST RUN chip's text, as the GUI's tag.
// A cancelled run with refused steps is "NOT AUTHORISED".
function outcomeTag(outcome, refused) {
    if (outcome === "cancelled" && refused > 0)
        return "NOT AUTHORISED";
    return _own(OUTCOME_TAGS, outcome) ? OUTCOME_TAGS[outcome] : "UNKNOWN";
}

// outcomeTint(outcome): the chip's colour role, "primary", "error",
// "warning" or "muted"; the widget maps it to Theme.primary, Theme.error,
// Theme.warning and Theme.surfaceVariantText. A cancelled run ran nothing,
// so it is muted, not a warning colour.
function outcomeTint(outcome, refused) {
    if (outcome === "cancelled" && refused > 0)
        return "warning";
    return _own(OUTCOME_TINTS, outcome) ? OUTCOME_TINTS[outcome] : "muted";
}

// The runnable actions' names as the GUI's Overview shows them; the record
// holds the ID. dms_test.go checks every runnable action of
// internal/actions has one. An unknown ID shows as itself.
var ACTION_LABELS = {
    fullcleanup: "Full Cleanup",
    usercache: "Clear user cache",
    repair: "Repair broken packages",
    downloads: "Delete old downloads",
    wipecache: "Delete entire package cache",
    topgrade: "Run topgrade",
    services: "Disable unused services",
    auraudit: "Audit AUR packages"
};

function actionLabel(id) {
    if (_own(ACTION_LABELS, id))
        return ACTION_LABELS[id];
    return (typeof id === "string" && id.length > 0) ? id : "Unknown action";
}

var DAY_NAMES = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];
var MONTH_NAMES = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

function _pad2(n) {
    return (n < 10 ? "0" : "") + n;
}

// stamp(then, now): "Mon 5 Oct, 00:45 · 17 h ago", local time, English
// names whatever the locale (the GUI's wording). The year is added when it
// isn't now's: "Wed 31 Dec 2025, 23:59 · 8 h ago".
function stamp(then, now) {
    var d = new Date(then);
    var s = DAY_NAMES[d.getDay()] + " " + d.getDate() + " " + MONTH_NAMES[d.getMonth()];
    if (d.getFullYear() !== new Date(now).getFullYear())
        s += " " + d.getFullYear();
    return s + ", " + _pad2(d.getHours()) + ":" + _pad2(d.getMinutes()) + " · " + ago(then, now);
}

var SHOWN_HIGH = 5; // AUR findings listed in the popout; the rest are counted

// updatesNote: the Updates line under the count. The held part (AUR updates
// younger than the 72 h cooldown, which yay holds back and the count leaves
// out) shows only when there are some.
//   "9 repo · 3 AUR (2 held, <72h) · checked 5 min ago"
//   "9 repo · AUR unknown · checked 5 min ago"   (no yay, or its check failed)
//   "up to date · checked 5 min ago"             (nothing pending, AUR known)
//   "pending · checked 5 min ago"                (a v:1 file: no split)
function updatesNote(s, now) {
    var d = s.updatesDetail;
    var tail = " · checked " + ago(s.updatesChecked, now);
    if (!d)
        return (s.updates === 0 ? "up to date" : "pending") + tail;
    if (d.aur === null)
        return d.repo + " repo · AUR unknown" + tail;
    if (d.repo === 0 && d.aur === 0 && d.aurHeld === 0)
        return "up to date" + tail;
    return d.repo + " repo · " + d.aur + " AUR" + (d.aurHeld > 0 ? " (" + d.aurHeld + " held, <72h)" : "") + tail;
}

// popout(s, now): the popout's three sections, in Workbench's order, from
// summarize's result s. Plain strings and numbers only.
//   lastRun  null (no record) or {action, tag, tint, stamp, notes:[{text, tint}],
//            summary, summaryLine, warnLine} (summaryLine "47 upgraded \u00b7 3.1 GiB
//            freed" and warnLine "\u26a0 Restart recommended \u00b7 1 new .pacnew" are
//            "" without a summary or without attention; a note: the failed steps "error", refused steps or staleness "warning")
//   updates  {count (number|null), note}
//   aur      {packages (number|null), note, high:[{pkg,reason}], more}
function popout(s, now) {
    var p = {
        lastRun: null,
        updates: { count: null, note: "Not checked yet" },
        aur: { packages: null, note: "No audit yet", high: [], more: 0 }
    };
    var r = s.lastRun;
    if (r) {
        var notes = [];
        for (var i = 0; i < s.issues.length; i++) {
            var k = s.issues[i].kind;
            if (k === "failed" || k === "incomplete" || k === "notauthorised" || k === "stale")
                notes.push({ text: s.issues[i].text, tint: k === "failed" ? "error" : "warning" });
        }
        p.lastRun = {
            action: actionLabel(r.action),
            tag: outcomeTag(r.outcome, r.stepsRefused),
            tint: outcomeTint(r.outcome, r.stepsRefused),
            stamp: stamp(r.finished, now),
            notes: notes,
            summary: r.summary || null,
            summaryLine: summaryLine(r.summary),
            warnLine: warnLine(r.summary)
        };
    }
    if (s.updates !== null) {
        p.updates.count = s.updates;
        p.updates.note = updatesNote(s, now);
    } else if (s.updatesChecked !== null && s.updatesChecked !== undefined) {
        p.updates.note = "Not checked for " + plural(Math.floor(_ageDays(s.updatesChecked, now)), "day", "days");
    }
    var a = s.audit;
    if (a) {
        p.aur.packages = a.packages;
        p.aur.note = (a.packages === 1 ? "package" : "packages") + (a.checked !== null ? " · checked " + ago(a.checked, now) : "");
        p.aur.high = a.high.slice(0, SHOWN_HIGH);
        p.aur.more = a.highCount - p.aur.high.length;
    }
    return p;
}

// plural(n, one, many): "1 step", "2 steps" (the GUI's plural.N).
function plural(n, one, many) {
    return n + " " + (n === 1 || n === -1 ? one : many);
}

// alsoRefused(n): " \u00b7 5 steps not authorised" after a failed or aborted
// run that also had refused steps, "" when none; the GUI's plural.AlsoRefused.
function alsoRefused(n) {
    return n > 0 ? " \u00b7 " + plural(n, "step", "steps") + " not authorised" : "";
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
    return plural(Math.floor(s / 86400), "day", "days") + " ago";
}

// launchPlan decides what "Run Full Cleanup" starts. Pure: the caller says
// which binaries exist and what $TERMINAL holds.
//   hasGui      archmaid-gui is on PATH
//   terminal    value of $TERMINAL ("" or undefined when unset)
//   hasXdgTerm  xdg-terminal-exec is on PATH
// Returns {argv:[...]} or {error:"..."}. argv is never run through a shell.
function launchPlan(hasGui, terminal, hasXdgTerm) {
    if (hasGui)
        return { argv: ["archmaid-gui", "--confirm", "fullcleanup"] };
    var words = (typeof terminal === "string" ? terminal : "").trim().split(/\s+/).filter(function (w) {
        return w.length > 0;
    });
    if (words.length > 0)
        return { argv: words.concat(["-e", "archmaid"]) };
    if (hasXdgTerm)
        return { argv: ["xdg-terminal-exec", "archmaid"] };
    return { error: "Set $TERMINAL (for example kitty) or install xdg-terminal-exec to open the archmaid TUI" };
}
