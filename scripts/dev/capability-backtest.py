"""Capability backtest: rework moves a model's capability C on the judge's D scale.
Reads a copy of the gateway database. Usage: python3 scripts/dev/capability-backtest.py <copy of gateway.db>. Never point it at the live database."""
import math, sqlite3, sys, time
from collections import defaultdict

FIX, CONT = 10, 2                      # minutes per fix, per nudge
DEFECT = {1: 20, 2: 45, 3: 90, 4: 180}  # minutes per late bug, by severity
LABEL_D = {"trivial": 1.1, "simple": 1.9, "normal": 2.6, "hard": 3.4, "very_complex": 4.1}  # old tasks: middle of each range
CUTS = [(20, 1.5), (30, 2.25), (40, 3.0), (50, 3.75)]  # benchmark score -> D
PER_MIN, CAP, PRIOR_TASKS, OWN_KEY_MIN = 0.25 / 4, 0.75, 5, 20
WIDTH = 0.5  # a model more than this far from D (either side) gets no weight
CANDIDATES = {"gpt-6-luna:xhigh": 23.3, "opencode-go/deepseek-v4.1-flash": 32.5,
              "gpt-6.1-terra:high": 42.3, "gpt-6.1-sol:medium": 46.1, "gpt-6.1-terra:max": 51.0}

def family(model):
    for f in ("deepseek", "terra", "sol", "luna", "opus"):
        if f in model:
            return f
    return model

def effort(model):
    return model.rsplit(":", 1)[1] if ":" in model else "default"

def score_to_d(s):
    (s0, d0), (s1, d1) = (CUTS[0], CUTS[1]) if s < CUTS[1][0] else (CUTS[-2], CUTS[-1]) if s >= CUTS[-2][0] else next((a, b) for a, b in zip(CUTS, CUTS[1:]) if a[0] <= s < b[0])
    return d0 + (s - s0) * (d1 - d0) / (s1 - s0)

db = sqlite3.connect(sys.argv[1])
cut = time.time() - 30 * 86400
tasks = {}
for sid, root, model, harness, label, outcome, ended, cont, fix in db.execute(
        "SELECT session_id, task_id, model, harness, difficulty, outcome, ended_at, continue_count, fix_count FROM worker_runs ORDER BY ended_at, id"):
    if not sid:
        continue
    t = tasks.setdefault(root or sid, {"model": model, "harness": harness, "label": label, "min": 0.0, "sessions": set(), "live": False, "recent": False})
    if sid == (root or sid):
        t.update(model=model, harness=harness, label=label)
    t["sessions"].add(sid)
    # Codex has no hand-back tool, so its nudges are agent-net's idle bug, not the model.
    t["min"] += FIX * fix + CONT * cont  # automatic nudges are counted apart and left out
    t["live"] |= outcome != "stopped"
    t["recent"] |= ended >= cut
defects = defaultdict(float)
for sid, sev in db.execute("SELECT session_id, severity FROM worker_defects WHERE at >= ?", (cut,)):
    defects[sid] += DEFECT.get(sev, 0)
rows = []
for t in tasks.values():
    if t["live"] and t["recent"] and t["label"] in LABEL_D:
        rows.append((t["model"], t["label"], t["min"] + sum(defects[s] for s in t["sessions"])))

# Residual = my time minus the average at that level, so harder levels are not held against a model.
by_level = defaultdict(list)
for _, label, m in rows:
    by_level[label].append(m)
avg = {l: sum(v) / len(v) for l, v in by_level.items()}
resid = defaultdict(list)
for model, label, m in rows:
    resid[("fam", family(model))].append(m - avg[label])
    resid[("eff", family(model), effort(model))].append(m - avg[label])

print("Level averages (min/task):", {l: f"{avg[l]:.1f} ({len(by_level[l])})" for l in LABEL_D if l in avg})
print()
print(f"{'model':34} {'score':>5} {'prior C':>7} {'data':>16} {'tasks':>5} {'extra min':>9} {'adj':>6} {'C':>5}")
cap = {}
for model, s in CANDIDATES.items():
    key = ("eff", family(model), effort(model))
    if len(resid[key]) < OWN_KEY_MIN:
        key = ("fam", family(model))
    r = resid[key]
    extra = sum(r) / (len(r) + PRIOR_TASKS)  # pulled toward 0 while there are few tasks
    adj = max(-CAP, min(CAP, -PER_MIN * extra))
    prior = score_to_d(s)
    cap[model] = prior + adj
    print(f"{model:34} {s:5.1f} {prior:7.2f} {' x '.join(key[1:]):>16} {len(r):5d} {extra:9.1f} {adj:+6.2f} {cap[model]:5.2f}")

def label_of(d):
    for name, top in (("trivial", 1.5), ("simple", 2.25), ("normal", 3.0), ("hard", 3.75)):
        if d < top:
            return name
    return "very_complex"

LEVELS = ["trivial", "simple", "normal", "hard", "very_complex"]
MINIMUM = {"trivial": 0, "simple": 20, "normal": 30, "hard": 40, "very_complex": 50}

def today(d):
    level = label_of(d)
    ok = {}
    for model, s in CANDIDATES.items():
        top = max(i for i, l in enumerate(LEVELS) if s >= MINIMUM[l])
        if LEVELS.index(level) in (top, top - 1):
            ok[model] = s
    if not ok:
        return {}
    weak = min(ok.values())
    w = {m: math.exp(-(s - weak) / 8) for m, s in ok.items()}
    return {m: v / sum(w.values()) for m, v in w.items()}

def new(d):
    # Qualifies from half a level below D (as today: a score at a level's floor
    # takes the whole level) up to two levels above. The closest model weighs most.
    # The weight falls in a straight line from 1 at D to 0 at WIDTH away,
    # so the odds change smoothly as D moves.
    w = {m: 1 - abs(c - d) / WIDTH for m, c in cap.items() if abs(c - d) < WIDTH}
    if not w:  # nothing in the window: the closest model, alone
        m = min(cap, key=lambda m: abs(cap[m] - d))
        return {m + " (closest)": 1.0}
    return {m: v / sum(w.values()) for m, v in w.items()}

def short(m):
    return m.split("/")[-1].replace("gpt-6.1-", "").replace("gpt-6-", "").replace("-v4.1-flash", "")

print()
for d in (1.5, 2.0, 2.3, 2.5, 2.6, 2.7, 2.8, 2.9, 3.0, 3.2, 3.4, 3.6, 3.8, 4.0, 4.3):
    fmt = lambda odds: ", ".join(f"{short(m)} {100 * p:.0f}%" for m, p in sorted(odds.items(), key=lambda x: -x[1])) or "none qualifies"
    print(f"D {d} ({label_of(d)})\n  today: {fmt(today(d))}\n  new:   {fmt(new(d))}")
