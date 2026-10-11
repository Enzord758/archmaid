#!/usr/bin/env python3
"""Turns classify.py's output into docs/coverage-map.json, docs/coverage-exempt.tsv
and docs/coverage-exempt.md, and refreshes the generated regions of
docs/coverage-map.md (between <!-- gen:NAME --> and <!-- /gen:NAME -->).

usage: report.py CLASSIFIED.json SUMMARY.txt BASE_SHA DOCS_DIR
SUMMARY.txt is cover-merge.sh's summary of the same merged run (its core,
helper and gui lines give the denominators). Writes DOCS_DIR/coverage-map.json,
coverage-exempt.tsv, coverage-exempt.md and the regions of coverage-map.md.
"""
import json, collections, re, sys

d = json.load(open(sys.argv[1]))
summary, base, docs = sys.argv[2], sys.argv[3], sys.argv[4]
tot = {}
for line in open(summary):
    m = re.match(r"^(core|helper|gui)\s+[\d.]+% \((\d+)/(\d+)\)", line)
    if m:
        tot[m.group(1)] = (int(m.group(3)), int(m.group(2)))
# gui/cmd/archmaid-gui is linked by no test, so it is in no run's data.
tot['gui'] = (tot['gui'][0] + 6, tot['gui'][1])
EXTRA = [{"file": "gui/cmd/archmaid-gui/main.go", "lines": "all", "func": "main", "stmts": 6, "cat": "B", "security": False,
          "reason": "the GUI binary's main: in no run's data at all (no test links it); a headless start of the cover archmaid-gui counts it",
          "home": "gui/scripts/headless.sh + cover archmaid-gui (new)"}]
d = d + EXTRA
CATS = ['A', 'B', 'C', 'D', 'E']

def scope(f):
    if f.startswith('gui/'): return 'gui'
    if f.startswith('cmd/archmaid-helper/'): return 'helper'
    return 'core'

def pct(n, t):
    return "%.1f %%" % (int(1000 * n / t) / 10)

c = collections.Counter()
for b in d:
    c[(scope(b['file']), b['cat'])] += b['stmts']
tot['all'] = tuple(sum(v[i] for v in tot.values()) for i in (0, 1))

gen = {}

# ---- totals
md = []
md.append("| Scope | Statements | Covered | Raw | Uncovered | A unit | B e2e | C vm | D owner | E exempt | Reachable (covered / (total - E)) |")
md.append("|---|---|---|---|---|---|---|---|---|---|---|")
after = []
for s in ['core', 'helper', 'gui', 'all']:
    T, C = tot[s]
    scopes = ['core', 'helper', 'gui'] if s == 'all' else [s]
    g = {k: sum(c[(x, k)] for x in scopes) for k in CATS}
    md.append(f"| {s} | {T} | {C} | {pct(C, T)} | {T-C} | {g['A']} | {g['B']} | {g['C']} | {g['D']} | {g['E']} | {pct(C, T - g['E'])} ({C}/{T - g['E']}) |")
    R = T - g['E']
    cum = C
    row = [s, f"{pct(C, T)} / {pct(C, R)}"]
    for k in ['A', 'B', 'C', 'D']:
        cum += g[k]
        row.append(f"{pct(cum, T)} / {pct(cum, R)}")
    after.append(row)
md.append("")
md.append("Raw / reachable, cumulative:")
md.append("")
md.append("| Scope | Now (unit + e2e + GUI) | after A | after A+B | after A+B+C | after A+B+C+D (= every reachable statement) |")
md.append("|---|---|---|---|---|---|")
for r in after:
    md.append("| " + " | ".join(r) + " |")
gen['totals'] = md

# ---- per package
pk = collections.OrderedDict()
for b in sorted(d, key=lambda b: b['file']):
    p = b['file'].rsplit('/', 1)[0]
    pk.setdefault(p, collections.Counter())[b['cat']] += b['stmts']
