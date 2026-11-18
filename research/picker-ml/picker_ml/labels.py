"""Task labels, computed from the events recorded up to a given time.

score_today  : protocol.PickerTaskScore. 0 unless complete; else
               1 - 0.25 x (fix rounds + failed steps + defects + defect severity).
score_a      : option A (decision 0173 draft, owner approved building it).
               fix round 0; failed test -0.2; failed accept -0.2; failed
               review -0.3; defect -0.25 x severity (counted once);
               not complete scores 0 unless its change merged.
clean        : complete, no failed accept, review or test, no defect.
"""

from __future__ import annotations

from .data import Task


def facts(t: Task, asof: int) -> dict:
    ev = [e for e in t.events if e.at <= asof]
    f = {"accept": 0, "review": 0, "test": 0, "defects": 0, "severity": 0, "merged": False}
    for e in ev:
        if e.kind in ("accept", "review", "test"):
            f[e.kind] += 1
        elif e.kind == "defect":
            f["defects"] += 1
            f["severity"] += e.severity
        elif e.kind == "merge":
            f["merged"] = True
    return f


def score_today(t: Task, asof: int) -> float:
    if not t.complete:
        return 0.0
    f = facts(t, asof)
    n = t.fix_rounds + f["accept"] + f["review"] + f["test"] + f["defects"] + f["severity"]
    return max(0.0, 1 - 0.25 * n)


def score_a(t: Task, asof: int) -> float:
    f = facts(t, asof)
    if not t.complete and not f["merged"]:
        return 0.0
    pen = 0.2 * (f["test"] + f["accept"]) + 0.3 * f["review"] + 0.25 * f["severity"]
    return max(0.0, 1 - pen)


def clean(t: Task, asof: int) -> int:
    f = facts(t, asof)
    return int(t.complete and f["accept"] + f["review"] + f["test"] + f["defects"] == 0)


def lag_days(tasks: list[Task]) -> dict[str, list[float]]:
    """Days from a task's end to each negative event, by kind."""
    out: dict[str, list[float]] = {}
    for t in tasks:
        for e in t.events:
            if e.kind != "merge":
                out.setdefault(e.kind, []).append((e.at - t.end) / 86400)
    return out
