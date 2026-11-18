"""The binned table (today's method) and the partially pooled models.

Table: internal/protocol/picker.go. Buckets of D, width 1/3 from 1.0; a cell is
shrunk to its bucket mean with k = 5; an empty cell takes the bucket mean, and
an empty bucket the mean of all tasks.

Pooled model: a GLM with Gaussian priors on group effects, fitted by maximum a
posteriori (penalized likelihood). The model effect is nested: family, then
base model (family and tier, any version), then model key (version, effort,
provider, harness). Each level has its own intercept and its own slope on D, all
shrunk to zero, so a key with few tasks borrows from its base model and its
family, and a new key with no task gets exactly its parent's effect. Links:
  fractional logit for the graded score (Papke and Wooldridge 1996: the
    Bernoulli quasi-likelihood gives a consistent mean for any outcome in
    [0, 1], with its lump at 0 and its lump at 1),
  logistic for P(clean),
  Gaussian on log cost.
"""

from __future__ import annotations

import math

import numpy as np
import pandas as pd
from scipy.optimize import minimize
from scipy.special import expit

# ---- today's table -------------------------------------------------------------

BUCKET_W, K_SHRINK, D_START = 0.3333333333, 5.0, 1.0


def bucket(d) -> np.ndarray:
    return np.maximum(0, np.floor((np.asarray(d, float) - D_START) / BUCKET_W + 1e-6)).astype(int)


class Table:
    def __init__(self, keys, ds, ys):
        df = pd.DataFrame({"key": keys, "b": bucket(ds), "y": np.asarray(ys, float)})
        self.all_mean = df.y.mean() if len(df) else 0.5
        self.bucket_mean = df.groupby("b").y.mean().to_dict()
        g = df.groupby(["key", "b"]).y
        self.cell_sum, self.cell_n = g.sum().to_dict(), g.size().to_dict()

    def predict(self, keys, ds) -> np.ndarray:
        out = []
        for k, b in zip(keys, bucket(ds)):
            mean = self.bucket_mean.get(b, self.all_mean)
            n = self.cell_n.get((k, b), 0)
            out.append((self.cell_sum.get((k, b), 0.0) + K_SHRINK * mean) / (n + K_SHRINK))
        return np.array(out)

    def n(self, keys, ds) -> np.ndarray:
        return np.array([self.cell_n.get((k, b), 0) for k, b in zip(keys, bucket(ds))])


class CostTable:
    """ExpectedCost: the cell's average recorded cost, else the fallback the
    caller passes (the key's price times the bucket's average tokens)."""

    def __init__(self, keys, ds, costs):
        df = pd.DataFrame({"key": keys, "b": bucket(ds), "c": np.asarray(costs, float)}).dropna()
        self.cell = df.groupby(["key", "b"]).c.mean().to_dict()
        self.bucket = df.groupby("b").c.mean().to_dict()
        self.all = df.c.mean()

    def predict(self, keys, ds, fallback) -> np.ndarray:
        out = []
        for k, b, fb in zip(keys, bucket(ds), fallback):
            out.append(self.cell[(k, b)] if (k, b) in self.cell else fb)
        return np.array(out)


# ---- pooled GLM ----------------------------------------------------------------

NUMERIC_GROUPS = {
    "d": ["d", "d2"],
    "judge_dims": ["R", "S", "C", "U", "V", "K"],
    "judge_meta": ["bottleneck", "conf_high", "conf_low"],
    "brief": ["reasons_len", "has_done", "n_done", "done_len", "done_paths"],
    "bench_effort": ["bench", "bench_missing", "effort_ord"],
    "context": ["concurrent"],
}
# Grouped effects: name -> (column, has a slope on D)
RE_GROUPS = {
    "model_hier": [("family", True), ("base", True), ("key", True)],
    "project": [("project", False)],
    "judge_meta": [("judge_family", False)],
    "provider_harness": [("provider", False), ("harness", False)],
    "context": [("node", False)],
}
DEFAULT_GROUPS = ["d", "judge_dims", "judge_meta", "brief", "bench_effort", "model_hier", "model_x_d", "project"]
ALL_GROUPS = DEFAULT_GROUPS + ["provider_harness", "context"]


