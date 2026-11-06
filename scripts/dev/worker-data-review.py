"""Worker data review: every table in docs/design/model-capability-review.md.

Reads a copy of the gateway database, read only, and prints the tables as
Markdown. Standard library only.

    cp ~/.local/state/agent-net/gateway/gateway.db* /tmp/gw/
    python3 scripts/dev/worker-data-review.py /tmp/gw/gateway.db

Never point this script at the live database. It opens the file with mode=ro
and does not write.

How a task is counted: worker_runs keeps one row each time a run is reported,
so the same session appears several times with the same start time. The script
folds those rows into one task per session: outcome, model, duration and level
come from the last row, and the rework counters are summed because they are
per-report increments.
"""
import collections
import datetime
import json
import math
import sqlite3
import statistics
import sys

FIX_MIN = 10          # the plan's minutes per fix round
CONTINUE_MIN = 2      # the plan's minutes per continue nudge
DEFECT_MIN = {1: 20, 2: 45, 3: 90, 4: 180}  # the plan's minutes per late bug
DIMS = ["reasoning", "scope", "coupling", "uncertainty", "verification", "risk"]
LABELS = ["trivial", "simple", "normal", "hard", "very_complex"]
CUTS = [2.25, 3.0, 3.75]

# Benchmark scores bundled in internal/protocol/scores.json at the medium or
# named effort. Used only to rebuild the plan's prior, never as an outcome.
SCORES = {
    "gpt-6-luna:xhigh": 23.3,
    "opencode-go/deepseek-v4.1-flash": 32.5,
    "gpt-6.1-terra:high": 42.3,
    "gpt-6.1-terra": 38.1,
    "gpt-6.1-terra:medium": 38.1,
    "gpt-6.1-terra:max": 51.0,
    "gpt-6.1-terra:low": 29.2,
    "gpt-6.1-sol:medium": 46.1,
    "gpt-6.1-sol:high": 50.2,
    "gpt-6.1-sol:low": 34.5,
    "gemini-3.8-flash-medium": 30.0,
    "gemini-4-argon-high": 50.4,
    "claude-opus-5-5:high": 51.7,
    "claude-opus-5-5:xhigh": 57.0,
    "claude-sonnet-5-5:high": 41.1,
}
# The capability C printed in the stored plan (2026-11-02), to show drift.
PLAN_C = {
    "gpt-6-luna:xhigh": 1.97,
    "opencode-go/deepseek-v4.1-flash": 2.37,
    "gpt-6.1-terra:high": 3.10,
    "gpt-6.1-sol:medium": 3.61,
    "gpt-6.1-terra:max": 3.73,
}


def pct(a, b):
    return 100.0 * a / b if b else float("nan")


def fmt(v, nd=1):
    if v is None or (isinstance(v, float) and math.isnan(v)):
        return "-"
    return f"{v:.{nd}f}"


def pearson(xs, ys):
    n = len(xs)
    if n < 3:
        return None
    mx, my = sum(xs) / n, sum(ys) / n
    sx = math.sqrt(sum((x - mx) ** 2 for x in xs))
    sy = math.sqrt(sum((y - my) ** 2 for y in ys))
    if sx == 0 or sy == 0:
        return None
    return sum((x - mx) * (y - my) for x, y in zip(xs, ys)) / (sx * sy)


def rank(values):
    order = sorted(range(len(values)), key=lambda i: values[i])
    out = [0.0] * len(values)
    i = 0
    while i < len(values):
        j = i
        while j + 1 < len(values) and values[order[j + 1]] == values[order[i]]:
            j += 1
        for k in range(i, j + 1):
            out[order[k]] = (i + j) / 2.0 + 1
        i = j + 1
    return out


def spearman(xs, ys):
    if len(xs) < 3:
        return None
    return pearson(rank(xs), rank(ys))


def ols(rows, ys):
    """Least squares with an intercept. Returns (coefficients, r squared)."""
    p = len(rows[0]) + 1
    x = [[1.0] + list(r) for r in rows]
    n = len(x)
    a = [[sum(x[i][u] * x[i][v] for i in range(n)) for v in range(p)] for u in range(p)]
    b = [sum(x[i][u] * ys[i] for i in range(n)) for u in range(p)]
    for c in range(p):
        pivot = max(range(c, p), key=lambda r: abs(a[r][c]))
        a[c], a[pivot] = a[pivot], a[c]
        b[c], b[pivot] = b[pivot], b[c]
        if abs(a[c][c]) < 1e-12:
            return None, None
        for r in range(c + 1, p):
            f = a[r][c] / a[c][c]
            for k in range(c, p):
                a[r][k] -= f * a[c][k]
            b[r] -= f * b[c]
    beta = [0.0] * p
    for c in range(p - 1, -1, -1):
        beta[c] = (b[c] - sum(a[c][k] * beta[k] for k in range(c + 1, p))) / a[c][c]
    mean = sum(ys) / n
    ss = sum((v - mean) ** 2 for v in ys)
    res = sum((ys[i] - sum(x[i][k] * beta[k] for k in range(p))) ** 2 for i in range(n))
    return beta[1:], 1 - res / ss if ss else None


