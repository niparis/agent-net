"""The whole study, from the snapshot to results/tables.md.

    uv run python -m picker_ml.study /path/to/gateway-snapshot.db

The snapshot is opened read only. Output goes to results/ next to this
package (or --out). Nothing is written to the snapshot or the live store.
"""

from __future__ import annotations

import argparse
import datetime as dt
import json
import math
from pathlib import Path

import numpy as np
import pandas as pd

from . import labels as L
from .data import PriceBook, load, model_key
from .features import FEATURE_TIMES, Benchmarks, candidate_rows, task_frame
from .metrics import (auc, brier, calib_error, calibration, logloss, mae, paired_boot, rmse)
from .models import DEFAULT_GROUPS, GBM, CostTable, Pooled, Table, TwoPart, bucket

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[2]
DAY = 86400
TEST_FRAC = 0.22
VAL_FRAC = 0.20
GRID_LAM = [0.5, 2.0, 8.0, 32.0]
GRID_TAU = [None, 14.0, 7.0]
FAILURE_COSTS = [0.5, 1.0, 2.0, 5.0, 10.0]
CASE_SESSION = "s_c1ce54688a069800"

SCORERS = {"today": L.score_today, "a": L.score_a}


def iso(t):
    return dt.datetime.fromtimestamp(t, dt.UTC).strftime("%Y-%m-%d %H:%M")


class Out:
    def __init__(self):
        self.lines: list[str] = []
        self.data: dict = {}

    def h(self, text, level=2):
        self.lines += ["", "#" * level + " " + text, ""]

    def p(self, text=""):
        self.lines.append(text)

    def table(self, df: pd.DataFrame, floatfmt="{:.4f}"):
        cols = list(df.columns)
        self.lines.append("| " + " | ".join(map(str, cols)) + " |")
        self.lines.append("|" + "---|" * len(cols))
        # to_dict keeps each column's own type, so counts print as integers.
        for r in df.to_dict("records"):
            cells = []
            for c in cols:
                v = r[c]
                if isinstance(v, (float, np.floating)):
                    cells.append("" if math.isnan(v) else floatfmt.format(v))
                else:
                    cells.append(str(v))
            self.lines.append("| " + " | ".join(cells) + " |")


def labels_for(tasks_by_root, roots, asof):
    ts = [tasks_by_root[r] for r in roots]
    return {
        "today": np.array([L.score_today(t, asof) for t in ts]),
        "a": np.array([L.score_a(t, asof) for t in ts]),
        "clean": np.array([L.clean(t, asof) for t in ts], float),
    }


def split(df, cut, asof_end=None):
    """Tasks that started and ended before the cut: what a model at the cut saw."""
    return df[(df.start < cut) & (df.end <= (asof_end or cut))]


def quasi_dev(y, p):
    p = np.clip(p, 1e-6, 1 - 1e-6)
    return float(-np.mean(y * np.log(p) + (1 - y) * np.log(1 - p)))


def tune(make, tr, ytr, va, yva, now, loss):
    best = None
    rows = []
    for lam in GRID_LAM:
        for tau in GRID_TAU:
            m = make(lam, tau).fit(tr, ytr, now)
            v = loss(yva, m.predict(va))
            rows.append((lam, tau, v))
            if best is None or v < best[2]:
                best = (lam, tau, v)
    return best, rows


NO_MODEL = [g for g in DEFAULT_GROUPS if g not in ("model_hier", "model_x_d", "bench_effort")]
BENCH_ONLY = [g for g in DEFAULT_GROUPS if g not in ("model_hier", "model_x_d")]


def oracle(base_pred, y, keys, mask, k=5.0):
    """A bound on what knowing each model's current form could add: the D bucket
    prediction plus the key's mean residual over the OTHER scored test tasks
    (leave one out), shrunk with the table's k. It reads answers from the test
    period itself, so no real method can use it; it shows how much model
    identity is worth when it is measured at the same time."""
    out = base_pred.copy()
    r = y - base_pred
    idx = np.flatnonzero(mask)
    sums = pd.Series(r[idx]).groupby(keys[idx]).sum()
    cnts = pd.Series(r[idx]).groupby(keys[idx]).size()
    for i in range(len(keys)):
        sm, n = sums.get(keys[i], 0.0), cnts.get(keys[i], 0)
        if mask[i]:
            sm, n = sm - r[i], n - 1
        out[i] = base_pred[i] + sm / (n + k)
    return out


def stability(df, y, by_root_, S):
    """Does a model's quality in one period predict it in the next? Cells are
    key x judge label. Each cell's mean residual (score minus the period's mean
    for that label) in the first half is compared with the second half."""
    half = df.start.median()
    rows = []
    for (key, label), g in df.assign(y=y).groupby(["key", "label"]):
        a, b = g[g.start < half], g[g.start >= half]
        if len(a) >= 8 and len(b) >= 8:
            rows.append((key, label, len(a), len(b), a.y.mean(), b.y.mean()))
    t = pd.DataFrame(rows, columns=["key", "label", "n1", "n2", "m1", "m2"])
    lm1 = df.assign(y=y)[df.start < half].groupby("label").y.mean()
    lm2 = df.assign(y=y)[df.start >= half].groupby("label").y.mean()
    t["r1"] = t.m1 - t.label.map(lm1)
    t["r2"] = t.m2 - t.label.map(lm2)
    return t, half


