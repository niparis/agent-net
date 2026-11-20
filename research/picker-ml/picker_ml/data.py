"""Load worker tasks from a gateway snapshot.

A task is built the way the gateway builds it (internal/gateway/picker.go,
pickerTaskRecords): a run and its continuations form one chain, keyed by the
run's task_id or else its session. Stopped and infra runs do not count. The
first counted run gives the model key, the last gives the outcome and the end.
D is the first judge result of the root session, or else of the first session
of the chain that has one.

Unlike the gateway, every later event (failed step, defect, merge) keeps its
time, so a label can be computed "as of" any moment. That is what the time
split needs: a model trained at time T may only see what was recorded by T.

The snapshot is opened read only with immutable=1, so SQLite never writes
next to it.
"""

from __future__ import annotations

import json
import re
import sqlite3
from dataclasses import dataclass, field
from pathlib import Path

STOPPED, INFRA, COMPLETE = "stopped", "infra", "complete"


def open_snapshot(path: str) -> sqlite3.Connection:
    p = Path(path).resolve()
    if not p.exists():
        raise SystemExit(f"snapshot not found: {p}")
    db = sqlite3.connect(f"file:{p}?immutable=1", uri=True)
    db.row_factory = sqlite3.Row
    return db


@dataclass
class Event:
    at: int
    kind: str  # accept, review, test (failed steps), defect, merge
    severity: int = 0


@dataclass
class Task:
    root: str
    key: str
    harness: str
    model: str
    node: str
    caller_agent: str
    sessions: list[str] = field(default_factory=list)
    start: int = 0  # unix seconds, first counted run
    end: int = 0  # unix seconds, last counted run
    complete: bool = False
    fix_rounds: int = 0
    continues: int = 0  # extra runs in the chain (continuations)
    events: list[Event] = field(default_factory=list)
    d: float = 0.0
    judge: dict | None = None
    project: str = ""
    done_items: list[str] = field(default_factory=list)
    touches: set[str] = field(default_factory=set)
    task_id_set: bool = False  # the start continued an earlier worker's task
    tokens: dict = field(default_factory=dict)
    cost: float | None = None
    price_known: bool = False
    has_change: bool = False  # a session of the chain opened a delivery change


# ---- price book, as internal/protocol/price.go ---------------------------------

_price_date = re.compile(r"-\d{8}$")
_price_version = re.compile(r"\b(\d)-(\d)\b")
_price_efforts = {"minimal", "low", "medium", "high", "xhigh", "max", "ultra"}


def _price_names(model: str) -> list[str]:
    m = model.strip().lower()
    if ":" in m:
        m = m[: m.index(":")]
    m = m[m.rfind("/") + 1 :]
    m = _price_date.sub("", m)
    m = _price_version.sub(r"\1.\2", m)
    names = [m]
    i = m.rfind("-")
    if i > 0 and m[i + 1 :] in _price_efforts:
        names.append(m[:i])
    return names


def _price_vendor(name: str) -> str:
    for pre, v in (("claude", "anthropic"), ("gpt", "openai"), ("o1", "openai"), ("o3", "openai"),
                   ("o4", "openai"), ("gemini", "google"), ("deepseek", "deepseek")):
        if name.startswith(pre):
            return v
    return ""


