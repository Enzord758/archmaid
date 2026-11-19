// No ".pragma library" line: it is QML-only syntax, and a plain JavaScript parser
// (GitHub CodeQL) rejects the whole file because of it. This file is pure (no Qt
// globals, no state), so one instance per importer behaves the same. Keep it plain
// ES5: no QML-only syntax, no let/const/arrow functions.
// Pure logic of the archmaid DMS widget: parsing the state files and turning
// them into one summary. No QML types, no I/O, so packaging/dms/dms_test.go
// can run it under qmltestrunner against files the real Go writers produced.
//
// Contract (docs/dms-widget.md, section 3):
//   last-run.json        internal/logstore.RunRecord   v:1
//   audit-state.json     internal/audit.State          v:1
//   updates-cache.json   written by archmaid-updates-check   v:1, v:2 or v:3
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
    if (typeof v.v !== "number" || Math.floor(v.v) !== v.v || v.v < 1 || v.v > (maxV || 1))
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

var MAX_PACKAGES = 200;      // the list's cap (the writer's too)
var NAME_RE = /^[A-Za-z0-9@._+-]{1,64}$/;
var VERSION_RE = /^[A-Za-z0-9._+:~-]{1,48}$/;
var AGE_RE = /^[0-9]+[dhms]([0-9]+[dhms])*$/;

// _packages: the v:3 "packages" list as [{name, from, to, src, held, age}] or
// null when it is absent or not an array. The file is user-writable, so every
// entry is validated again: a bad entry is dropped, unknown fields are
// ignored, a bad "age" is dropped, "held" only counts for the AUR. At most
// MAX_PACKAGES entries are kept.
function _packages(list) {
    if (!Array.isArray(list))
        return null;
    var out = [];
    for (var i = 0; i < list.length && out.length < MAX_PACKAGES; i++) {
        var e = list[i];
        if (e === null || typeof e !== "object" || Array.isArray(e))
            continue;
        if (typeof e.name !== "string" || !NAME_RE.test(e.name))
            continue;
        if (typeof e.from !== "string" || !VERSION_RE.test(e.from) || typeof e.to !== "string" || !VERSION_RE.test(e.to))
            continue;
        if (e.src !== "repo" && e.src !== "aur")
            continue;
        if (e.held !== undefined && typeof e.held !== "boolean")
            continue;
        var aur = e.src === "aur";
        out.push({
            name: e.name,
            from: e.from,
            to: e.to,
            src: e.src,
            held: aur && e.held === true,
            age: aur && typeof e.age === "string" && AGE_RE.test(e.age) ? e.age : ""
        });
    }
    return out;
}

// parseUpdates: {count, repo, aur, aurHeld, checked(ms), packages} or null.
// v:1 files carry only count (repo/aur/aurHeld come back null). v:2 adds
// repo, aur (AUR updates past the cooldown, null = unknown) and aur_held
// (AUR updates inside the cooldown). v:3 adds packages (see _packages; null
// for v:1/v:2 or a malformed list). A malformed optional field reads as null.
function parseUpdates(text) {
    var o = _obj(text, 3);
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
        checked: checked,
        packages: o.v === 3 ? _packages(o.packages) : null
    };
}

// health.json (internal/health ReportJSON, docs/design/health.md):
//   {"v":1,"at":"<RFC 3339 UTC>","attention":N,"checks":[{id,title,state,detail}]}
// state is ok, look, unknown or ignored; at most 16 checks. parseHealth
// returns {at(ms), attention, checks:[{id,title,state,detail}]} or null for
// anything unusable. attention is counted from the checks (only "look"), so
// a file that miscounts cannot raise a false alarm.
var HEALTH_MAX_CHECKS = 16;
var HEALTH_STATES = { ok: true, look: true, unknown: true, ignored: true };
var HEALTH_STALE_MS = 2 * 3600000;    // older: the age is shown, muted
var HEALTH_MAX_AGE_MS = 3 * DAY_MS;   // older: unknown, "Not checked yet"
var HEALTH_TITLES_SHOWN = 3;

function parseHealth(text) {
    var o = _obj(text);
    if (!o || !Array.isArray(o.checks) || o.checks.length > HEALTH_MAX_CHECKS)
        return null;
    var at = _time(o.at);
    if (at === null)
        return null;
    var checks = [];
    var look = 0;
    for (var i = 0; i < o.checks.length; i++) {
        var c = o.checks[i];
        if (c === null || typeof c !== "object" || Array.isArray(c))
            return null;
        if (typeof c.id !== "string" || typeof c.title !== "string" || typeof c.detail !== "string" || !_own(HEALTH_STATES, c.state))
            return null;
        if (c.title.length === 0)
            return null;
        if (c.state === "look")
            look++;
        checks.push({ id: c.id, title: c.title, state: c.state, detail: c.detail });
    }
    return { at: at, attention: look, checks: checks };
}

