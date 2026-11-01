package gateway

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The optional owner-provided snapshot remains read-only. Opening the copy
// runs the real migrations and one-time backfills before reading panel data.
func TestTaskReworkSnapshot(t *testing.T) {
	const dir = "/tmp/agent-net-gwcopy"
	raw, err := os.ReadFile(filepath.Join(dir, "gateway-2026-11-01.db"))
	if os.IsNotExist(err) {
		t.Skip("gateway snapshot is not present")
	}
	if err != nil {
		t.Fatal(err)
	}
	dirCopy := t.TempDir()
	path := filepath.Join(dirCopy, "gateway.db")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	// The independent reference reads the same snapshot before migrations or
	// backfills, so it can use the original stored rework counts.
	referenceDB := filepath.Join(dirCopy, "reference.db")
	if err := os.WriteFile(referenceDB, raw, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// Evaluate the retention window at the snapshot's own time, so this
	// optional check remains useful when the fixture is more than 30 days old.
	var lastRun int64
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(ended_at),0) FROM worker_runs`).Scan(&lastRun); err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return time.Unix(lastRun, 0) }
	if err := s.refreshQuality(); err != nil {
		t.Fatal(err)
	}
	type row struct {
		level, family string
		tasks, mid    int
		headache      float64
	}
	var got []row
	for _, level := range s.QualityAnalysis().Levels {
		for _, f := range level.Families {
			if f.Tasks >= 5 {
				got = append(got, row{level.Difficulty, f.Family, f.Tasks, f.Midrun, f.Headache})
			}
		}
	}
	sort.Slice(got, func(i, j int) bool {
		if got[i].level != got[j].level {
			return got[i].level < got[j].level
		}
		if got[i].tasks != got[j].tasks {
			return got[i].tasks > got[j].tasks
		}
		return got[i].family < got[j].family
	})
	expected, err := os.ReadFile(filepath.Join(dir, "expected.txt"))
	if err != nil {
		t.Fatal(err)
	}
	// The provided replay leaves its last notice set after every request and
	// also reconstructs existing rework from messages. Preserve its linking
	// logic, but use a fresh notice flag per request and the stored run counts.
	cmd := exec.Command("python3", "-c", correctedSnapshotRecount, referenceDB,
		filepath.Join(dir, "link.py"), filepath.Join(dir, "recount.py"))
	corrected, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("corrected snapshot reference: %v: %s", err, corrected)
	}
	re := regexp.MustCompile(`^(\S+)\s+(\S+)\s+tasks\s+(\d+)\s+headache\s+([\d.]+) min\s+\(\+mid-run msgs\s+(\d+),`)
	parse := func(raw []byte) map[string]row {
		rows := map[string]row{}
		for _, line := range strings.Split(string(raw), "\n") {
			m := re.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			n, _ := strconv.Atoi(m[3])
			h, _ := strconv.ParseFloat(m[4], 64)
			mid, _ := strconv.Atoi(m[5])
			rows[m[1]+"/"+m[2]] = row{m[1], m[2], n, mid, h}
		}
		return rows
	}
	provided, want := parse(expected), parse(corrected)
	if len(provided) == 0 || len(want) == 0 {
		t.Fatal("expected snapshot table is empty")
	}
	for _, r := range got {
		t.Logf("%-7s %-9s tasks %3d  headache %4.1f min  midrun %d", r.level, r.family, r.tasks, r.headache, r.mid)
		key := r.level + "/" + r.family
		w, ok := want[key]
		if !ok {
			t.Errorf("unexpected row %s", key)
			continue
		}
		original, ok := provided[key]
		if !ok || original.tasks != w.tasks {
			t.Errorf("%s task count differs from the provided reference", key)
		} else if original.mid != w.mid || fmt.Sprintf("%.1f", original.headache) != fmt.Sprintf("%.1f", w.headache) {
			t.Logf("%s: corrected reference headache %.1f midrun %d; provided %.1f and %d", key, w.headache, w.mid, original.headache, original.mid)
		}
		if r.tasks != w.tasks || r.mid != w.mid || fmt.Sprintf("%.1f", r.headache) != fmt.Sprintf("%.1f", w.headache) {
			t.Errorf("%s got %s; want %s", key, fmt.Sprintf("%d %.1f %d", r.tasks, r.headache, r.mid), fmt.Sprintf("%d %.1f %d", w.tasks, w.headache, w.mid))
		}
		delete(want, key)
		delete(provided, key)
	}
	for key := range want {
		t.Errorf("missing row %s", key)
	}
	for key := range provided {
		t.Errorf("missing provided row %s", key)
	}
}

// correctedSnapshotRecount runs the supplied reference's own brief matching
// and task metadata on an unmodified copy of the snapshot. It changes only
// the replay: notices are cleared by each caller request, and existing
// continue/fix counts come from worker_runs rather than being reconstructed.
// Continuations add one continue after a partial outcome, otherwise one fix.
const correctedSnapshotRecount = `
import collections, sys
source = open(sys.argv[3], encoding="utf-8").read()
marker = "# replay messages with the existing rule, per session"
if marker not in source:
    raise ValueError("reference replay marker not found")
exec(source.split(marker, 1)[0])
cont, fix, mid = collections.Counter(), collections.Counter(), collections.Counter()
for sid, c, f in db.execute("SELECT session_id, SUM(continue_count), SUM(fix_count) FROM worker_runs WHERE session_id<>'' GROUP BY session_id"):
    cont[sid], fix[sid] = c, f
seen, notice = set(), set()
for frm, to, typ, body in db.execute("SELECT sender,recipient,type,body FROM messages ORDER BY created_at,rowid"):
    if to in runs and frm == runs[to]["caller"] and typ == "request":
        if to in seen and to not in notice:
            mid[to] += 1
        seen.add(to)
        notice.discard(to)
    if frm in runs and to == runs[frm]["caller"] and typ in ("reply", "notification") and body.startswith("[agent-net worker notice]\nstatus: "):
        status = body.split("\n")[1][8:]
        if status in ("done", "partial", "failed", "timed out", "idle without hand-back"):
            notice.add(frm)
link_time = {to: at for at, frm, to, task in briefs if task.startswith(HEAD) and to in links}
run_outcomes = collections.defaultdict(list)
for sid, outcome, ended in db.execute("SELECT session_id,outcome,ended_at FROM worker_runs WHERE session_id<>'' ORDER BY id"):
    run_outcomes[sid].append((ended, outcome))
for new, prev in links.items():
    if new not in runs:
        continue
    outcomes = run_outcomes.get(prev, [])
    before = [(at, i, outcome) for i, (at, outcome) in enumerate(outcomes) if at * 1000000000 <= link_time[new]]
    outcome = max(before)[2] if before else (outcomes[-1][1] if outcomes else "")
    if outcome == "partial":
        cont[new] += 1
    else:
        fix[new] += 1
defects = collections.Counter()
for sid, severity in db.execute("SELECT session_id,severity FROM worker_defects"):
    defects[sid] += {1: 10, 2: 30, 3: 60, 4: 180}.get(severity, 0)
tasks = {}
for sid, r in runs.items():
    task = tasks.setdefault(root(sid), dict(c=0, f=0, mid=0, dm=0, stopped_only=True))
    task["c"] += cont[sid]; task["f"] += fix[sid]; task["mid"] += mid[sid]; task["dm"] += defects[sid]
    if any(outcome != "stopped" for outcome in r["outcomes"]):
        task["stopped_only"] = False
table = collections.defaultdict(lambda: [0, 0.0, 0])
for root_id, task in tasks.items():
    r = runs.get(root_id)
    if not r or task["stopped_only"]:
        continue
    row = table[(r["diff"], r["fam"])]
    row[0] += 1
    row[1] += 2 * task["c"] + 10 * task["f"] + task["dm"]
    row[2] += task["mid"]
for (level, family), (n, headache, midrun) in sorted(table.items()):
    if n >= 5:
        print(f"{level:7} {family:9} tasks {n:3}  headache {headache/n:5.1f} min   (+mid-run msgs {midrun}, +0 min)")
`