md = ["| Package | A | B | C | D | E |", "|---|---|---|---|---|---|"]
for p, cc in pk.items():
    md.append(f"| `{p}` | " + " | ".join(str(cc[k]) if cc[k] else "" for k in CATS) + " |")
gen['packages'] = md

# ---- blocks (A-D, E is in coverage-exempt.md)
groups = collections.OrderedDict()
for b in d:
    if b['cat'] == 'E':
        continue
    p = b['file'].rsplit('/', 1)[0]
    key = (b['file'], b['func'], b['cat'], b['reason'], b.get('home', ''))
    groups.setdefault(p, collections.OrderedDict()).setdefault(key, []).append(b)
md = []
for p, g in groups.items():
    md.append(f"#### `{p}`")
    md.append("")
    md.append("| Lines | Function | Stmts | Cat | Why | Test home |")
    md.append("|---|---|---|---|---|---|")
    for (f, fn, cat, why, home), bs in g.items():
        lines = ", ".join(b['lines'] for b in bs)
        n = sum(b['stmts'] for b in bs)
        base_name = f.rsplit('/', 1)[1]
        sec = " (security: Dev 3)" if bs[0].get('security') else ""
        md.append(f"| `{base_name}:{lines}` | `{fn}` | {n} | {cat} | {why} | {home}{sec} |")
    md.append("")
gen['blocks'] = md

# ---- work packages: A and B statements, grouped by area
def wp(b):
    f, fn = b['file'], b['func']
    if b['cat'] == 'B':
        if f.startswith('cmd/archmaid-helper/'):
            return 7
        return 8
    if f.startswith(('internal/testenv/', 'internal/testtranscript/', 'internal/engine/enginetest/')):
        return 1
    if f.startswith(('cmd/archmaid-helper/', 'internal/helperclient/', 'internal/privilege/')):
        return 2
    if f.startswith(('internal/tui/', 'internal/driver/', 'internal/engine/', 'internal/actions/', 'internal/logstore/', 'internal/frontend/')):
        return 3
    if f.startswith(('internal/audit/', 'internal/downloads/', 'internal/proc/', 'internal/sanitize/', 'internal/sysstat/',
                     'gui/internal/polkitagent/', 'gui/internal/realdriver/')):
        return 4
    if f in ('gui/internal/ui/timeline.go', 'gui/internal/ui/logs.go', 'gui/internal/ui/signals.go', 'gui/internal/ui/failed.go') \
            or f.startswith(('gui/internal/logview/', 'gui/internal/overview/')):
        return 5
    if f.startswith('gui/'):
        return 6
    return 0

WP = {
    1: ("Test infrastructure", "testenv's post-`m.Run` checks (orphan reaper, trap report, state-guard compare) called directly inside a test, the recorder's fake targets, the contract checker's own failure paths", "architecture", False),
    2: ("Helper and privilege edge cases", "the helper's fake-runner/Stat/Exe error paths and lock edges, helperclient's malformed and failing helper output, privilege's askpass relays, faillock and kept-password failures", "security", True),
    3: ("Core: TUI keys, driver, engine, actions, logstore", "per-screen key handling and Finished/PromptClosed edges in the TUI, the driver's teardown, hang-up and agent paths, combined-queue edges, discovery outputs, the log store's error paths", "architecture (driver/keep.go and the askpass bits: security)", False),
    4: ("Leaf packages and the polkit agent", "audit/downloads write and scan error paths, proc argv forms, sanitize and sysstat edges, polkitagent and realdriver against fake agents", "performance-QA (polkitagent, realdriver, downloads: security)", True),
    5: ("GUI logs, timeline and signals pages", "new headless scenarios: log page states (none, unreadable, empty), the timeline, signals mid-run, the failed-step view, logview and overview edges", "GUI", False),
    6: ("GUI windows, dialogs, settings, run view", "window/dialog/settings-page scenarios, the run view's SGR and ring edges, settings.Save error paths, shell and usercache cells", "GUI", False),
    7: ("E2E: the cover-helper check", "main and hardenProcess of the helper binary: the run.sh check of the cover helper as the user and as uid 0 in an unprivileged user namespace (both refused before anything runs)", "performance-QA with Dev 3's review (helper)", True),
    8: ("E2E: binary entries", "archmaid-gui's main, `--stub` with each script (headless), the TUI's error exit and the editor handover on a pty tape", "GUI (the TUI tape: architecture)", False),
}
agg = collections.OrderedDict((i, [0, 0, collections.Counter(), collections.Counter()]) for i in WP)
for b in d:
    if b['cat'] not in ('A', 'B'):
        continue
    i = wp(b)
    assert i, b
    agg[i][0] += b['stmts']
    if b.get('security'):
        agg[i][1] += b['stmts']
    agg[i][2][b['file'].rsplit('/', 1)[0]] += b['stmts']