def score_to_d(score):
    """The plan's straight line through the level minimums."""
    points = [(20, 1.5), (30, 2.25), (40, 3.0), (50, 3.75)]
    if score < points[1][0]:
        (s0, d0), (s1, d1) = points[0], points[1]
    elif score >= points[-2][0]:
        (s0, d0), (s1, d1) = points[-2], points[-1]
    else:
        (s0, d0), (s1, d1) = next((p, q) for p, q in zip(points, points[1:]) if p[0] <= score < q[0])
    return d0 + (score - s0) * (d1 - d0) / (s1 - s0)


def family(model):
    m = (model or "").lower()
    for f in ("deepseek", "terra", "sol", "luna", "opus", "sonnet", "gemini", "argon", "claude"):
        if f in m:
            return f
    return m or "?"


def effort(model):
    m = model or ""
    if ":" in m:
        return m.rsplit(":", 1)[1]
    if "-" in m:
        tail = m.rsplit("-", 1)[1]
        if tail in ("low", "medium", "high", "xhigh", "minimal"):
            return tail
    return "default"


def table(headers, rows, align=None):
    align = align or ["l"] + ["r"] * (len(headers) - 1)
    sep = {"l": ":---", "r": "---:", "c": ":---:"}
    out = ["| " + " | ".join(headers) + " |", "| " + " | ".join(sep[a] for a in align) + " |"]
    for r in rows:
        out.append("| " + " | ".join(str(c) for c in r) + " |")
    return "\n".join(out)