def price_fallback(book: PriceBook, keys, ds, train_tasks):
    """Go's ExpectedCost fallback: the key's price times the bucket's average tokens."""
    tok = {}
    allt, alln = {}, 0
    for t in train_tasks:
        if not t.tokens:
            continue
        b = int(bucket([t.d])[0])
        s, n = tok.get(b, ({}, 0))
        for k, v in t.tokens.items():
            s[k] = s.get(k, 0) + v
            allt[k] = allt.get(k, 0) + v
        tok[b] = (s, n + 1)
        alln += 1
    out = []
    for k, b in zip(keys, bucket(ds)):
        s, n = tok.get(b, (allt, alln))
        avg = {kk: vv / n for kk, vv in s.items()}
        p, _ = book.price_of(k)
        out.append(PriceBook.cost(p, avg))
    return np.array(out)


def main(argv=None):
    ap = argparse.ArgumentParser()
    ap.add_argument("snapshot")
    ap.add_argument("--out", default=str(HERE.parent / "results"))
    args = ap.parse_args(argv)
    out_dir = Path(args.out)
    out_dir.mkdir(parents=True, exist_ok=True)
    O = Out()

    all_tasks, meta = load(args.snapshot)
    S = meta["snapshot_time"]
    tasks = [t for t in all_tasks if t.d > 0 and t.key]
    by_root = {t.root: t for t in tasks}
    bench = Benchmarks(REPO / "internal/protocol/scores.json")
    df = task_frame(tasks, bench).sort_values("start", kind="stable").reset_index(drop=True)

    O.p("# picker-ml results (generated)")
    O.p()
    O.p(f"Generated by `picker_ml.study`. Snapshot last event {iso(S)} UTC. Do not edit by hand; rerun the study.")

    # ---- data and label lag ------------------------------------------------
    O.h("Data")
    O.p(f"- chains (run plus continuations, stopped and infra runs left out): {len(all_tasks)}")
    O.p(f"- chains with a judge D and a model key (the picker's task set): {len(tasks)}")
    O.p(f"- with a recorded cost: {int(df.cost.notna().sum())}")
    O.p(f"- with a project from worker_starts: {int((df.project != 'unknown').sum())}; with done checks: {int(df.has_done.sum())}")
    O.p(f"- tasks that continue an earlier worker's task (task_id set at start): {sum(t.task_id_set for t in tasks)}")
    O.p(f"- tasks with touches recorded: {sum(1 for t in tasks if t.touches)}")
    O.p(f"- model keys: {df.key.nunique()}; first start {iso(df.start.min())}, last start {iso(df.start.max())}")

    ylast = labels_for(by_root, df.root, S)
    dist = pd.DataFrame({"today": pd.Series(np.round(ylast["today"], 2)).value_counts().sort_index(),
                         "option A": pd.Series(np.round(ylast["a"], 2)).value_counts().sort_index()}).fillna(0).astype(int)
    dist.index.name = "score"
    O.h("Score shape (all tasks, labels as of the snapshot)", 3)
    O.table(dist.reset_index(), "{:.2f}")
    O.p()
    O.p(f"Clean share {ylast['clean'].mean():.3f}; complete share {np.mean([t.complete for t in tasks]):.3f}.")

    O.h("Label measurement differs by project", 3)
    O.p("A task can only fail an accept, review or test step if its session opened a delivery change. "
        "Projects that do not use delivery steps can only lose clean status through a defect or an "
        "incomplete run, so the same model looks cleaner there.")
    meas = pd.DataFrame({"group": np.where(df.project == "agent-net", "agent-net",
                                           np.where(df.project == "unknown", "project unknown (before 11-04)", "other projects")),
                         "change": [by_root[r].has_change for r in df.root], "clean": ylast["clean"], "a": ylast["a"]})
    O.table(meas.groupby("group").agg(tasks=("clean", "size"), opened_change=("change", "mean"),
                                      clean=("clean", "mean"), option_A=("a", "mean")).reset_index(), "{:.3f}")

    # The loader must rebuild the live table: compare with the cells the live
    # picker recorded at each of its picks.
    db = meta["db"]
    picks = db.execute("""SELECT DISTINCT p.run_id, a.assessed_at FROM pick_candidates p
                          JOIN worker_assessments a ON a.run_id = p.run_id""").fetchall()
    match = total = nmatch = 0
    for pk in picks:
        at = pk["assessed_at"]
        tr_ = df[df.end <= at]
        y_ = labels_for(by_root, tr_.root, at)["today"]
        tb = Table(tr_.key, tr_.d, y_)
        for c in db.execute("SELECT harness, model, bucket_lo, bucket_hi, cell_score, cell_n FROM pick_candidates "
                            "WHERE run_id=? AND cell_n IS NOT NULL", (pk["run_id"],)):
            key = model_key(c["harness"], c["model"])
            mid = [(c["bucket_lo"] + c["bucket_hi"]) / 2]
            total += 1
            match += abs(tb.predict([key], mid)[0] - c["cell_score"]) <= 0.01
            nmatch += int(tb.n([key], mid)[0]) == c["cell_n"]
    O.h("Loader check against the live picker", 3)
    O.p(f"Today's table rebuilt at each of the {len(picks)} recorded picks (tasks ended by the judge time, labels "
        f"as of then): cell score within 0.01 of the recorded cell for {match} of {total} candidate rows; same "
        f"task count for {nmatch} of {total}.")
    O.data["loader_check"] = {"picks": len(picks), "rows": total, "score_match": int(match), "n_match": int(nmatch)}

    lags = L.lag_days(tasks)
    pos = np.concatenate([np.array(v) for v in lags.values()])
    late = pos[pos > 0]
    w_days = math.ceil(np.percentile(late, 95) * 2) / 2
    O.h("Label lag")
    rows = []
    for k, v in lags.items():
        v = np.array(v)
        rows.append({"event": k, "n": len(v), "before task end": int((v <= 0).sum()),
                     "p50 days": np.percentile(v, 50), "p90": np.percentile(v, 90), "p95": np.percentile(v, 95),
                     "p99": np.percentile(v, 99), "max": v.max()})
    O.table(pd.DataFrame(rows), "{:.2f}")
    O.p()
    O.p(f"Rule: a test task is mature when it ended at least W = {w_days} days before the snapshot. W is the 95th "
        f"percentile of the delay of every event that came after its task ended ({len(late)} events), rounded up to "
        "half a day. Training labels are cut at the split time: a training task counts only the failed steps, "
        "defects and merges recorded by then, and a task still running at the split is left out of training.")

    # ---- split -------------------------------------------------------------
    n_test = int(round(TEST_FRAC * len(df)))
    test = df.iloc[-n_test:]
    T = int(test.start.min())
    train = split(df, T)
    running_at_cut = int(((df.start < T) & (df.end > T)).sum())
    ytr = labels_for(by_root, train.root, T)
    yte = labels_for(by_root, test.root, S)
    mature = (test.end <= S - w_days * DAY).to_numpy()
    O.h("Time split")
    O.p(f"- train: {len(train)} tasks that started before {iso(T)} and ended by then; {running_at_cut} tasks "
        "running at the cut are left out")
    O.p(f"- test: the last {n_test} tasks by start ({TEST_FRAC:.0%}), {iso(T)} to {iso(test.start.max())}")
    O.p(f"- test tasks mature under the lag rule: {int(mature.sum())}; flagged immature and reported apart: "
        f"{int((~mature).sum())}")
    changed = {k: int((labels_for(by_root, train.root, S)[k] != ytr[k]).sum()) for k in ytr}
    O.p(f"- training labels that changed between the cut and the snapshot (what the cut hides): {changed}")
    O.data["split"] = {"train": len(train), "test": n_test, "mature": int(mature.sum()), "W_days": w_days,
                       "cut": iso(T)}

    # Inner split for tuning: the last VAL_FRAC of train by start.
    n_val = int(round(VAL_FRAC * len(train)))
    val = train.iloc[-n_val:]
    T2 = int(val.start.min())
    itr = split(train, T2)
    yitr = labels_for(by_root, itr.root, T2)
    yval = labels_for(by_root, val.root, T)
    O.p(f"- tuning: inner train {len(itr)} tasks before {iso(T2)}, validation {len(val)} tasks (labels as of the cut)")

    # ---- tune ------------------------------------------------------------------
    O.h("Tuning (validation inside train)")
    params = {}
    trows = []
    for tgt, link in (("today", "logit"), ("a", "logit"), ("clean", "logit")):
        loss = logloss if tgt == "clean" else quasi_dev
        best, grid = tune(lambda lam, tau: Pooled(link, lam_re=lam, tau_days=tau), itr, yitr[tgt], val, yval[tgt], T2, loss)
        params[tgt] = best
        trows.append({"target": tgt, "lambda": best[0], "tau days": str(best[1]), "val loss": best[2]})
    itr_c = itr[itr.cost > 0]
    val_c = val[val.cost > 0]
    best, _ = tune(lambda lam, tau: Pooled("gaussian", lam_re=lam, tau_days=tau), itr_c, np.log(itr_c.cost),
                   val_c, np.log(val_c.cost), T2, lambda y, p: float(np.mean((y - p) ** 2)))
    params["cost"] = best
    trows.append({"target": "log cost", "lambda": best[0], "tau days": str(best[1]), "val loss": best[2]})
    O.table(pd.DataFrame(trows))
    O.p()
    O.p(f"Grid: lambda (precision of each group effect; slopes x4) in {GRID_LAM}; recency half-life-like tau in "
        f"{GRID_TAU} days (None = equal weights). Fixed effects have ridge 1 on standardized columns.")

    def pooled(tgt, groups=None, tau="tuned"):
        lam, t, _ = params[tgt]
        link = "gaussian" if tgt == "cost" else "logit"
        return Pooled(link, groups=groups, lam_re=lam, tau_days=t if tau == "tuned" else tau)

    # ---- quality ---------------------------------------------------------------
    preds = {}
    O.h("Quality (primary)")
    O.p("Held-out on the mature test tasks. Bootstrap columns: method minus table, 95% interval over 2000 "
        "resamples of the test tasks. Negative is better than the table. RMSE is the proper score for a mean "
        "forecast; MAE favours the median and so rewards predicting 1 for everything.")
    qrows, calib_tables = [], {}
    for tgt in ("today", "a"):
        y, ym = yte[tgt], yte[tgt][mature]
        tab = Table(train.key, train.d, ytr[tgt]).predict(test.key, test.d)
        pm = pooled(tgt).fit(train, ytr[tgt], T)
        pool = pm.predict(test)
        two = TwoPart(lam_re=params[tgt][0], tau_days=params[tgt][1]).fit(train, ytr[tgt], T).predict(test)
        gbm = GBM("reg").fit(train, ytr[tgt]).predict(test)
        mean = np.full(len(test), ytr[tgt].mean())
        dtab = Table(np.full(len(train), "all"), train.d, ytr[tgt]).predict(np.full(len(test), "all"), test.d)
        nomodel = pooled(tgt, NO_MODEL).fit(train, ytr[tgt], T).predict(test)
        benchonly = pooled(tgt, BENCH_ONLY).fit(train, ytr[tgt], T).predict(test)
        orc = oracle(dtab, yte[tgt], test.key.to_numpy(), mature)
        preds[tgt] = {"table": tab, "pooled": pool, "two-part": two, "gbm": gbm, "model": pm, "nomodel": nomodel,
                      "dtab": dtab}
        for name, p in (("train mean", mean), ("D bucket only", dtab), ("table (today)", tab), ("pooled fractional logit", pool),
                        ("pooled two-part", two), ("pooled, no model information", nomodel),
                        ("pooled, benchmark and effort only", benchonly), ("gradient boosting", gbm),
                        ("bound: D bucket + same-period model form (leave one out, cheats)", orc)):
            pmat = p[mature]
            r = {"score": "today" if tgt == "today" else "option A", "method": name,
                 "MAE": mae(ym, pmat), "RMSE": rmse(ym, pmat), "calib err": calib_error(ym, pmat),
                 "RMSE all test": rmse(y, p)}
            if name != "table (today)":
                d, lo, hi = paired_boot(ym, pmat, tab[mature], rmse)
                r["dRMSE vs table [95%]"] = f"{d:+.4f} [{lo:+.4f}, {hi:+.4f}]"
                d, lo, hi = paired_boot(ym, pmat, tab[mature], mae)
                r["dMAE vs table [95%]"] = f"{d:+.4f} [{lo:+.4f}, {hi:+.4f}]"
            else:
                r["dRMSE vs table [95%]"] = r["dMAE vs table [95%]"] = "-"
            qrows.append(r)
        calib_tables[tgt] = (calibration(ym, tab[mature]), calibration(ym, pool[mature]))
    O.table(pd.DataFrame(qrows))
    O.data["quality"] = qrows
    for tgt, (ct, cp) in calib_tables.items():
        O.h(f"Calibration by decile, {'today' if tgt == 'today' else 'option A'} score (mature test)", 3)
        m = ct.rename(columns={"predicted": "table pred", "observed": "table obs"}).merge(
            cp.rename(columns={"predicted": "pooled pred", "observed": "pooled obs", "n": "n2"}), on="decile")
        O.table(m.drop(columns=["n2"]), "{:.3f}")

    # ---- P(clean) ---------------------------------------------------------------
    O.h("P(clean) (secondary)")
    y, ym = yte["clean"], yte["clean"][mature]
    tab = Table(train.key, train.d, ytr["clean"]).predict(test.key, test.d)
    pmc = pooled("clean").fit(train, ytr["clean"], T)
    pool = pmc.predict(test)
    gbm = GBM("binary").fit(train, ytr["clean"]).predict(test)
    mean = np.full(len(test), ytr["clean"].mean())
    preds["clean"] = {"table": tab, "pooled": pool, "gbm": gbm, "model": pmc}
    nomodel = pooled("clean", NO_MODEL).fit(train, ytr["clean"], T).predict(test)
    benchonly = pooled("clean", BENCH_ONLY).fit(train, ytr["clean"], T).predict(test)
    dtab = Table(np.full(len(train), "all"), train.d, ytr["clean"]).predict(np.full(len(test), "all"), test.d)
    orc = np.clip(oracle(dtab, yte["clean"], test.key.to_numpy(), mature), 0.01, 0.99)
    crows = []
    for name, p in (("train mean", mean), ("D bucket only", dtab), ("table (today)", tab), ("pooled logistic", pool),
                    ("pooled, no model information", nomodel), ("pooled, benchmark and effort only", benchonly),
                    ("gradient boosting", gbm), ("bound: D bucket + same-period model form (leave one out, cheats)", orc)):
        pmat = p[mature]
        r = {"method": name, "log loss": logloss(ym, pmat), "Brier": brier(ym, pmat), "AUC": auc(ym, pmat),
             "calib err": calib_error(ym, pmat), "log loss all test": logloss(y, p)}
        if name != "table (today)":
            d, lo, hi = paired_boot(ym, pmat, tab[mature], logloss)
            r["dLogLoss vs table [95%]"] = f"{d:+.4f} [{lo:+.4f}, {hi:+.4f}]"
            d, lo, hi = paired_boot(ym, pmat, tab[mature], brier)
            r["dBrier vs table [95%]"] = f"{d:+.4f} [{lo:+.4f}, {hi:+.4f}]"
        else:
            r["dLogLoss vs table [95%]"] = r["dBrier vs table [95%]"] = "-"
        crows.append(r)
    O.table(pd.DataFrame(crows))
    O.data["clean"] = crows
    O.h("Calibration by decile, P(clean) (mature test)", 3)
    ct, cp = calibration(ym, tab[mature]), calibration(ym, pool[mature])
    m = ct.rename(columns={"predicted": "table pred", "observed": "table obs"}).merge(
        cp.rename(columns={"predicted": "pooled pred", "observed": "pooled obs", "n": "n2"}), on="decile")
    O.table(m.drop(columns=["n2"]), "{:.3f}")

    O.h("By model key on the mature test tasks (keys with at least 10)", 3)
    O.p("Mean realized option A score next to the mean prediction of each method for the same tasks. "
        "The standard error of a realized mean is about sd / sqrt(n), with sd near 0.33.")
    tk = test[mature].assign(y=yte["a"][mature], tab=preds["a"]["table"][mature], pool=preds["a"]["pooled"][mature],
                             nom=preds["a"]["nomodel"][mature], yc=yte["clean"][mature])
    g = tk.groupby("key").agg(n=("y", "size"), realized=("y", "mean"), se=("y", lambda v: v.std(ddof=1) / np.sqrt(len(v))),
                              table=("tab", "mean"), pooled=("pool", "mean"), no_model=("nom", "mean"),
                              clean=("yc", "mean")).query("n >= 10").sort_values("n", ascending=False).reset_index()
    O.table(g, "{:.3f}")

    # ---- cost ------------------------------------------------------------------
    O.h("Run cost")
    book = meta["book"]
    trc = train[train.cost > 0]
    tec_mask = (test.cost > 0).to_numpy()
    tec = test[tec_mask]
    train_tasks = [by_root[r] for r in train.root]
    fb = price_fallback(book, tec.key, tec.d, train_tasks)
    ctab = CostTable(trc.key, trc.d, trc.cost).predict(tec.key, tec.d, fb)
    pc = pooled("cost").fit(trc, np.log(trc.cost), T)
    cpool = np.exp(pc.predict(tec) + pc.sigma2 / 2)
    cmed = np.exp(pc.predict(tec))
    preds["cost"] = {"model": pc}
    yc = tec.cost.to_numpy()
    cm = mature[tec_mask]
    crow = []
    for name, p in (("table (today)", ctab), ("pooled log-normal mean", cpool), ("pooled log-normal median", cmed)):
        pm_, ym_ = p[cm], yc[cm]
        crow.append({"method": name, "n": int(cm.sum()), "MAE log": mae(np.log(ym_), np.log(pm_)),
                     "median abs % err": float(np.median(np.abs(pm_ / ym_ - 1)) * 100),
                     "MAE $": mae(ym_, pm_), "sum pred / sum actual": float(pm_.sum() / ym_.sum())})
    O.table(pd.DataFrame(crow))
    d, lo, hi = paired_boot(np.log(yc[cm]), np.log(cpool[cm]), np.log(ctab[cm]), mae)
    O.p()
    O.p(f"MAE of log cost, pooled minus table: {d:+.4f} [{lo:+.4f}, {hi:+.4f}].")
    O.data["cost"] = crow

    # ---- stability of model effects -----------------------------------------------
    O.h("Is a model's quality stable over time?")
    st, half = stability(df, ylast["a"], by_root, S)
    w = 1 / (1 / st.n1 + 1 / st.n2)
    corr = float(np.corrcoef(st.r1, st.r2)[0, 1]) if len(st) > 2 else float("nan")
    wc = float(np.cov(st.r1, st.r2, aweights=w)[0, 1] / np.sqrt(np.cov(st.r1, aweights=w) * np.cov(st.r2, aweights=w))) if len(st) > 2 else float("nan")
    rng = np.random.default_rng(1)
    perm = [np.corrcoef(st.r1, rng.permutation(st.r2.to_numpy()))[0, 1] for _ in range(2000)]
    pval = float(np.mean(np.array(perm) >= corr))
    O.p(f"Cells: model key x judge label with at least 8 tasks in each half (split at {iso(half)}). Option A score, "
        f"labels as of the snapshot. Residual = cell mean minus the half's mean for that label.")
    O.table(st.assign(key=st.key, label=st.label)[["key", "label", "n1", "n2", "r1", "r2"]], "{:+.3f}")
    O.p()
    O.p(f"Correlation of first-half and second-half residuals over {len(st)} cells: {corr:+.3f} "
        f"(weighted by cell size {wc:+.3f}); one-sided permutation p = {pval:.3f}.")
    O.data["stability"] = {"cells": len(st), "corr": corr, "wcorr": wc, "p": pval}
    sd_a = float(np.std(ylast["a"]))
    O.p(f"Per-task sd of the option A score: {sd_a:.3f}. Tasks per model and band needed to tell a 0.10 gap at 95% "
        f"confidence and 80% power: {int(math.ceil(2 * (1.96 + 0.84) ** 2 * sd_a ** 2 / 0.01))}; a 0.05 gap: "
        f"{int(math.ceil(2 * (1.96 + 0.84) ** 2 * sd_a ** 2 / 0.0025))}.")

    # ---- leakage -----------------------------------------------------------------
    O.h("Leakage check: when each feature is known")
    O.table(pd.DataFrame(FEATURE_TIMES, columns=["feature", "known at"]))
    O.p()
    O.p("Left out because they are known only after the start: run seconds, tokens, fix rounds, continuations, "
        "struggle counters, files and lines changed, worker_complete, ratings, brief defects, change titles, "
        "and every delivery step and defect (they are the label).")

    # ---- ablations ----------------------------------------------------------------
    O.h("Ablations")
    O.p("Each row refits the pooled model with the tuned lambda and tau and one feature group changed. "
        "Validation is the tuning split (inner train, labels at its cut); test is the mature test set. "
        "Decide on validation; test is shown for honesty. Lower is better.")
    variants = [("default (all default groups)", DEFAULT_GROUPS, "tuned")]
    for g in DEFAULT_GROUPS:
        gs = [x for x in DEFAULT_GROUPS if x != g]
        if g == "model_hier":
            gs = [x for x in gs if x != "model_x_d"]
        variants.append((f"without {g}", gs, "tuned"))
    variants += [("with provider_harness", DEFAULT_GROUPS + ["provider_harness"], "tuned"),
                 ("with context (machine, concurrent workers)", DEFAULT_GROUPS + ["context"], "tuned"),
                 ("no recency weight", DEFAULT_GROUPS, None),
                 ("no model information (no model_hier, model_x_d, bench_effort)", NO_MODEL, "tuned"),
                 ("model key only (no family or base)", None, "tuned"),
                 ("D + model only", ["d", "model_hier", "model_x_d"], "tuned")]
    arows = []
    for name, gs, tau in variants:
        r = {"variant": name}
        for tgt, fn, short in (("a", rmse, "A"), ("today", rmse, "today"), ("clean", logloss, "clean")):
            for split_name, (tr_, ytr_, te_, yte_, now) in {
                    "val": (itr, yitr[tgt], val, yval[tgt], T2),
                    "test": (train, ytr[tgt], test[mature], yte[tgt][mature], T)}.items():
                if gs is None:
                    m = pooled(tgt, DEFAULT_GROUPS, tau)
                    m.fit(tr_.assign(family="x", base=tr_.key), ytr_, now)
                    p = m.predict(te_.assign(family="x", base=te_.key))
                else:
                    p = pooled(tgt, gs, tau).fit(tr_, ytr_, now).predict(te_)
                r[f"{short} {split_name}"] = fn(yte_, p)
        arows.append(r)
    ab = pd.DataFrame(arows)
    O.table(ab)
    O.p()
    O.p("Columns: A and today are RMSE of the graded score; clean is log loss of P(clean).")
    O.data["ablation"] = arows

    # ---- learning curve and sample size ------------------------------------------
    O.h("Learning curve and sample size")
    lrows = []
    for frac in (0.25, 0.5, 0.75, 1.0):
        sub = train.iloc[-int(round(frac * len(train))):]
        ysub = labels_for(by_root, sub.root, T)
        for tgt in ("a", "today"):
            tab = Table(sub.key, sub.d, ysub[tgt]).predict(test.key, test.d)[mature]
            pool = pooled(tgt).fit(sub, ysub[tgt], T).predict(test)[mature]
            ym = yte[tgt][mature]
            dq = Table(np.full(len(sub), "all"), sub.d, ysub[tgt]).predict(np.full(len(test), "all"), test.d)[mature]
            lrows.append({"train tasks": len(sub), "score": tgt, "D bucket RMSE": rmse(ym, dq), "table RMSE": rmse(ym, tab),
                          "pooled RMSE": rmse(ym, pool), "pooled gain over table": rmse(ym, tab) - rmse(ym, pool)})
    O.table(pd.DataFrame(lrows))
    # Paired per-task squared error difference: how many test tasks would show a
    # gain of a given size at 95%.
    ym = yte["a"][mature]
    dsq = (ym - preds["a"]["table"][mature]) ** 2 - (ym - preds["a"]["pooled"][mature]) ** 2
    sd = dsq.std(ddof=1)
    O.p()
    O.p(f"Per-task squared error, table minus pooled (option A): mean {dsq.mean():+.5f}, sd {sd:.5f}, "
        f"n {len(dsq)}. Test tasks needed to see a gain of 2% of the table's MSE "
        f"({0.02 * np.mean((ym - preds['a']['table'][mature]) ** 2):.5f}) at 95%: "
        f"{int(math.ceil((1.96 * sd / (0.02 * np.mean((ym - preds['a']['table'][mature]) ** 2))) ** 2))}.")
    O.data["learning"] = lrows
    O.data["dsq"] = {"mean": float(dsq.mean()), "sd": float(sd), "n": int(len(dsq))}

    # ---- counterfactual picks --------------------------------------------------------
    O.h("Counterfactual picks on the test tasks")
    recent = train[train.start >= T - 7 * DAY]
    counts = train.key.value_counts()
    roster = [k for k in recent.key.unique() if counts.get(k, 0) >= 5]
    harness_of = dict(zip(df.key, df.harness))
    O.p(f"Roster: the {len(roster)} model keys with at least 5 training tasks that ran in the 7 days before the cut "
        f"({', '.join(sorted(roster))}).")
    tm = test[mature].reset_index(drop=True)
    cand = pd.concat([candidate_rows(r, [(k, harness_of[k]) for k in roster], bench).assign(task=i)
                      for i, r in tm.iterrows()], ignore_index=True)
    qa = preds["a"]["model"].predict(cand)
    qt_ = preds["today"]["model"].predict(cand)
    pcl = preds["clean"]["model"].predict(cand)
    cost_hat = np.exp(preds["cost"]["model"].predict(cand) + preds["cost"]["model"].sigma2 / 2)
    tab_a = Table(train.key, train.d, ytr["a"]).predict(cand.key, cand.d)
    tab_c = Table(train.key, train.d, ytr["clean"]).predict(cand.key, cand.d)
    fbc = price_fallback(book, cand.key, cand.d, train_tasks)
    tab_cost = CostTable(trc.key, trc.d, trc.cost).predict(cand.key, cand.d, fbc)
    cand = cand.assign(qa=qa, qt=qt_, pc=pcl, cost_hat=cost_hat, tab_a=tab_a, tab_c=tab_c, tab_cost=tab_cost)
    ya = yte["a"][mature]
    yc_ = yte["clean"][mature]
    actual_cost = tm.cost.to_numpy()
    prow = [{"policy": "logged (what ran)", "F": "-", "picks": ", ".join(f"{k} {v}" for k, v in tm.key.value_counts().head(5).items()),
             "E[quality A] pooled": float(preds["a"]["pooled"][mature].mean()),
             "E[clean] pooled": float(preds["clean"]["pooled"][mature].mean()),
             "E[cost] pooled": float(np.nanmean(np.exp(preds["cost"]["model"].predict(tm) + preds["cost"]["model"].sigma2 / 2))),
             "E[quality A] table": float(Table(train.key, train.d, ytr["a"]).predict(tm.key, tm.d).mean()),
             "realized quality A": float(ya.mean()), "realized clean": float(yc_.mean()),
             "realized cost": float(np.nanmean(actual_cost)), "agree with logged": "-",
             "realized A where agree": "-"}]

    def pick(score_col, cost_col, F, qcol):
        rows = []
        for i, g in cand.groupby("task"):
            obj = g[cost_col] + (1 - g[score_col]) * F
            rows.append(g.loc[obj.idxmin()])
        return pd.DataFrame(rows).reset_index(drop=True)

    for label, qcol, ccol in (("pooled, quality A", "qa", "cost_hat"), ("pooled, P(clean)", "pc", "cost_hat"),
                              ("table, quality A", "tab_a", "tab_cost"), ("table, P(clean)", "tab_c", "tab_cost")):
        for F in FAILURE_COSTS:
            ch = pick(qcol, ccol, F, qcol)
            agree = (ch.key.to_numpy() == tm.key.to_numpy())
            prow.append({"policy": label, "F": F,
                         "picks": ", ".join(f"{k} {v}" for k, v in ch.key.value_counts().head(4).items()),
                         "E[quality A] pooled": float(ch.qa.mean()), "E[clean] pooled": float(ch.pc.mean()),
                         "E[cost] pooled": float(ch.cost_hat.mean()), "E[quality A] table": float(ch.tab_a.mean()),
                         "realized quality A": "-", "realized clean": "-", "realized cost": "-",
                         "agree with logged": f"{agree.sum()}/{len(agree)}",
                         "realized A where agree": f"{ya[agree].mean():.3f}" if agree.any() else "-"})
    O.p()
    O.p("Rule: pick the key with the lowest cost + (1 - quality) x F, where F is the dollar cost of a failed "
        "task (rescue and owner time). Quality is E[option A score] or P(clean). Each policy is shown with its "
        "expected values under the pooled model and under the table, so a policy is never judged only by the "
        "model that chose it.")
    O.table(pd.DataFrame(prow), "{:.3f}")
    O.data["counterfactual"] = prow

    # ---- the D 3.20 case -------------------------------------------------------------
    O.h(f"The D 3.20 case ({CASE_SESSION})")
    db = meta["db"]
    run = db.execute("SELECT run_id FROM worker_assessments WHERE session_id=? AND run_id IN (SELECT run_id FROM pick_candidates)",
                     (CASE_SESSION,)).fetchone()
    case_row = df[df.root == CASE_SESSION]
    if run is None or case_row.empty:
        O.p("Case not found in the snapshot.")
    else:
        cr = case_row.iloc[0]
        cut = int(cr.start)
        tr = split(df, cut)
        ycase = labels_for(by_root, tr.root, cut)
        pc_rows = db.execute("""SELECT name, harness, model, cell_score, p_clean, cost_per_run, expected_cost, adjusted_cost, is_chosen, is_qualified
                                FROM pick_candidates WHERE run_id=? ORDER BY pos""", (run["run_id"],)).fetchall()
        keys = [(r["model"].lower() if r["harness"] != "codex" or ":" in r["model"] else r["model"].lower() + ":default", r["harness"]) for r in pc_rows]
        keys.append(("gpt-6.2-astra:high", "codex"))
        cc = candidate_rows(cr, keys, bench)
        ms = {t: pooled(t).fit(tr, ycase[t], cut) for t in ("a", "today", "clean")}
        trc2 = tr[tr.cost > 0]
        mc = pooled("cost").fit(trc2, np.log(trc2.cost), cut)
        cc = cc.assign(q_a=ms["a"].predict(cc), q_today=ms["today"].predict(cc), p_clean=ms["clean"].predict(cc),
                       cost=np.exp(mc.predict(cc) + mc.sigma2 / 2),
                       table_q_today=Table(tr.key, tr.d, ycase["today"]).predict(cc.key, cc.d),
                       table_n=Table(tr.key, tr.d, ycase["today"]).n(cc.key, cc.d))
        rec = {(k, h): r for (k, h), r in zip(keys, pc_rows)}
        rows = []
        for _, r in cc.iterrows():
            pr = rec.get((r.key, r.harness))
            rows.append({"candidate": pr["name"] if pr else f"{r.key} (new class, no record)", "key": r.key,
                         "recorded cell": pr["cell_score"] if pr and pr["cell_score"] is not None else float("nan"),
                         "recorded p": pr["p_clean"] if pr and pr["p_clean"] is not None else float("nan"),
                         "recorded run $": pr["cost_per_run"] if pr and pr["cost_per_run"] is not None else float("nan"),
                         "recorded expected $": pr["expected_cost"] if pr and pr["expected_cost"] is not None else float("nan"),
                         "table today (rebuilt)": r.table_q_today, "cell n": int(r.table_n),
                         "pooled q today": r.q_today, "pooled q A": r.q_a, "pooled p clean": r.p_clean,
                         "pooled run $": r.cost, "chosen": "yes" if pr and pr["is_chosen"] else ""})
        case = pd.DataFrame(rows)
        O.p(f"Model trained on the {len(tr)} tasks that ended before this start, labels as of the start. "
            f"D {cr.d:.2f}, judge R{int(cr.R)} S{int(cr.S)} C{int(cr.C)} U{int(cr.U)} V{int(cr.V)} K{int(cr.K)}.")
        O.table(case, "{:.3f}")
        dec = []
        for F in FAILURE_COSTS:
            sub = case[case["candidate"].str.contains("no record") == False]  # noqa: E712
            o1 = sub["pooled run $"] + (1 - sub["pooled q A"]) * F
            o2 = sub["pooled run $"] + (1 - sub["pooled p clean"]) * F
            dec.append({"F": F, "pick by quality A": sub.loc[o1.idxmin(), "candidate"],
                        "pick by P(clean)": sub.loc[o2.idxmin(), "candidate"]})
        O.p()
        O.table(pd.DataFrame(dec))
        O.data["case"] = rows

    (out_dir / "tables.md").write_text("\n".join(O.lines) + "\n")
    (out_dir / "results.json").write_text(json.dumps(O.data, indent=1, default=str) + "\n")
    print(f"wrote {out_dir / 'tables.md'}")


if __name__ == "__main__":
    main()