TA = tot['all'][0]
md = ["| # | Package | Statements | Gain (all) | Role | Security code | Covers |", "|---|---|---|---|---|---|---|"]
for i, (name, what, role, secflag) in WP.items():
    n, nsec, pks, _ = agg[i]
    cat = "B" if i >= 7 else "A"
    secn = f"{nsec} stmts: Dev 3 reviews" if nsec else "no"
    pkgs = ", ".join(f"`{p.replace('internal/', '').replace('gui/internal/', 'gui/')}` {v}" for p, v in pks.most_common(6))
    md.append(f"| {i} | **{name}** ({cat}) | {n} | +{int(1000 * n / TA) / 10:.1f} pts | {role} | {secn} | {what}. By package: {pkgs} |")
n_all = sum(a[0] for a in agg.values())
md.append(f"| | **A and B together** | {n_all} | +{int(1000 * n_all / TA) / 10:.1f} pts | | | |")
gen['workpackages'] = md

# ---- the exempt list
E = sorted((b for b in d if b['cat'] == 'E'), key=lambda b: (b['file'], int(b['lines'].split('-')[0])))
tsv = ["# docs/coverage-exempt.tsv: generated by scripts/covmap/report.py, read by scripts/covblocks -reachable",
       "# file\tlines\tfunc\tstmts\trecommendation\tsecurity\twhy"]
for b in E:
    tsv.append("\t".join([b['file'], b['lines'], b['func'], str(b['stmts']), b['recommend'], "yes" if b['security'] else "no", b['reason']]))
open(f"{docs}/coverage-exempt.tsv", "w").write("\n".join(tsv) + "\n")

ec = collections.Counter()
for b in E:
    ec[(b['recommend'], b['security'])] += b['stmts']
ecb = collections.Counter(b['recommend'] for b in E)
md = ["# Coverage exemptions (category E)", "",
      f"Statements no test or run can reach, as measured at `{base[:7]}` ({sum(b['stmts'] for b in E)} statements in {len(E)} blocks). "
      "Machine-readable copy: [`coverage-exempt.tsv`](coverage-exempt.tsv), which `scripts/covblocks -reachable -exempt` reads "
      "(`make e2e-cover` prints both figures per scope; `scripts/check.sh` prints the core figure). "
      "Why the list exists: Go has no pragma to leave a branch out of the count, so raw coverage reaches 100 % only if these "
      "branches go. The goal is **100 % of reachable statements**: covered / (total - exempt). Every exemption is one line "
      "here, in review; an entry whose lines moved, or that a test now covers, is reported as stale and takes nothing out of "
      "the denominator, so a drifting list can only lower the reachable figure, never inflate it.", "",
      "Refreshing: after a code change that moves a listed block, re-run the measurement (docs/coverage-map.md, \"How it was measured\") "
      "and `scripts/covmap/report.py`; do not edit the tsv by hand.", "",
      "## Recommendations", "",
      "| Recommendation | Means | Blocks | Statements | of which security code |",
      "|---|---|---|---|---|"]