def main(path):
    db = sqlite3.connect(f"file:{path}?mode=ro", uri=True)
    db.row_factory = sqlite3.Row
    q = lambda s, p=(): [dict(r) for r in db.execute(s, p)]

    # ---- fold runs into tasks -------------------------------------------
    runs = q("SELECT * FROM worker_runs ORDER BY id")
    tasks = {}
    for r in runs:
        t = tasks.setdefault(r["session_id"], {"rows": [], "continue": 0, "fix": 0, "auto": 0, "midrun": 0})
        t["rows"].append(r)
        t["continue"] += r["continue_count"]
        t["fix"] += r["fix_count"]
        t["auto"] += r["auto_continue_count"]
        t["midrun"] += r["midrun_count"]
    for sid, t in tasks.items():
        last = t["rows"][-1]
        t.update(
            sid=sid,
            model=last["model"],
            harness=last["harness"],
            outcome=last["outcome"],
            seconds=last["run_seconds"],
            difficulty=last["difficulty"],
            difficulty_used=last["difficulty_used"],
            started=last["started_at"],
            ended=last["ended_at"],
        )
        t["rework"] = t["continue"] + t["fix"]

    assessments = {}
    for a in q("SELECT * FROM worker_assessments ORDER BY assessed_at"):
        assessments[a["session_id"]] = a  # latest wins
    ratings = {r["session_id"]: r for r in q("SELECT * FROM worker_ratings")}
    defects = collections.defaultdict(list)
    for d in q("SELECT * FROM worker_defects"):
        defects[d["session_id"]].append(d)
    changes = []
    for c in q("SELECT * FROM delivery_changes ORDER BY seq"):
        c["origin"] = json.loads(c["origin"])
        changes.append(c)
    evidence = collections.defaultdict(list)
    for e in q("SELECT * FROM delivery_evidence ORDER BY seq"):
        e["fields"] = json.loads(e["fields"] or "{}")
        evidence[e["change_id"]].append(e)

    def judged(t):
        a = assessments.get(t["sid"])
        return a if a and not a["error"] else None

    def level(t):
        a = judged(t)
        if a:
            return a["label"]
        return t["difficulty_used"] or t["difficulty"]

    def dvalue(t):
        a = judged(t)
        return a["d"] if a else None

    def my_minutes(t):
        return (FIX_MIN * t["fix"] + CONTINUE_MIN * t["continue"]
                + sum(DEFECT_MIN.get(d["severity"], 0) for d in defects[t["sid"]]))

    for t in tasks.values():
        t["level"] = level(t)
        t["D"] = dvalue(t)
        t["my_minutes"] = my_minutes(t)
        t["rating"] = ratings[t["sid"]]["score"] if t["sid"] in ratings else None
        t["defect_rows"] = defects[t["sid"]]

    print("# Worker data review tables\n")
    print(f"Database copy: `{path}` (opened read only).")
    print(f"Run at {datetime.datetime.now().strftime('%Y-%m-%d %H:%M')}.\n")

    # ---- A. coverage ----------------------------------------------------
    print("## A. Data and coverage\n")
    counts = [
        ("worker_runs rows", len(runs)),
        ("tasks (distinct sessions)", len(tasks)),
        ("assessment rows", len(q("SELECT * FROM worker_assessments"))),
        ("assessed tasks (latest row per session)", len(assessments)),
        ("assessed tasks with a usable D", sum(1 for t in tasks.values() if t["D"] is not None)),
        ("ratings", len(ratings)),
        ("rating offers", len(q("SELECT * FROM worker_rate_offers"))),
        ("late defects", len(q("SELECT * FROM worker_defects"))),
        ("delivery changes", len(changes)),
        ("delivery evidence rows", sum(len(v) for v in evidence.values())),
        ("tasks with a delivery change", sum(1 for t in tasks.values() if any(c["origin"].get("session_id") == t["sid"] for c in changes))),
    ]
    print(table(["Item", "Count"], counts))
    first = datetime.datetime.fromtimestamp(min(t["started"] for t in tasks.values()))
    last = datetime.datetime.fromtimestamp(max(t["ended"] for t in tasks.values()))
    print(f"\nWindow: {first:%Y-%m-%d %H:%M} to {last:%Y-%m-%d %H:%M}.")
    print(f"Rating coverage: {len(ratings)} of {len(tasks)} tasks ({pct(len(ratings), len(tasks)):.0f}%).")
    print(f"Rated when offered: {len(ratings)} of {len(q('SELECT * FROM worker_rate_offers'))} offers.")
    print("No table stores tokens, cost or active model minutes. `run_seconds` is wall clock from start to hand-back and includes queue and quota waits.\n")

    # ---- B. outcomes by model and harness ------------------------------
    print("## B. Outcomes by model and harness\n")

    def agg(rows):
        rated = [t for t in rows if t["rating"] is not None]
        mins = [t["seconds"] / 60 for t in rows]
        return {
            "n": len(rows),
            "complete": pct(sum(1 for t in rows if t["outcome"] == "complete"), len(rows)),
            "partial": pct(sum(1 for t in rows if t["outcome"] == "partial"), len(rows)),
            "failed": pct(sum(1 for t in rows if t["outcome"] in ("failed", "timed_out", "infra")), len(rows)),
            "stopped": pct(sum(1 for t in rows if t["outcome"] == "stopped"), len(rows)),
            "rated": len(rated),
            "rating": statistics.mean(t["rating"] for t in rated) if rated else None,
            "fives": pct(sum(1 for t in rated if t["rating"] == 5), len(rated)),
            "rework": statistics.mean(t["rework"] for t in rows),
            "fix": statistics.mean(t["fix"] for t in rows),
            "defects": sum(len(t["defect_rows"]) for t in rows),
            "sev": statistics.mean([d["severity"] for t in rows for d in t["defect_rows"]]) if any(t["defect_rows"] for t in rows) else None,
            "mins": statistics.median(mins),
            "p90": sorted(mins)[int(0.9 * (len(mins) - 1))],
        }

    groups = collections.defaultdict(list)
    for t in tasks.values():
        groups[(t["harness"], t["model"], effort(t["model"]))].append(t)
    rows = []
    for (h, m, e), ts in sorted(groups.items(), key=lambda kv: -len(kv[1])):
        if len(ts) < 5:
            continue
        a = agg(ts)
        rows.append([h, m, e, a["n"], f"{a['complete']:.0f}", f"{a['partial']:.0f}", f"{a['failed']:.0f}",
                     f"{a['stopped']:.0f}", f"{a['rating']:.2f}" if a["rating"] else "-", a["rated"],
                     fmt(a["fives"], 0), f"{a['rework']:.2f}", f"{a['fix']:.2f}",
                     a["defects"], fmt(a["sev"]), f"{a['mins']:.0f}", f"{a['p90']:.0f}"])
    print(table(["Harness", "Model", "Effort", "Tasks", "Done %", "Part %", "Fail %", "Stop %",
                 "Rating", "Rated", "5s %", "Rework", "Fixes", "Defects", "Sev", "Med min", "P90 min"], rows))
    print("\nModels with fewer than 5 tasks are left out. Fail includes failed, timed out and infra.")

    print("\n### By harness\n")
    rows = []
    for h in sorted({t["harness"] for t in tasks.values()}):
        ts = [t for t in tasks.values() if t["harness"] == h]
        a = agg(ts)
        rows.append([h, a["n"], f"{a['complete']:.0f}", f"{a['partial']:.0f}", f"{a['failed']:.0f}",
                     f"{a['rating']:.2f}" if a["rating"] else "-", a["rated"], fmt(a["fives"], 0),
                     f"{a['rework']:.2f}", f"{a['fix']:.2f}", a["defects"], fmt(a["sev"]),
                     f"{a['mins']:.0f}", f"{a['p90']:.0f}"])
    print(table(["Harness", "Tasks", "Done %", "Part %", "Fail %", "Rating", "Rated", "5s %",
                 "Rework", "Fixes", "Defects", "Sev", "Med min", "P90 min"], rows))

    # ---- C. outcomes by level and by D bucket --------------------------
    print("\n## C. Outcomes by level and by D bucket\n")
    rows = []
    for lvl in LABELS:
        ts = [t for t in tasks.values() if t["level"] == lvl]
        a = agg(ts)
        rows.append([lvl, a["n"], f"{a['complete']:.0f}", f"{a['partial']:.0f}",
                     f"{a['rating']:.2f}" if a["rating"] else "-", a["rated"],
                     f"{a['rework']:.2f}", a["defects"], fmt(a["sev"]), f"{a['mins']:.0f}"])
    print(table(["Level", "Tasks", "Done %", "Part %", "Rating", "Rated", "Rework", "Defects", "Sev", "Med min"], rows))
    print("\nLevel is the judge's label when the judge finished, otherwise the level the task was started at.")

    print("\n### By D bucket (0.25 wide, judged tasks only)\n")
    buckets = collections.defaultdict(list)
    for t in tasks.values():
        if t["D"] is None:
            continue
        buckets[math.floor(t["D"] / 0.25) * 0.25].append(t)
    rows = []
    for b in sorted(buckets):
        ts = buckets[b]
        a = agg(ts)
        rows.append([f"{b:.2f}-{b + 0.25:.2f}", a["n"], f"{a['complete']:.0f}", f"{a['partial']:.0f}",
                     f"{a['rating']:.2f}" if a["rating"] else "-", a["rated"],
                     f"{a['rework']:.2f}", a["defects"], fmt(a["sev"]), f"{a['mins']:.0f}"])
    print(table(["D bucket", "Tasks", "Done %", "Part %", "Rating", "Rated", "Rework", "Defects", "Sev", "Med min"], rows))

    # ---- D. judge predictiveness --------------------------------------
    print("\n## D. Is D predictive?\n")
    judged_tasks = [t for t in tasks.values() if t["D"] is not None]
    print(f"Judged tasks: {len(judged_tasks)}. Ratings on judged tasks: {sum(1 for t in judged_tasks if t['rating'] is not None)}.\n")
    print("### Correlations with outcomes\n")
    fields = [("D", lambda t: t["D"])] + [(d[:4].capitalize(), lambda t, d=d: assessments[t["sid"]][d]) for d in DIMS]
    outcomes = [
        ("Rating", lambda t: t["rating"], [t for t in judged_tasks if t["rating"] is not None]),
        ("Rework", lambda t: t["rework"], judged_tasks),
        ("Minutes", lambda t: t["seconds"] / 60, judged_tasks),
        ("Defects", lambda t: len(t["defect_rows"]), judged_tasks),
        ("Incomplete", lambda t: 1 if t["outcome"] in ("partial", "failed") else 0, judged_tasks),
    ]
    rows = []
    for name, f in fields:
        line = [name]
        for oname, of, osub in outcomes:
            xs = [f(t) for t in osub]
            ys = [of(t) for t in osub]
            r = pearson(xs, ys)
            rho = spearman(xs, ys)
            line.append(f"{r:+.2f} / {rho:+.2f}" if r is not None and rho is not None else "-")
        rows.append(line)
    print(table(["Score", "Rating (r/rho)", "Rework (r/rho)", "Minutes (r/rho)", "Defects (r/rho)", "Incomplete (r/rho)"], rows))

    print("\n### Six sub-scores together (least squares, standardized betas)\n")
    rows = []
    for name, f, subset in outcomes:
        X = [[assessments[t["sid"]][d] for d in DIMS] for t in subset]
        y = [f(t) for t in subset]
        beta, r2 = ols(X, y)
        out = []
        for i, b in enumerate(beta):
            xs = [r[i] for r in X]
            sx = statistics.pstdev(xs)
            sy = statistics.pstdev(y)
            out.append(f"{b * sx / sy:+.2f}" if sx and sy else "-")
        rows.append([name, len(y), f"{r2:.2f}"] + out)
    print(table(["Outcome", "N", "R2", "R", "S", "C", "U", "V", "K"], rows))
    print("\nA standardized beta is the change in the outcome in standard deviations per one standard deviation of the score.")

    print("\n### Capability gap (benchmark prior minus D)\n")
    gap_tasks = [t for t in judged_tasks if t["model"] in SCORES]
    for t in gap_tasks:
        t["gap"] = score_to_d(SCORES[t["model"]]) - t["D"]
    print(f"Tasks with a benchmark score: {len(gap_tasks)}. A gap below -0.25 means the model was picked clearly below the task.\n")
    gap_tasks.sort(key=lambda t: t["gap"])
    rows = []
    qn = len(gap_tasks)
    for i in range(4):
        part = gap_tasks[i * qn // 4:(i + 1) * qn // 4]
        a = agg(part)
        rows.append([f"{part[0]['gap']:+.2f} to {part[-1]['gap']:+.2f}", a["n"], f"{a['complete']:.0f}",
                     f"{a['rating']:.2f}" if a["rating"] else "-", a["rated"], f"{a['rework']:.2f}",
                     a["defects"], f"{a['mins']:.0f}"])
    print(table(["Gap quartile", "Tasks", "Done %", "Rating", "Rated", "Rework", "Defects", "Med min"], rows))
    under = [t for t in gap_tasks if t["gap"] < -0.25]
    print(f"\nPicked clearly below the task (gap < -0.25): {len(under)} of {len(gap_tasks)} ({pct(len(under), len(gap_tasks)):.0f}%).")
    print(f"Gap vs rating r={pearson([t['gap'] for t in gap_tasks if t['rating'] is not None], [t['rating'] for t in gap_tasks if t['rating'] is not None]):+.2f}; "
          f"gap vs rework r={pearson([t['gap'] for t in gap_tasks], [t['rework'] for t in gap_tasks]):+.2f}; "
          f"gap vs minutes r={pearson([t['gap'] for t in gap_tasks], [t['seconds'] / 60 for t in gap_tasks]):+.2f}.")

    # ---- E. level cut-off ---------------------------------------------
    print("\n## E. Does the level cut-off hurt?\n")
    print("Tasks within 0.12 of each label boundary, judged only.\n")
    rows = []
    for c in CUTS:
        for side, lo, hi in (("below", c - 0.12, c), ("above", c, c + 0.12)):
            ts = [t for t in judged_tasks if lo <= t["D"] < hi]
            if not ts:
                continue
            a = agg(ts)
            rows.append([f"{c:.2f}", side, a["n"], f"{a['complete']:.0f}", f"{a['partial']:.0f}",
                         f"{a['rating']:.2f}" if a["rating"] else "-", a["rated"],
                         f"{a['rework']:.2f}", a["defects"], f"{a['mins']:.0f}"])
    print(table(["Boundary", "Side", "Tasks", "Done %", "Part %", "Rating", "Rated", "Rework", "Defects", "Med min"], rows))

    print("\n### Capability of the model used, by level\n")
    rows = []
    for lvl in LABELS:
        ts = [t for t in tasks.values() if t["level"] == lvl and t["model"] in SCORES]
        if not ts:
            continue
        scores = [SCORES[t["model"]] for t in ts]
        gaps = [score_to_d(SCORES[t["model"]]) - t["D"] for t in ts if t["D"] is not None]
        rows.append([lvl, len(ts), f"{statistics.mean(scores):.1f}", f"{min(scores):.1f}", f"{max(scores):.1f}",
                     f"{statistics.mean(gaps):+.2f}" if gaps else "-",
                     f"{pct(sum(1 for g in gaps if g < -0.25), len(gaps)):.0f}" if gaps else "-"])
    print(table(["Level", "Tasks with a score", "Mean score", "Min", "Max", "Mean gap", "Gap <-0.25 %"], rows))
    print("\nThe mean score jumps by about 9 benchmark points between normal and hard, and the pair the owner saw at the boundary differs by 12 (30 against 42.3), even though D moves by 0.05.")

    print("\n### What model the pick used near the 3.0 boundary\n")
    today = [t for t in judged_tasks if 2.75 <= t["D"] <= 3.10 and t["started"] >= 1793800000]
    rows = []
    for t in sorted(today, key=lambda t: t["started"]):
        a = assessments[t["sid"]]
        rows.append([f"{datetime.datetime.fromtimestamp(t['started']):%m-%d %H:%M}", t["model"],
                     f"{t['D']:.2f}", a["label"], t["outcome"],
                     t["rating"] if t["rating"] is not None else "-", t["fix"],
                     f"{t['seconds'] / 60:.0f}"])
    print(table(["Started", "Model", "D", "Label", "Outcome", "Rating", "Fixes", "Min"], rows))

    print("\n### Long and incomplete cases the owner reported\n")
    agy = [t for t in tasks.values() if t["harness"] == "agy"]
    longest = max(agy, key=lambda t: t["seconds"])
    print(f"- Longest agy run: {longest['seconds'] / 60:.0f} min wall clock, {len(longest['rows'])} report rows, "
          f"{longest['fix']} fixes, D {longest['D']:.2f} ({longest['level']}), rating {longest['rating']}.")
    terra = [t for t in tasks.values() if t["model"] == "gpt-6.1-terra:high"]
    parts = [t for t in terra if t["outcome"] == "partial"]
    print(f"- Terra high: {len(parts)} of {len(terra)} tasks ended partial ({pct(len(parts), len(terra)):.0f}%). "
          f"Median wall clock {statistics.median([t['seconds'] / 60 for t in parts]):.0f} min partial, "
          f"{statistics.median([t['seconds'] / 60 for t in terra if t['outcome'] == 'complete']):.0f} min complete.")

    # ---- F. reviews ----------------------------------------------------
    print("\n## F. Reviews\n")
    reviewed = {}
    for c in changes:
        ev = evidence[c["id"]]
        reviewed[c["id"]] = [e for e in ev if e["step"] == "review"]
    rows = []
    fams = collections.defaultdict(lambda: collections.Counter())
    real = withdrawn = open_ = 0
    for c in changes:
        revs = reviewed[c["id"]]
        for i, e in enumerate(revs):
            fam = e["by_model_family"]
            fams[fam][e["result"]] += 1
            if e["result"] != "fail":
                continue
            later = [x for x in revs if x["seq"] > e["seq"]]
            if not later:
                open_ += 1
                fams[fam]["open"] += 1
            elif later[0]["result"] == "pass" and later[0]["commit_hash"] != e["commit_hash"]:
                real += 1
                fams[fam]["real"] += 1
            elif later[0]["result"] == "pass":
                withdrawn += 1
                fams[fam]["withdrawn"] += 1
    for f in sorted(fams):
        c = fams[f]
        rows.append([f, c["pass"], c["fail"], c["real"], c["withdrawn"], c["open"], c["pass"] + c["fail"]])
    print(table(["Reviewer family", "Pass", "Fail", "Real fix", "Withdrawn", "Unresolved", "Reviews"], rows))
    print(f"\nFails that a later review passed at a new commit (real finding): {real}.")
    print(f"Fails withdrawn at the same commit (false alarm): {withdrawn}.")
    print(f"Fails with no later review: {open_}.")

    print("\n### Changes by level, review and state\n")
    rows = []
    for lvl in LABELS + [""]:
        ts = [c for c in changes if c["difficulty"] == lvl]
        if not ts:
            continue
        r = [c for c in ts if reviewed[c["id"]]]
        rows.append([lvl or "(unset)", len(ts), len(r), sum(1 for c in ts if c["state"] == "merged"),
                     sum(1 for c in ts if c["state"] == "open"), sum(1 for c in ts if c["state"] in ("done", "stopped"))])
    print(table(["Level", "Changes", "Reviewed", "Merged", "Open", "Done/stopped"], rows))

    normal = [c for c in changes if c["difficulty"] == "normal"]
    normal_merged = [c for c in normal if c["state"] == "merged"]
    print(f"\nNormal changes: {len(normal)}; merged: {len(normal_merged)}; merged without a review: "
          f"{sum(1 for c in normal_merged if not reviewed[c['id']])}.")
    linked = collections.defaultdict(set)
    for c in changes:
        linked[c["origin"].get("session_id")].add(c["id"])
    defect_changes = [d for d in q("SELECT * FROM worker_defects") if linked[d["session_id"]]]
    dr = [d for d in defect_changes if any(reviewed[c] for c in linked[d["session_id"]])]
    du = [d for d in defect_changes if not any(reviewed[c] for c in linked[d["session_id"]])]
    print(f"Late defects joined to a delivery change: {len(defect_changes)}; on reviewed changes: {len(dr)} "
          f"(severities {[d['severity'] for d in dr]}); on unreviewed changes: {len(du)} "
          f"(severities {[d['severity'] for d in du]}).")

    print("\n### Reviews with findings, by level\n")
    rows = []
    for c in changes:
        revs = reviewed[c["id"]]
        if not revs:
            continue
        findings = [int(e["fields"].get("findings_open", 0) or 0) for e in revs]
        fails = sum(1 for e in revs if e["result"] == "fail")
        if fails:
            rows.append([f"{datetime.datetime.fromtimestamp(c['created_at']):%m-%d %H:%M}", c["difficulty"] or "(unset)",
                         len(revs), fails, sum(findings), c["state"],
                         ",".join(sorted({e["by_model_family"] for e in revs}))])
    print(table(["Change started", "Level", "Reviews", "Fails", "Findings open", "State", "Reviewer families"], rows))

    # ---- G. picker -----------------------------------------------------
    print("\n## G. Picker\n")
    print("### Tasks per model\n")
    by_model = collections.Counter(t["model"] for t in tasks.values())
    rows = [[m, n, f"{pct(n, len(tasks)):.0f}"] for m, n in by_model.most_common(12)]
    print(table(["Model", "Tasks", "Share %"], rows))

    print("\n### Picks recorded in the picker note, by level\n")
    import re
    note = re.compile(r"-> (\S+) (\S+) \(([\d.]+),")
    auto = collections.Counter()
    pace = []
    rotation = []
    pace_re = re.compile(r"(\w+) (-?\d+)% (ahead of|behind) pace")
    rot_re = re.compile(r"rotation weight ([\d.]+)")
    for a in assessments.values():
        text = a["picker_note"]
        if not text:
            continue
        m = note.search(text)
        if not m:
            continue
        auto[(a["label"], m.group(2))] += 1
        p = pace_re.search(text)
        if p:
            pace.append((int(p.group(2)) * (1 if p.group(3) == "ahead of" else -1), m.group(2), a["session_id"]))
        r = rot_re.search(text)
        if r:
            rotation.append(float(r.group(1)))
    rows = []
    for (lvl, model), n in sorted(auto.items()):
        rows.append([lvl, model, n])
    print(table(["Level", "Model", "Automatic picks"], rows))

    print("\n### Quota pace at the pick\n")
    print(f"Notes with a pace figure: {len(pace)}. Mean signed pace: {statistics.mean(v for v, _, _ in pace):+.1f} points.")
    print(f"Rotation weight: n={len(rotation)}, min={min(rotation):.2f}, median={statistics.median(rotation):.2f}, max={max(rotation):.2f}.")
    ahead = [v for v, _, _ in pace if v > 0]
    behind = [v for v, _, _ in pace if v < 0]
    print(f"Ahead of pace: {len(ahead)} picks; behind: {len(behind)}; exactly on pace: {len(pace) - len(ahead) - len(behind)}.")
    print("\nPace below is the provider's, from the note: positive is ahead of the weekly pace.\n")
    rows = []
    for sign, label in ((1, "ahead"), (-1, "behind")):
        ids = [sid for v, _, sid in pace if (v > 0 if sign == 1 else v < 0)]
        ts = [tasks[sid] for sid in ids if sid in tasks]
        if ts:
            a = agg(ts)
            rows.append([label, a["n"], f"{a['complete']:.0f}", f"{a['rating']:.2f}" if a["rating"] else "-",
                         a["rated"], f"{a['rework']:.2f}", f"{a['mins']:.0f}"])
    print(table(["Provider pace", "Tasks", "Done %", "Rating", "Rated", "Rework", "Med min"], rows))

    print("\n### Was a better pick available in hindsight?\n")
    label_means = collections.defaultdict(dict)
    for sid, r in ratings.items():
        label_means[r["difficulty"]].setdefault(r["model"], []).append(r["score"])
    label_means = {l: {m: statistics.mean(v) for m, v in d.items() if len(v) >= 4} for l, d in label_means.items()}
    total = better = 0
    gaps = []
    misses = collections.Counter()
    for t in tasks.values():
        if t["rating"] is None:
            continue
        lm = label_means.get(ratings[t["sid"]]["difficulty"], {})
        if t["model"] not in lm:
            continue
        total += 1
        mine = lm[t["model"]]
        best_model, best = max(lm.items(), key=lambda kv: kv[1])
        if best_model != t["model"] and best - mine >= 0.5:
            better += 1
            gaps.append(best - mine)
            misses[(t["model"], best_model)] += 1
    print(f"Rated tasks where the model had a same-level mean: {total}.")
    print(f"A different model had a mean at least 0.5 higher: {better} ({pct(better, total):.0f}%), mean gap {statistics.mean(gaps):.2f}.")
    if misses:
        print("Most common misses (picked -> better): " + ", ".join(f"{a} -> {b} ({n})" for (a, b), n in misses.most_common(5)) + ".")
    protected = {"claude", "opus", "sonnet"}
    total_u = better_u = 0
    gaps_u = []
    misses_u = collections.Counter()
    for t in tasks.values():
        if t["rating"] is None or family(t["model"]) in protected:
            continue
        lm = {m: v for m, v in label_means.get(ratings[t["sid"]]["difficulty"], {}).items() if family(m) not in protected}
        if t["model"] not in lm:
            continue
        total_u += 1
        mine = lm[t["model"]]
        best_model, best = max(lm.items(), key=lambda kv: kv[1])
        if best_model != t["model"] and best - mine >= 0.5:
            better_u += 1
            gaps_u.append(best - mine)
            misses_u[(t["model"], best_model)] += 1
    print(f"Unprotected alternatives only: {better_u} of {total_u} rated tasks had a choice at least 0.5 higher"
          + (f", mean gap {statistics.mean(gaps_u):.2f}." if gaps_u else "."))
    if misses_u:
        print("Unprotected misses (picked -> better, task count): " + ", ".join(f"{a} -> {b} ({n})" for (a, b), n in misses_u.most_common(5)) + ".")

    print("\n### Tasks per day\n")
    per_day = collections.defaultdict(collections.Counter)
    for t in tasks.values():
        per_day[datetime.datetime.fromtimestamp(t["started"]).strftime("%m-%d")][t["model"]] += 1
    for day in sorted(per_day):
        top = ", ".join(f"{m} {n}" for m, n in per_day[day].most_common(6))
        print(f"- {day}: {top}")

    # ---- H. plan check -------------------------------------------------
    print("\n## H. The stored plan on today's data\n")
    print("Rebuilds capability C exactly as `scripts/dev/capability-backtest.py` does: my time per task, "
          "residual against the level average, `extra = sum / (n + 5)`, `adjust = clamp(-extra / 16, -0.75, +0.75)`.\n")
    pool = [t for t in tasks.values() if t["level"] in LABELS and t["outcome"] != "stopped"]
    level_avg = collections.defaultdict(list)
    for t in pool:
        level_avg[t["level"]].append(t["my_minutes"])
    avg = {k: statistics.mean(v) for k, v in level_avg.items()}
    resid = collections.defaultdict(list)
    for t in pool:
        resid[(family(t["model"]), effort(t["model"]))].append(t["my_minutes"] - avg[t["level"]])
    rows = []
    for model in PLAN_C:
        key = (family(model), effort(model))
        data = resid.get(key) or []
        extra = sum(data) / (len(data) + 5) if data else 0
        prior = score_to_d(SCORES[model])
        c = prior + max(-0.75, min(0.75, -extra / 16))
        rows.append([model, f"{SCORES[model]:.1f}", f"{prior:.2f}", len(data), f"{extra:+.1f}",
                     f"{c - prior:+.2f}", f"{c:.2f}", f"{PLAN_C[model]:.2f}", f"{c - PLAN_C[model]:+.2f}"])
    print(table(["Model", "Score", "Prior C", "Tasks", "Extra min", "Adjust", "C today", "C in plan", "Drift"], rows))

    print("\n### Split-half reliability (same model and effort, first half against second half by end time)\n")
    rows = []
    for kind, values in (("Rework residual (min)", None), ("Rating", "rating")):
        groups = collections.defaultdict(list)
        for t in pool:
            if kind == "Rating" and t["rating"] is None:
                continue
            groups[(family(t["model"]), effort(t["model"]))].append(t)
        pairs = []
        detail = []
        for key, ts in groups.items():
            if len(ts) < 10:
                continue
            ts = sorted(ts, key=lambda t: t["ended"])
            h = len(ts) // 2
            def mean(part):
                if kind == "Rating":
                    return statistics.mean(t["rating"] for t in part)
                return statistics.mean(t["my_minutes"] - avg[t["level"]] for t in part)
            a, b = mean(ts[:h]), mean(ts[h:])
            pairs.append((a, b))
            detail.append(f"{key[0]} {key[1]} n={len(ts)} {a:+.1f} then {b:+.1f}")
        r = pearson([p[0] for p in pairs], [p[1] for p in pairs]) if len(pairs) > 2 else None
        rows.append([kind, len(pairs), fmt(r, 2)])
        print("\n" + kind + ":")
        for line in detail:
            print(f"- {line}")
    print()
    print(table(["Signal", "Groups", "Split-half r"], rows))
    print("\nA split-half r near 0 means the difference between models in that signal is mostly noise; near 1 means it is a stable trait.")

    # ---- I. missing data ----------------------------------------------
    print("\n## I. What the data does not hold\n")
    print("- Tokens and cost per task: no column in any worker table.")
    print("- Active model minutes: only wall clock, which includes queue and quota waits.")
    print("- Review time: the plan's measured review minutes have no table yet.")
    print("- Whether a start was automatic: only 80 of 246 assessments carry a picker note.")
    print("- Per-task link between a worker run and a delivery change: only recent changes carry the worker session.")
    print("- Provider quota snapshots over time: only the pace figure inside a picker note.")
    print("- A second rater for the same task: one rating per session, from the caller.")


if __name__ == "__main__":
    if len(sys.argv) != 2:
        sys.exit("usage: worker-data-review.py <copy of gateway.db>")
    main(sys.argv[1])