class Design:
    """Builds the same columns for train and for any later rows."""

    def __init__(self, train: pd.DataFrame, groups: list[str]):
        self.groups = groups
        self.num_cols = [c for g in groups for c in NUMERIC_GROUPS.get(g, [])]
        self.re = [(c, s and "model_x_d" in groups) for g in groups for (c, s) in RE_GROUPS.get(g, [])]
        x = self._numeric(train)
        self.mu = x.mean(0)
        sd = x.std(0)
        # A column constant in training (for example the done checks before they
        # were stored) stays at 0 after scaling and keeps a zero coefficient.
        self.sd = np.where(sd < 1e-9, 1.0, sd)
        self.levels = {c: sorted(train[c].astype(str).unique()) for c, _ in self.re}
        self.dmu = train.d.mean()

    def _numeric(self, df: pd.DataFrame) -> np.ndarray:
        cols = []
        bench_fill = 35.0
        for c in self.num_cols:
            if c == "d2":
                v = (df.d - 3.0) ** 2
            elif c == "conf_high":
                v = (df.confidence == "high").astype(float)
            elif c == "conf_low":
                v = (~df.confidence.isin(["high", "medium"])).astype(float)
            elif c == "bench":
                v = df.bench.fillna(bench_fill)
            elif c == "bench_missing":
                v = df.bench.isna().astype(float)
            else:
                v = df[c].astype(float).fillna(0)
            cols.append(np.asarray(v, float))
        return np.column_stack(cols) if cols else np.zeros((len(df), 0))

    def matrix(self, df: pd.DataFrame):
        """X and, per column, the group name of its penalty ('fixed' or 'c:int'/'c:slope')."""
        blocks = [(self._numeric(df) - self.mu) / self.sd]
        pen = ["fixed"] * len(self.num_cols)
        dc = np.asarray(df.d, float) - self.dmu
        for c, slope in self.re:
            vals = df[c].astype(str).to_numpy()
            lv = self.levels[c]
            idx = {v: i for i, v in enumerate(lv)}
            m = np.zeros((len(df), len(lv)))
            for r, v in enumerate(vals):
                if v in idx:  # an unseen level gets 0: its parent's effect
                    m[r, idx[v]] = 1
            blocks.append(m)
            pen += [f"{c}:int"] * len(lv)
            if slope:
                blocks.append(m * dc[:, None])
                pen += [f"{c}:slope"] * len(lv)
        return np.column_stack(blocks), np.array(pen)