means = {"keep-exempt": "the branch is right as written (defensive, fail-closed, or a platform fallback); it stays in the exempt list",
         "make-testable": "a small seam turns it into a unit test (A): the shared atomic-write helper, or a hook between a TOCTOU check and its use",
         "remove": "dead code: delete the branch (a reviewer confirms it cannot be reached)"}
for r in ("keep-exempt", "make-testable", "remove"):
    n = sum(v for (k, s), v in ec.items() if k == r)
    ns = ec[(r, True)]
    md.append(f"| `{r}` | {means[r]} | {ecb[r]} | {n} | {ns} |")
md += ["", "**Security code** (`yes` in the tsv): `cmd/archmaid-helper`, `internal/privilege`, `internal/helperclient`, `internal/ttyguard`, "
       "`gui/internal/polkitagent`, `gui/internal/realdriver`, `internal/downloads` (it deletes the owner's files), "
       "`internal/driver/keep.go`, askpass in `engine` and `tui`. Dev 3 reviews any removal, seam or test there before it lands; "
       "the others need only the usual review.", ""]
by = collections.OrderedDict()
for r in ("make-testable", "remove", "keep-exempt"):
    by[r] = [b for b in E if b['recommend'] == r]
for r, bs in by.items():
    md += [f"## `{r}` ({sum(b['stmts'] for b in bs)} statements)", ""]
    if r == "make-testable":
        md += ["The atomic-write pattern (CreateTemp, write, chmod, fsync, close, rename, fsync the directory) is copied into "
               "`audit.WriteSnapshot`, `audit.WriteState`, `logstore.WriteLastRun` and `gui/internal/settings.Save`. "
               "One shared helper taking an injectable file turns those branches into unit tests and removes three copies. "
               "The downloads TOCTOU guards need a hook between the check and the use (like `Options.EntryHook`); that is "
               "security code, so Dev 3 decides.", ""]
    md += ["| Where | Function | Stmts | Security | Why it can't run |", "|---|---|---|---|---|"]
    for b in bs:
        md.append(f"| `{b['file']}:{b['lines']}` | `{b['func']}` | {b['stmts']} | {'yes' if b['security'] else ''} | {b['reason']} |")
    md.append("")
open(f"{docs}/coverage-exempt.md", "w").write("\n".join(md))

# ---- the json
meta = {
    "base": base,
    "measured": "scripts/cover-report.sh with COVER_GUI=1: unit (cover-unit.sh) + e2e tapes + GUI headless, merged",
    "categories": {"A": "unit", "B": "e2e", "C": "vm", "D": "owner", "E": "unreachable (docs/coverage-exempt.md)"},
    "totals": {s: {"stmts": tot[s][0], "covered": tot[s][1], "exempt": sum(c[(x, 'E')] for x in (['core', 'helper', 'gui'] if s == 'all' else [s]))} for s in tot},
    "work_packages": {str(i): {"name": WP[i][0], "stmts": agg[i][0], "security_stmts": agg[i][1], "role": WP[i][2]} for i in WP},
    "blocks": d,
}
for b in meta["blocks"]:
    if b['cat'] in 'AB' and b['cat'] != 'E' and 'file' in b:
        b['package'] = wp(b)
json.dump(meta, open(f"{docs}/coverage-map.json", "w"), indent=1, ensure_ascii=False)

# ---- splice the regions into coverage-map.md
path = f"{docs}/coverage-map.md"
text = open(path).read()
for name, lines in gen.items():
    pat = re.compile(r"(<!-- gen:%s -->\n)(?:.*?\n)?(<!-- /gen:%s -->)" % (name, name), re.S)
    if not pat.search(text):
        sys.exit(f"report.py: no <!-- gen:{name} --> region in {path}")
    text = pat.sub(lambda m: m.group(1) + "\n".join(lines) + "\n" + m.group(2), text)
open(path, "w").write(text)
print("\n".join(gen['totals']))
print("exempt:", {k: v for k, v in sorted(ec.items())})
print("work packages:", {i: agg[i][0] for i in agg})
