"""Held-out metrics, calibration tables and paired bootstrap intervals."""

from __future__ import annotations

import numpy as np
import pandas as pd

RNG_SEED = 20261118


def mae(y, p):
    return float(np.mean(np.abs(y - p)))


def rmse(y, p):
    return float(np.sqrt(np.mean((y - p) ** 2)))


def logloss(y, p):
    p = np.clip(p, 1e-6, 1 - 1e-6)
    return float(-np.mean(y * np.log(p) + (1 - y) * np.log(1 - p)))


def brier(y, p):
    return float(np.mean((y - p) ** 2))


def auc(y, p):
    y = np.asarray(y, int)
    pos, neg = p[y == 1], p[y == 0]
    if len(pos) == 0 or len(neg) == 0:
        return float("nan")
    # Mann-Whitney with ties counted as half.
    r = pd.Series(np.concatenate([pos, neg])).rank().to_numpy()
    return float((r[: len(pos)].sum() - len(pos) * (len(pos) + 1) / 2) / (len(pos) * len(neg)))


def paired_boot(y, a, b, fn, n=2000):
    """fn(y, a) - fn(y, b) with a 95% percentile interval over resampled tasks."""
    rng = np.random.default_rng(RNG_SEED)
    idx = rng.integers(0, len(y), size=(n, len(y)))
    diffs = np.array([fn(y[i], a[i]) - fn(y[i], b[i]) for i in idx])
    return float(fn(y, a) - fn(y, b)), float(np.percentile(diffs, 2.5)), float(np.percentile(diffs, 97.5))


def calibration(y, p, bins=10) -> pd.DataFrame:
    """Rows of equal size by predicted value (ties split by order)."""
    order = np.argsort(p, kind="stable")
    parts = np.array_split(order, bins)
    rows = []
    for i, ix in enumerate(parts):
        if len(ix):
            rows.append({"decile": i + 1, "n": len(ix), "predicted": p[ix].mean(), "observed": y[ix].mean()})
    return pd.DataFrame(rows)


def calib_error(y, p, bins=10) -> float:
    """Expected calibration error: the size weighted mean |predicted - observed|."""
    c = calibration(y, p, bins)
    return float(np.sum(c.n * (c.predicted - c.observed).abs()) / c.n.sum())
