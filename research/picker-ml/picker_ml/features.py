"""Pre-start features of a task.

Every feature here is known before the worker starts. FEATURE_TIMES lists, for
each one, the moment it becomes known; RESULTS.md prints that table. Nothing
read after the first run starts is used (no run length, tokens, fix rounds,
continuations, struggle counters, files changed or later events).
"""

from __future__ import annotations

import json
import math
import re
from pathlib import Path

import numpy as np
import pandas as pd

from .data import Task

FEATURE_TIMES = [
    ("judge R S C U V K, D, label, confidence, bottleneck", "judge result, written before the pick (assessed_at <= first run start for 1279 of 1284 judged sessions; the other 5 are within seconds)"),
    ("judge model", "chosen before the judge runs"),
    ("judge reasons length (brief complexity proxy)", "judge result, before the pick"),
    ("project", "worker start request"),
    ("done checks: count, length, paths named", "worker start request (stored since 2026-11-12 only)"),
    ("model key, family, tier, version, effort, harness, provider", "the candidate being scored"),
    ("benchmark score", "bundled table internal/protocol/scores.json"),
    ("machine", "worker start request (context ablation only)"),
    ("concurrent workers", "count of tasks running at the start moment (context ablation only)"),
    ("start time", "used only for the split and the recency weight, never as an input"),
]

EFFORT_ORDER = {"minimal": 0, "low": 1, "medium": 2, "default": 2, "none": 2, "high": 3, "xhigh": 4, "max": 5}
_efforts = {"minimal", "low", "medium", "high", "xhigh", "max"}


def parse_model(key: str, harness: str) -> dict:
    """family, base (family + tier, no version), version, effort, provider."""
    m = key.lower()
    provider = m.split("/", 1)[0] if "/" in m else harness
    name = m.rsplit("/", 1)[-1]
    effort = ""
    if ":" in name:
        name, effort = name.split(":", 1)
    else:
        last = name.rsplit("-", 1)
        if len(last) == 2 and last[1] in _efforts:
            name, effort = last
    if effort in ("", "default"):
        effort = "default" if harness == "codex" else ("none" if not effort else effort)
    aliases = {"opus": "claude-opus-5-5", "sonnet": "claude-sonnet-5-5", "haiku": "claude-haiku-5-5"}
    name = aliases.get(name, name)
    name = re.sub(r"(?:-\d{4,})$", "", name)
    if name.startswith("claude"):
        family, tier = "anthropic", name.split("-")[1]
        version = ".".join(name.split("-")[2:4])
    elif name.startswith("gpt"):
        family = "openai"
        parts = name.split("-")  # gpt, 6.1, terra
        version, tier = parts[1], parts[-1]
    elif name.startswith("gemini"):
        family = "google"
        parts = name.split("-")
        version, tier = parts[1], "-".join(parts[2:])
    elif name.startswith("deepseek"):
        family = "deepseek"
        parts = name.split("-")
        version, tier = parts[1], "-".join(parts[2:]) or "base"
    else:
        family, tier, version = "other", name, ""
    return {"family": family, "base": f"{family}:{tier}", "version": version, "effort": effort,
            "provider": provider, "name": name}


def _score_name(model: str) -> str:
    m = model.lower().strip().rsplit("/", 1)[-1]
    m = m.split(":", 1)[0]
    m = re.sub(r"(?:-\d{4,})$", "", m)
    m = re.sub(r"(\d)\.(\d)", r"\1-\2", m)
    return {"opus": "claude-opus-5-5", "sonnet": "claude-sonnet-5-5", "haiku": "claude-haiku-5-5"}.get(m, m)


class Benchmarks:
    """A simplified LookupModelScoreWithDefaultEffort over scores.json."""

    def __init__(self, path: Path):
        self.rows = json.loads(path.read_text())

    def score(self, key: str, harness: str) -> float:
        pm = parse_model(key, harness)
        name = _score_name(pm["name"])
        want = pm["effort"] if pm["effort"] in _efforts else "medium"
        cands = [r for r in self.rows if _score_name(r["match"]) == name]
        if not cands:
            return float("nan")
        exact = [r for r in cands if r.get("effort") == want]
        if exact:
            return exact[0]["score"]
        return min(cands, key=lambda r: abs(EFFORT_ORDER.get(r.get("effort", "medium"), 2) - EFFORT_ORDER[want]))["score"]


_path_re = re.compile(r"[\w.-]+/[\w./-]+|\b[\w-]+\.(?:go|py|ts|tsx|js|mjs|md|sql|json|yaml|yml|sh|toml)\b")


def task_frame(tasks: list[Task], bench: Benchmarks) -> pd.DataFrame:
    rows = []
    starts = np.array([t.start for t in tasks])
    ends = np.array([t.end for t in tasks])
    for t in tasks:
        j = t.judge or {}
        pm = parse_model(t.key, t.harness)
        done_text = " ".join(str(x) for x in t.done_items)
        rows.append({
            "root": t.root, "key": t.key, "harness": t.harness, "start": t.start, "end": t.end,
            "d": t.d, "R": j.get("reasoning", np.nan), "S": j.get("scope", np.nan), "C": j.get("coupling", np.nan),
            "U": j.get("uncertainty", np.nan), "V": j.get("verification", np.nan), "K": j.get("risk", np.nan),
            "label": j.get("label", ""), "confidence": j.get("confidence", ""),
            "bottleneck": int(bool(j.get("bottleneck"))), "judge_family": parse_model(j.get("judge_model", ""), "")["family"],
            "reasons_len": math.log1p(j.get("reasons_len", 0)),
            "project": t.project or "unknown", "has_done": int(bool(t.done_items)),
            "n_done": len(t.done_items), "done_len": math.log1p(len(done_text)),
            "done_paths": len(_path_re.findall(done_text)),
            "family": pm["family"], "base": pm["base"], "version": pm["version"], "effort": pm["effort"],
            "effort_ord": EFFORT_ORDER.get(pm["effort"], 2), "provider": pm["provider"],
            "bench": bench.score(t.key, t.harness), "node": t.node,
            # Tasks running when this one started, this one excluded.
            "concurrent": int(((starts < t.start) & (ends > t.start)).sum()),
            "cost": t.cost if t.cost is not None else np.nan,
        })
    return pd.DataFrame(rows)


def candidate_rows(base_row: pd.Series, keys_harness: list[tuple[str, str]], bench: Benchmarks) -> pd.DataFrame:
    """The same task scored for several candidate models."""
    out = []
    for key, harness in keys_harness:
        r = base_row.copy()
        pm = parse_model(key, harness)
        r["key"], r["harness"] = key, harness
        for f in ("family", "base", "version", "effort", "provider"):
            r[f] = pm[f]
        r["effort_ord"] = EFFORT_ORDER.get(pm["effort"], 2)
        r["bench"] = bench.score(key, harness)
        out.append(r)
    return pd.DataFrame(out)
