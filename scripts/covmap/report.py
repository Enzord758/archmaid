#!/usr/bin/env python3
"""Turns classify.py's output into docs/coverage-map.json and the generated
tables of docs/coverage-map.md (totals, per package, per block).

usage: report.py CLASSIFIED.json SUMMARY.txt OUT.json OUT.md
SUMMARY.txt is cover-merge.sh's summary of the same merged run (its core,
helper and gui lines give the denominators).
"""
import json, collections, re, sys

d = json.load(open(sys.argv[1]))
summary, out_json, out_md = sys.argv[2], sys.argv[3], sys.argv[4]
tot = {}
for line in open(summary):
    m = re.match(r"^(core|helper|gui)\s+[\d.]+% \((\d+)/(\d+)\)", line)
    if m:
        tot[m.group(1)] = (int(m.group(3)), int(m.group(2)))
# gui/cmd/archmaid-gui is linked by no test, so it is in no run's data.
tot['gui'] = (tot['gui'][0] + 6, tot['gui'][1])
EXTRA = [{"file": "gui/cmd/archmaid-gui/main.go", "lines": "all", "func": "main", "stmts": 6, "cat": "B",
          "reason": "the GUI binary's main: in no run's data at all (no test links it); a headless start of the cover archmaid-gui counts it",
          "home": "gui/scripts/headless.sh + cover archmaid-gui (new)"}]
d = d + EXTRA
CATS = ['A', 'B', 'C', 'D', 'E', 'pending']

def scope(f):
    if f.startswith('gui/'): return 'gui'
    if f.startswith('cmd/archmaid-helper/'): return 'helper'
    return 'core'

c = collections.Counter()
for b in d:
    c[(scope(b['file']), b['cat'])] += b['stmts']
tot['all'] = tuple(sum(v[i] for v in tot.values()) for i in (0, 1))

md = []
md.append("| Scope | Statements | Covered now | Uncovered | A unit | B e2e | C vm | D owner | E unreachable | pending Dev 3 |")
md.append("|---|---|---|---|---|---|---|---|---|---|")
after = []
for s in ['core', 'helper', 'gui', 'all']:
    T, C = tot[s]
    scopes = ['core', 'helper', 'gui'] if s == 'all' else [s]
    g = {k: sum(c[(x, k)] for x in scopes) for k in CATS}
    pct = lambda n: "%.1f %%" % (int(1000 * n / T) / 10)
    md.append(f"| {s} | {T} | {C} ({pct(C)}) | {T-C} | {g['A']} | {g['B']} | {g['C']} | {g['D']} | {g['E']} | {g['pending']} |")
    cum = C
    row = [s, pct(C)]
    for k in ['A', 'B', 'C', 'D']:
        cum += g[k]
        row.append(pct(cum))
    row.append(pct(cum + g['pending']))
    after.append(row)
md.append("")
md.append("| Scope | Now (unit + e2e + GUI) | after A | after A+B | after A+B+C | after A+B+C+D | + pending, if all reachable |")
md.append("|---|---|---|---|---|---|---|")
for r in after:
    md.append("| " + " | ".join(r) + " |")
md.append("")

# per package
pk = collections.OrderedDict()
for b in sorted(d, key=lambda b: b['file']):
    p = b['file'].rsplit('/', 1)[0]
    pk.setdefault(p, collections.Counter())[b['cat']] += b['stmts']
md.append("| Package | A | B | C | D | E | pending |")
md.append("|---|---|---|---|---|---|---|")
for p, cc in pk.items():
    md.append(f"| `{p}` | " + " | ".join(str(cc[k]) if cc[k] else "" for k in CATS) + " |")
md.append("")

# blocks grouped by package, then (file, func, cat, reason)
md.append("<!-- blocks -->")
groups = collections.OrderedDict()
for b in d:
    if b['cat'] == 'pending':
        continue
    p = b['file'].rsplit('/', 1)[0]
    key = (b['file'], b['func'], b['cat'], b['reason'], b.get('home', ''), b.get('recommend', ''))
    groups.setdefault(p, collections.OrderedDict()).setdefault(key, []).append(b)
for p, g in groups.items():
    md.append(f"#### `{p}`")
    md.append("")
    md.append("| Lines | Function | Stmts | Cat | Why | Test home / recommendation |")
    md.append("|---|---|---|---|---|---|")
    for (f, fn, cat, why, home, rec), bs in g.items():
        lines = ", ".join(b['lines'] for b in bs)
        n = sum(b['stmts'] for b in bs)
        base = f.rsplit('/', 1)[1]
        extra = rec if cat == 'E' else home
        md.append(f"| `{base}:{lines}` | `{fn}` | {n} | {cat} | {why} | {extra} |")
    md.append("")
open(out_md, "w").write("\n".join(md) + "\n")

meta = {
    "base": "dad922a91d189032b9c7c42cc6bf9eea7e9da924",
    "measured": "scripts/cover-report.sh with COVER_GUI=1: unit (cover-unit.sh) + e2e tapes + GUI headless, merged",
    "categories": {"A": "unit", "B": "e2e", "C": "vm", "D": "owner", "E": "unreachable", "pending": "pending Dev 3's discovery change"},
    "totals": {s: {"stmts": tot[s][0], "covered": tot[s][1]} for s in tot},
    "blocks": d,
}
json.dump(meta, open(out_json, "w"), indent=1, ensure_ascii=False)
print("\n".join(md[:15]))