class Pooled:
    """MAP fit. lam_fixed is the ridge on standardized fixed effects; lam_re the
    precision of each group intercept; lam_re x slope_mult that of each slope."""

    def __init__(self, link: str, groups=None, lam_re=3.0, lam_fixed=1.0, slope_mult=4.0, tau_days=None):
        self.link, self.groups = link, groups or DEFAULT_GROUPS
        self.lam_re, self.lam_fixed, self.slope_mult, self.tau = lam_re, lam_fixed, slope_mult, tau_days

    def _weights(self, df, now):
        if not self.tau:
            return np.ones(len(df))
        age = (now - df.start.to_numpy()) / 86400
        w = np.exp(-age / self.tau)
        return w / w.mean()

    def fit(self, df: pd.DataFrame, y, now=None):
        y = np.asarray(y, float)
        self.design = Design(df, self.groups)
        X, pen = self.design.matrix(df)
        lam = np.where(pen == "fixed", self.lam_fixed,
                       np.where(np.char.endswith(pen.astype(str), ":slope"), self.lam_re * self.slope_mult, self.lam_re))
        w = self._weights(df, now if now is not None else df.start.max())
        if self.link == "gaussian":
            self.ymu, self.ysd = y.mean(), y.std() + 1e-9
            y = (y - self.ymu) / self.ysd
        n, p = X.shape
        b0 = np.zeros(p + 1)
        if self.link != "gaussian":
            m = np.clip(np.average(y, weights=w), 1e-3, 1 - 1e-3)
            b0[0] = math.log(m / (1 - m))

        def f(b):
            eta = b[0] + X @ b[1:]
            if self.link == "gaussian":
                r = eta - y
                loss = 0.5 * np.sum(w * r * r)
                g = w * r
            else:
                mu = expit(eta)
                mu_c = np.clip(mu, 1e-12, 1 - 1e-12)
                loss = -np.sum(w * (y * np.log(mu_c) + (1 - y) * np.log(1 - mu_c)))
                g = w * (mu - y)
            loss += 0.5 * np.sum(lam * b[1:] ** 2)
            grad = np.concatenate([[g.sum()], X.T @ g + lam * b[1:]])
            return loss, grad

        res = minimize(f, b0, jac=True, method="L-BFGS-B", options={"maxiter": 2000})
        self.b = res.x
        if self.link == "gaussian":
            resid = (self.b[0] + X @ self.b[1:]) - y
            self.sigma2 = float(np.average(resid ** 2, weights=w)) * self.ysd ** 2
        return self

    def eta(self, df):
        X, _ = self.design.matrix(df)
        return self.b[0] + X @ self.b[1:]

    def predict(self, df) -> np.ndarray:
        e = self.eta(df)
        if self.link == "gaussian":
            return e * self.ysd + self.ymu
        return expit(e)


class TwoPart:
    """P(score > 0) x E[score | score > 0], both pooled."""

    def __init__(self, **kw):
        self.kw = kw

    def fit(self, df, y, now=None):
        y = np.asarray(y, float)
        self.p = Pooled("logit", **self.kw).fit(df, (y > 0).astype(float), now)
        pos = y > 0
        self.m = Pooled("logit", **self.kw).fit(df[pos], y[pos], now)
        return self

    def predict(self, df):
        return self.p.predict(df) * self.m.predict(df)


# ---- gradient boosting baseline (nonlinearity check only) ----------------------

GBM_NUM = ["d", "R", "S", "C", "U", "V", "K", "bottleneck", "reasons_len", "has_done", "n_done", "done_len",
           "done_paths", "bench", "effort_ord"]
GBM_CAT = ["family", "base", "key", "project", "confidence", "judge_family"]


class GBM:
    def __init__(self, kind: str):
        self.kind = kind

    def _x(self, df):
        x = df[GBM_NUM].astype(float).copy()
        for c in GBM_CAT:
            x[c] = df[c].astype(str).map(self.codes[c]).fillna(-1).astype(float)
        return x

    def fit(self, df, y, now=None):
        from sklearn.ensemble import HistGradientBoostingClassifier, HistGradientBoostingRegressor
        self.codes = {c: {v: i for i, v in enumerate(sorted(df[c].astype(str).unique()))} for c in GBM_CAT}
        cat = [False] * len(GBM_NUM) + [True] * len(GBM_CAT)
        args = dict(max_iter=150, learning_rate=0.05, max_depth=3, min_samples_leaf=20, l2_regularization=1.0,
                    categorical_features=cat, random_state=0)
        X = self._x(df)
        X.iloc[:, len(GBM_NUM):] = X.iloc[:, len(GBM_NUM):].clip(lower=0)
        if self.kind == "binary":
            self.m = HistGradientBoostingClassifier(**args).fit(X, np.asarray(y, int))
        else:
            self.m = HistGradientBoostingRegressor(**args).fit(X, np.asarray(y, float))
        return self

    def predict(self, df):
        X = self._x(df)
        # An unseen category is coded -1; HistGradientBoosting treats negative codes as missing.
        X.iloc[:, len(GBM_NUM):] = X.iloc[:, len(GBM_NUM):].where(X.iloc[:, len(GBM_NUM):] >= 0, np.nan)
        if self.kind == "binary":
            return self.m.predict_proba(X)[:, 1]
        return np.clip(self.m.predict(X), 0, 1)