// _healthView: what the widget may say about a parsed health file at `now`.
//   known      false when the file is missing, malformed or over 3 days old
//   attention  checks in state "look" (0 when not known)
//   titles     the look checks' titles, at most 3, then "+N"
//   stale      older than 2 h: the age is shown
//   at         the file's time (ms) or null
function _healthView(h, now) {
    var v = { known: false, attention: 0, titles: "", stale: false, at: null };
    if (!h)
        return v;
    v.at = h.at;
    var age = now - h.at;
    if (age > HEALTH_MAX_AGE_MS)
        return v;
    v.known = true;
    v.attention = h.attention;
    v.stale = age > HEALTH_STALE_MS;
    var names = [];
    for (var i = 0; i < h.checks.length; i++) {
        if (h.checks[i].state === "look")
            names.push(h.checks[i].title);
    }
    v.titles = names.slice(0, HEALTH_TITLES_SHOWN).join(", ") + (names.length > HEALTH_TITLES_SHOWN ? " +" + (names.length - HEALTH_TITLES_SHOWN) : "");
    return v;
}

function _attentionText(n) {
    return n === 1 ? "1 thing needs attention" : n + " things need attention";
}

// The Health row: "All good" with an OK tag, "2 things need attention" with
// a warn LOOK tag, or "Not checked yet" (muted, no tag) when the file is
// missing, malformed or too old. A muted line under it names the look checks
// and, when the file is older than 2 h, its age.
function _healthRow(s, now) {
    var v = _healthView(s.health, now);
    var row = { label: "Health", value: "Not checked yet", note: "", tag: "", tone: "muted" };
    if (!v.known)
        return row;
    var notes = [];
    if (v.attention > 0) {
        row.value = _attentionText(v.attention);
        row.tag = "LOOK";
        row.tone = "warn";
        notes.push(v.titles);
    } else {
        row.value = "All good";
        row.tag = "OK";
        row.tone = "ok";
    }
    if (v.stale)
        notes.push("checked " + ago(v.at, now));
    row.note = notes.join(" · ");
    return row;
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
function summarize(lastRun, audit, updates, now, health) {
    var errors = [];
    var warns = [];

    if (!lastRun && !audit && !updates)
        return { level: "nodata", issues: [], updates: null, updatesDetail: null, updatesChecked: null, updatesPackages: null, lastRun: null, audit: null, health: health || null };

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

    // The package list only inside the same window as the count: stale means no list.
    var pkgs = count !== null && updates.packages ? updates.packages : null;

    return {
        level: errors.length > 0 ? "error" : (warns.length > 0 ? "warn" : "ok"),
        issues: errors.concat(warns),
        updates: count,
        updatesDetail: detail,
        updatesChecked: updates ? updates.checked : null,
        updatesPackages: pkgs,
        lastRun: lastRun,
        audit: audit,
        health: health || null
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
    clearcaches: "Clear caches",
    repair: "Repair broken packages",
    downloads: "Delete old downloads",
    topgrade: "Run topgrade",
    services: "Disable unused services",
    auraudit: "Audit AUR packages",
    packages: "Packages",
    // Retired (they became "Clear caches"), kept so a last-run.json or a
    // history entry written before still shows a name.
    usercache: "Clear user cache",
    wipecache: "Delete entire package cache"
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
//   hero, rows {lastRun, updates, aur}, updateList: see hero() and updateList()
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
    p.hero = hero(s, now);
    p.rows = { lastRun: _lastRunRow(s), updates: _updatesRow(s), aur: _aurRow(s, now), health: _healthRow(s, now) };
    p.updateList = updateList(s, now);
    return p;
}

// --- the popout's hero, rows and update list (draft A, list variant 1) ---

// Mirrors internal/report/restart.go (restartPkg and restartNot): the
// packages whose upgrade a running system keeps using the old copy of until
// it restarts. packaging/dms TestRestartRuleMatchesGo runs a table of names
// through both and fails on any difference. A kernel is not on this list (Go
// finds it through /usr/lib/modules, see isKernelPkg for the list's tag).
var RESTART_RE = /^(lib32-)?(systemd|mesa|nvidia(-[a-z0-9-]+)?)$/;
var RESTART_NOT = { "nvidia-settings": true, "nvidia-prime": true, "lib32-nvidia-settings": true };

function isRestartPkg(name) {
    return typeof name === "string" && RESTART_RE.test(name) && !_own(RESTART_NOT, name);
}

// isKernelPkg: a kernel package by name, for the update list only: "linux" or
// "linux-<flavour>" (linux-cachyos, linux-lts, linux-zen, a flavour's module
// packages such as linux-cachyos-nvidia-open), not its headers or docs,
// linux-firmware(-*), linux-tools* or linux-api-headers.
var KERNEL_RE = /^linux(-[a-z0-9]+)*$/;
var KERNEL_NOT_RE = /(-headers|-docs)$|^linux-firmware(-|$)|^linux-tools/;

function isKernelPkg(name) {
    return typeof name === "string" && KERNEL_RE.test(name) && !KERNEL_NOT_RE.test(name);
}

// _headline: the hero headline of an issue (its colour comes from the level).
function _headline(issue) {
    switch (issue.kind) {
    case "failed":
        return "Last run failed";
    case "incomplete":
        return "Last run incomplete";
    case "aborted":
        return "Last run aborted";
    case "cancelled":
        return "Last run cancelled";
    case "notauthorised":
        return "Last run not authorised";
    }
    return issue.text; // "N AUR warnings", "No cleanup for 16 days"
}

function _tone(role) {
    return { primary: "ok", error: "danger", warning: "warn" }[role] || "muted";
}

// hero(s, now): {tone ("ok" "warn" "danger" "neutral" "muted"), kicker, headline, sub,
// attention:[string]}. The headline says the state in words: the worst issue
// (a failed run before AUR warnings), else the run's first attention item
// ("Restart recommended"), else "N updates waiting", else "Everything is up
// to date". The attention lines never repeat the headline.
function hero(s, now) {
    var r = s.lastRun, a = s.audit;
    var h = { tone: "ok", kicker: r ? "LAST RUN · " + ago(r.finished, now).toUpperCase() : "NO RUN YET", headline: "", sub: "", attention: [] };
    if (s.level === "nodata") {
        h.tone = "muted";
        h.headline = "No data yet";
        h.sub = "Run archmaid once";
        return h;
    }
    var att = r && r.summary ? r.summary.attention : [];
    var hv = _healthView(s.health, now);
    var held = s.updates !== null && s.updatesDetail ? s.updatesDetail.aurHeld : 0;
    var waiting = s.updates !== null && s.updates > 0;
    var subs = [];
    var lines = [];
    var waitingHead = false;
    var clean = false;
    if (s.issues.length > 0) {
        var main = s.issues[0];
        for (var i = 0; i < s.issues.length; i++) {
            if (s.issues[i].kind !== "aur") {
                main = s.issues[i];
                break;
            }
        }
        h.tone = s.level === "error" ? "danger" : "warn";
        h.headline = _headline(main);
        if (r && r.stepsFailed > 0 && main.kind === "failed")
            subs.push(plural(r.stepsFailed, "step", "steps") + " failed");
        if (r && r.stepsRefused > 0 && main.kind !== "notauthorised")
            subs.push(plural(r.stepsRefused, "step", "steps") + " not authorised");
        if (a && a.highCount > 0 && main.kind !== "aur")
            subs.push(plural(a.highCount, "AUR warning", "AUR warnings"));
        // The AUR finding itself is the AUR row's; the sub line counts them.
        lines = att.slice();
    } else if (att.length > 0) {
        h.tone = "warn";
        h.headline = att[0];
        lines = att.slice(1);
    } else if (hv.attention > 0) {
        // Health only speaks when nothing from the run or the audit does.
        h.tone = "warn";
        h.headline = _attentionText(hv.attention);
    } else if (waiting) {
        h.headline = plural(s.updates, "update", "updates") + " waiting";
        h.tone = "neutral"; // pending is not "up to date" (green), nor a problem
        waitingHead = true;
    } else {
        h.headline = "Everything is up to date";
        clean = true;
    }
    if (waitingHead || clean) {
        if (r)
            subs.push("Cleaned " + ago(r.finished, now) + (clean && s.updates === 0 ? ", nothing pending" : ""));
    }
    var upd = waiting && !waitingHead ? plural(s.updates, "update", "updates") + " waiting" : "";
    if (held > 0)
        upd = upd ? upd + " · " + held + " held" : held + " held";
    // With an issue as the headline the Updates row says all this: no updates part.
    if (s.issues.length > 0)
        upd = "";
    if (upd !== "")
        subs.push(upd);
    else if (s.updates === null && s.updatesChecked !== null && s.updatesChecked !== undefined)
        subs.push("Update check is " + plural(Math.floor(_ageDays(s.updatesChecked, now)), "day", "days") + " old");
    h.sub = subs.join(" · ");
    var seen = h.headline.toLowerCase();
    h.attention = lines.filter(function (l) {
        return l.toLowerCase() !== seen;
    }).slice(0, 3);
    return h;
}

// The three compact rows, each {label, value, tag, tone}; tag "" is none.
function _lastRunRow(s) {
    var r = s.lastRun;
    if (!r)
        return { label: "Last run", value: "No run recorded", tag: "", tone: "muted" };
    return { label: "Last run", value: actionLabel(r.action), tag: outcomeTag(r.outcome, r.stepsRefused), tone: _tone(outcomeTint(r.outcome, r.stepsRefused)) };
}

// The Updates row: the count in the tag excludes held updates (as the
// script's count does), so "9 repo · 1 AUR · 2 held" reads "10 NEW".
function _updatesRow(s) {
    var d = s.updatesDetail;
    var row = { label: "Updates", value: "Not checked yet", tag: "", tone: "muted" };
    if (s.updates === null) {
        if (s.updatesChecked !== null && s.updatesChecked !== undefined) {
            row.value = "Unknown";
            row.tag = "STALE";
            row.tone = "warn";
        }
        return row;
    }
    var held = d ? d.aurHeld : 0;
    var heldPart = held > 0 ? " · " + held + " held" : "";
    if (s.updates === 0) {
        row.value = "Up to date" + heldPart;
        row.tag = "UP TO DATE";
        row.tone = "ok";
        return row;
    }
    row.tag = s.updates + " NEW";
    if (!d)
        row.value = "Pending";
    else if (d.aur === null)
        row.value = d.repo + " repo · AUR unknown";
    else
        row.value = d.repo + " repo · " + d.aur + " AUR" + heldPart;
    return row;
}

// The AUR row: "NO HIGH" (not "CLEAN") when the audit found no high finding.
function _aurRow(s, now) {
    var a = s.audit;
    var row = { label: "AUR", value: "No audit yet", reason: "", tag: "", tone: "muted" };
    if (!a)
        return row;
    if (a.highCount > 0) {
        // The package is the value; the reason is the muted line under the row
        // (the first finding's, the tag carries the count).
        row.value = a.high[0].pkg;
        row.reason = a.high[0].reason;
        row.tag = a.highCount + " HIGH";
        row.tone = "danger";
    } else {
        row.value = a.packages + " audited" + (a.checked !== null ? " · " + ago(a.checked, now) : "");
        row.tag = "NO HIGH";
        row.tone = "ok";
    }
    return row;
}

function _byName(x, y) {
    return x.name < y.name ? -1 : (x.name > y.name ? 1 : 0);
}

var HELD_TITLE = "Held · cooldown 72 h";

// updateList(s, now): the unfoldable list under the Updates row, or null
// when there is none (a v:1/v:2 file, no "packages", an empty list, or a
// count older than the 3-day window).
//   {items:[{name, from, to, section, tag, restart}], sections:[title],
//    listed, more, checked:"checked 17 min ago"}
// Sections in order Repo, AUR, "Held · cooldown 72 h" (empty ones are left
// out). In Repo the restart-worthy packages come first, then by name. tag is
// "restart" or "held 1d9h" ("held" without an age), else "". more = repo + aur
// + aur_held - listed (what the writer's cap cut), never below 0.
function updateList(s, now) {
    var pk = s.updatesPackages;
    if (!pk || pk.length === 0)
        return null;
    var repo = [], aur = [], held = [];
    for (var i = 0; i < pk.length; i++) {
        var p = pk[i];
        var it = { name: p.name, from: p.from, to: p.to, section: "", tag: "", restart: false };
        if (p.src === "repo") {
            it.restart = isRestartPkg(p.name) || isKernelPkg(p.name);
            it.tag = it.restart ? "restart" : "";
            it.section = "Repo";
            repo.push(it);
        } else if (p.held) {
            it.tag = p.age ? "held " + p.age : "held";
            it.section = HELD_TITLE;
            held.push(it);
        } else {
            it.section = "AUR";
            aur.push(it);
        }
    }
    repo.sort(function (x, y) {
        return x.restart !== y.restart ? (x.restart ? -1 : 1) : _byName(x, y);
    });
    aur.sort(_byName);
    held.sort(_byName);
    var items = repo.concat(aur, held);
    var d = s.updatesDetail;
    var total = d ? d.repo + (d.aur || 0) + d.aurHeld : items.length;
    var sections = [];
    if (repo.length > 0)
        sections.push("Repo");
    if (aur.length > 0)
        sections.push("AUR");
    if (held.length > 0)
        sections.push(HELD_TITLE);
    return {
        items: items,
        sections: sections,
        listed: items.length,
        more: Math.max(0, total - items.length),
        checked: "checked " + ago(s.updatesChecked, now)
    };
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
    if (s < 3600)
        return Math.min(59, Math.round(s / 60)) + " min ago";
    if (s < 129600)
        return Math.floor(s / 3600) + " h ago";
    return plural(Math.floor(s / 86400), "day", "days") + " ago";
}

// launchPlan decides what "Run Full Cleanup" starts. Pure: the caller says
// which binaries exist and what $TERMINAL holds.
//   hasGui      archmaid-gui is on PATH
//   terminal    value of $TERMINAL ("" or undefined when unset)
//   hasXdgTerm  xdg-terminal-exec is on PATH
// The TUI fallback passes --full-cleanup, so it opens Full Cleanup's gate like
// the GUI's --confirm fullcleanup does.
// Returns {argv:[...]} or {error:"..."}. argv is never run through a shell.
function launchPlan(hasGui, terminal, hasXdgTerm) {
    if (hasGui)
        return { argv: ["archmaid-gui", "--confirm", "fullcleanup"] };
    var words = (typeof terminal === "string" ? terminal : "").trim().split(/\s+/).filter(function (w) {
        return w.length > 0;
    });
    if (words.length > 0)
        return { argv: words.concat(["-e", "archmaid", "--full-cleanup"]) };
    if (hasXdgTerm)
        return { argv: ["xdg-terminal-exec", "archmaid", "--full-cleanup"] };
    return { error: "Set $TERMINAL (for example kitty) or install xdg-terminal-exec to open the archmaid TUI" };
}

// --- the hero's fill: always a surface its text can be read on (#301) ---
//
// The hero sits on DMS's card surface. In the test VM (DMS's default theme)
// that binding left the Rectangle at its default, opaque white, under the
// theme's pale text. So the fill is checked: a colour is {r, g, b, a} in 0..1
// (a QML color has these), and heroFill answers the candidate only when the
// text can be read on it (WCAG contrast of at least 4.5, the candidate laid
// over the popout's own surface); otherwise the dark or the light fill that
// reads best against the text.

var HERO_DARK = { r: 0x26 / 255, g: 0x23 / 255, b: 0x2d / 255, a: 1 };
var HERO_LIGHT = { r: 0xee / 255, g: 0xea / 255, b: 0xf3 / 255, a: 1 };
var MIN_CONTRAST = 4.5;

function _isColor(c) {
    return c !== null && typeof c === "object" && typeof c.r === "number" && typeof c.g === "number" && typeof c.b === "number" && isFinite(c.r + c.g + c.b);
}

function _alpha(c) {
    return typeof c.a === "number" && isFinite(c.a) ? Math.max(0, Math.min(1, c.a)) : 1;
}

// over(top, bottom): top laid over bottom, opaque (bottom missing: over white,
// what an empty window shows).
function over(top, bottom) {
    var b = _isColor(bottom) ? bottom : { r: 1, g: 1, b: 1, a: 1 };
    var a = _alpha(top);
    return { r: top.r * a + b.r * (1 - a), g: top.g * a + b.g * (1 - a), b: top.b * a + b.b * (1 - a), a: 1 };
}

function _lin(v) {
    return v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4);
}

function luminance(c) {
    return 0.2126 * _lin(c.r) + 0.7152 * _lin(c.g) + 0.0722 * _lin(c.b);
}

// contrast(fg, bg): the WCAG ratio, 1 to 21; fg is laid over bg when it is
// translucent.
function contrast(fg, bg) {
    var back = over(bg, null);
    var l1 = luminance(over(fg, back)), l2 = luminance(back);
    return (Math.max(l1, l2) + 0.05) / (Math.min(l1, l2) + 0.05);
}

function heroFill(candidate, base, text) {
    if (_isColor(text) && _isColor(candidate) && contrast(text, over(candidate, base)) >= MIN_CONTRAST)
        return candidate;
    if (!_isColor(text))
        return HERO_DARK;
    var onDark = contrast(text, HERO_DARK), onLight = contrast(text, HERO_LIGHT);
    return onDark >= onLight ? HERO_DARK : HERO_LIGHT;
}

// openGuiPlan is what the Health row's click starts: the GUI at its first
// page (the Overview, where the System health cell is), or null when it is
// not installed (the row is then not a button). argv is never run through a
// shell.
function openGuiPlan(hasGui) {
    return hasGui ? { argv: ["archmaid-gui"] } : null;
}