class PriceBook:
    def __init__(self, rows):
        self.all = []
        self.by_name: dict[str, list[dict]] = {}
        for r in rows:
            if (r["prompt"] <= 0 and r["completion"] <= 0) or any(c in r["id"] for c in ":~"):
                continue
            self.all.append(r)
            self.by_name.setdefault(r["id"][r["id"].rfind("/") + 1 :], []).append(r)
        self.all.sort(key=lambda r: r["id"])

    def lookup(self, model: str):
        for name in _price_names(model):
            lst = self.by_name.get(name)
            if not lst:
                continue
            vendor = _price_vendor(name)
            for p in lst:
                if p["id"].startswith(vendor + "/"):
                    return p
            return lst[0]
        return None

    def price_of(self, model: str):
        p = self.lookup(model)
        if p is not None:
            return p, True
        by = sorted(self.all, key=lambda r: r["prompt"] + r["completion"])
        return (by[len(by) // 2], False) if by else (None, False)

    @staticmethod
    def cost(p, t: dict) -> float:
        read = p["cache_read"] if p["cache_read"] > 0 else p["prompt"]
        write = p["cache_write"] if p["cache_write"] > 0 else p["prompt"]
        return (t.get("in", 0) * p["prompt"] + (t.get("out", 0) + t.get("reasoning", 0)) * p["completion"]
                + t.get("cache_read", 0) * read + t.get("cache_write", 0) * write)


# ---- model key, as protocol.PickerModelKey -------------------------------------

def model_key(harness: str, model: str) -> str:
    m = model.strip().lower()
    if not m:
        return ""
    if harness == "codex" and ":" not in m and not m.endswith(":default"):
        m += ":default"
    return m


def load(path: str) -> tuple[list[Task], dict]:
    db = open_snapshot(path)
    q = lambda s, *a: db.execute(s, a).fetchall()  # noqa: E731

    # First valid judge result of each session, oldest first (as the gateway).
    judged: dict[str, dict] = {}
    for r in q("""SELECT session_id, d, reasoning, scope, coupling, uncertainty, verification, risk,
                   label, confidence, bottleneck, judge_model, reasons, assessed_at
                   FROM worker_assessments WHERE d > 0 ORDER BY started_at, assessed_at"""):
        if r["session_id"] not in judged:
            j = dict(r)
            try:
                reasons = json.loads(r["reasons"] or "{}")
            except json.JSONDecodeError:
                reasons = {}
            j["reasons_len"] = sum(len(str(v)) for v in reasons.values()) if isinstance(reasons, dict) else 0
            del j["reasons"]
            judged[r["session_id"]] = j

    # Defects by session, with time. The auto defect for a failed merger test
    # step is skipped: the failed step already counts (decision 0161).
    defects: dict[str, list[Event]] = {}
    for r in q("""SELECT session_id, severity, at FROM worker_defects
                  WHERE moved_at = 0 AND auto_kind IS NOT 'test'"""):
        defects.setdefault(r["session_id"], []).append(Event(r["at"], "defect", r["severity"]))

    # Failed accept, review or test steps count against the session that opened
    # the change.
    failed: dict[str, list[Event]] = {}
    for r in q("""SELECT json_extract(c.origin, '$.session_id') s, e.step, e.at
                  FROM delivery_evidence e JOIN delivery_changes c ON c.id = e.change_id
                  WHERE e.result = 'fail' AND e.step IN ('accept', 'review', 'test')"""):
        if r["s"]:
            failed.setdefault(r["s"], []).append(Event(r["at"], r["step"]))

    # The time a session's change merged: its first passing merge step.
    merged: dict[str, int] = {}
    for r in q("""SELECT json_extract(c.origin, '$.session_id') s, MIN(e.at) at
                  FROM delivery_evidence e JOIN delivery_changes c ON c.id = e.change_id
                  WHERE e.step = 'merge' AND e.result = 'pass' GROUP BY 1"""):
        if r["s"]:
            merged[r["s"]] = r["at"]

    with_change = {r["s"] for r in q("SELECT DISTINCT json_extract(origin, '$.session_id') s FROM delivery_changes") if r["s"]}

    usage: dict[str, dict] = {}
    for r in q("SELECT session_id, usage FROM worker_usage"):
        try:
            t = json.loads(r["usage"])
        except json.JSONDecodeError:
            continue
        if isinstance(t, dict) and any(t.values()):
            usage[r["session_id"]] = t

    starts: dict[str, dict] = {}
    for r in q("SELECT session_id, project, done, created_at, node_id FROM worker_starts WHERE session_id != ''"):
        starts.setdefault(r["session_id"], dict(r))

    book = PriceBook([dict(r) for r in q("SELECT id, prompt, completion, cache_read, cache_write FROM model_prices")])

    groups: dict[str, Task] = {}
    order: list[Task] = []
    for r in q("""SELECT node_id, session_id, task_id, harness, model, outcome, started_at, ended_at, run_seconds,
                         fix_count, touches, caller_agent_id FROM worker_runs ORDER BY ended_at, id"""):
        if not r["session_id"] or r["outcome"] in (STOPPED, INFRA):
            continue
        root = r["task_id"] or r["session_id"]
        t = groups.get(root)
        if t is None:
            start = r["started_at"] or (r["ended_at"] - r["run_seconds"])
            t = Task(root=root, key=model_key(r["harness"], r["model"]), harness=r["harness"], model=r["model"],
                     node=r["node_id"], caller_agent=r["caller_agent_id"] or "", start=start,
                     task_id_set=bool(r["task_id"]) and r["task_id"] != r["session_id"])
            groups[root] = t
            order.append(t)
        else:
            t.continues += 1
        t.complete = r["outcome"] == COMPLETE
        t.fix_rounds += r["fix_count"]
        t.end = r["ended_at"]
        t.touches |= {a for a in (r["touches"] or "").split(",") if a}
        if r["session_id"] not in t.sessions:
            t.sessions.append(r["session_id"])

    for t in order:
        judge = None
        if t.root in judged:
            judge = judged[t.root]
        for s in t.sessions:
            t.events += failed.get(s, []) + defects.get(s, [])
            t.has_change = t.has_change or s in with_change
            if s in merged:
                t.events.append(Event(merged[s], "merge"))
            if judge is None and s in judged:
                judge = judged[s]
            for k, v in usage.get(s, {}).items():
                t.tokens[k] = t.tokens.get(k, 0) + v
            st = starts.get(s)
            if st and not t.project:
                t.project = st["project"]
                try:
                    t.done_items = json.loads(st["done"] or "[]")
                except json.JSONDecodeError:
                    t.done_items = []
        if judge is not None:
            t.judge, t.d = judge, judge["d"]
        if t.tokens:
            p, known = book.price_of(t.key)
            if p is not None:
                t.cost, t.price_known = PriceBook.cost(p, t.tokens), known
        t.events.sort(key=lambda e: e.at)

    snapshot_time = q("SELECT MAX(ended_at) m FROM worker_runs")[0]["m"]
    ev_max = q("SELECT MAX(at) m FROM delivery_evidence")[0]["m"]
    meta = {"snapshot_time": max(snapshot_time, ev_max), "book": book, "db": db}
    return order, meta
