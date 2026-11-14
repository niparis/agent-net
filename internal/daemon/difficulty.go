package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/difficulty"
	"github.com/niparis/agent-net/internal/protocol"
)

// Task difficulty from evidence (docs/design/task-difficulty.md sections 2, 4,
// 5 and 7). assessDifficulty runs a short judge and turns its answer into a
// label. Worker starts run the judge asynchronously against the gateway's
// fixed judge list (decision 0162).
//
// The judge runs a registered normal-level worker profile in an empty folder
// and gets only the task brief. A failure, timeout or insufficient answer
// returns "normal" with the reason in Notes: the start must go ahead.

const defaultDifficultyTimeout = 3 * time.Minute

// difficultyLookupTimeout bounds the roster and score reads, so a slow gateway
// cannot hold a worker start past the run limits.
const difficultyLookupTimeout = 10 * time.Second

// assessDifficulty analyzes task and has a judge score the evidence. It is the
// daemon's call: the same work runs outside a daemon through
// NewDifficultyAssessor (see difficulty_assessor.go).
func (d *Daemon) assessDifficulty(ctx context.Context, project, task string) (difficulty.Assessment, error) {
	return NewDifficultyAssessor(d.cfg, DifficultySources{
		PickJudge: d.pickDifficultyJudge,
		Binary:    d.harnessBinary,
		Logf:      d.safeLogf,
	}).Assess(ctx, project, task)
}

func (d *Daemon) safeLogf(format string, args ...any) {
	if d != nil && d.log != nil {
		d.Logf(format, args...)
	}
}

// pickDifficultyJudge asks the gateway to use the same picker as worker starts.
func (d *Daemon) pickDifficultyJudge(ctx context.Context, exclude string) (protocol.DifficultyJudgeChoice, error) {
	if d.cfg.testJudgePick != nil {
		return d.cfg.testJudgePick(ctx, exclude)
	}
	var out protocol.DifficultyJudgeChoice
	raw, err := d.call(ctx, "worker.judge.pick", protocol.DifficultyJudgePick{ExcludeProvider: exclude})
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(raw, &out)
	return out, err
}

func judgeBrief(profile difficulty.Profile) string {
	raw, _ := json.Marshal(profile)
	return "AGENT_NET_DIFFICULTY_KIND=judge\n" +
		"[difficulty judge] Score the evidence profile. Do not inspect a repository.\n\n" +
		"Rubric:\n" + difficulty.RubricText() + "\n\n" +
		"Evidence profile:\n" + string(raw) + "\n\n" +
		"Score each dimension 1 to 5 with one line of reason, as JSON; or INSUFFICIENT_EVIDENCE with the dimensions and what to inspect.\n" +
		`Reply like {"reasoning":3,"scope":3,"coupling":3,"uncertainty":3,"verification":3,"risk":3,"reasons":{"reasoning":"...","scope":"...","coupling":"...","uncertainty":"...","verification":"...","risk":"..."}}.` + "\n"
}

func judgeBriefForTask(task string) string {
	return "AGENT_NET_DIFFICULTY_KIND=judge\n" + difficulty.RubricText() + `

Score each dimension 1 to 5 from the facts in this task brief (its Evidence section if present, otherwise the brief text). Start every dimension at 1 and raise it only for a concrete reason you name. Reply with JSON only, in exactly this shape: {"reasoning":2,"scope":1,"coupling":1,"uncertainty":1,"verification":2,"risk":1,"reasons":{"reasoning":"...","scope":"...","coupling":"...","uncertainty":"...","verification":"...","risk":"..."},"confidence":"high"}. Each score is a whole number from 1 to 5; each reason is one line; confidence is low, medium, or high. Do not use INSUFFICIENT_EVIDENCE. A missing fact means score from what is there and lower confidence.

Task brief:
` + judgeTaskText(task)
}

// judgeTaskLimit caps the brief the judge reads. The judge's time grows with
// the brief: the briefs it failed on were 5 to 8 KB (2026-11-04). The goal at
// the top and the Evidence section carry what the scores need.
const judgeTaskLimit = 4000

// judgeEvidenceLimit caps the Evidence section that a trimmed brief keeps.
const judgeEvidenceLimit = 2000

// judgeTailLimit is how much of the end of a long brief is kept.
const judgeTailLimit = 400

var evidenceHeading = regexp.MustCompile(`(?im)^[ \t#*]*evidence\b`)

// judgeTaskText returns task, or for a brief over judgeTaskLimit its head, its
// Evidence section and its tail, with a marker where text was cut. Cuts fall
// on line ends.
func judgeTaskText(task string) string {
	task = strings.TrimSpace(task)
	if len(task) <= judgeTaskLimit {
		return task
	}
	tail := task[len(task)-judgeTailLimit:]
	if i := strings.IndexByte(tail, '\n'); i >= 0 {
		tail = tail[i+1:]
	}
	tailStart := len(task) - len(tail)
	// The Evidence section runs from its heading to the first blank line.
	evStart, evEnd := -1, -1
	if loc := evidenceHeading.FindStringIndex(task[:tailStart]); loc != nil {
		evStart, evEnd = loc[0], tailStart
		if i := strings.Index(task[evStart:tailStart], "\n\n"); i >= 0 {
			evEnd = evStart + i
		}
		evEnd = evStart + len(cutAtLine(task[evStart:evEnd], judgeEvidenceLimit))
	}
	head := cutAtLine(task, judgeTaskLimit-max(evEnd-evStart, 0)-len(tail))
	var parts []string
	keptEnd := len(head)
	switch {
	case evStart < 0:
		parts = []string{head}
	case evStart < len(head):
		// The section starts in the head: the head runs to its end.
		keptEnd = max(len(head), evEnd)
		parts = []string{task[:keptEnd]}
	default:
		parts = []string{head, cutMarker(evStart - len(head)), task[evStart:evEnd]}
		keptEnd = evEnd
	}
	parts = append(parts, cutMarker(tailStart-keptEnd), tail)
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return strings.Join(parts, "\n\n")
}

func cutMarker(n int) string {
	return fmt.Sprintf("[... %d bytes of the brief cut ...]", n)
}

// cutAtLine returns the first n bytes of s, shortened to the last line end.
func cutAtLine(s string, n int) string {
	if n >= len(s) {
		return s
	}
	if n <= 0 {
		return ""
	}
	if i := strings.LastIndexByte(s[:n], '\n'); i > 0 {
		return s[:i]
	}
	return s[:n]
}

// capWriter keeps the first limit bytes and counts the rest.
type capWriter struct {
	b     strings.Builder
	limit int
	n     int
}

func (w *capWriter) Write(p []byte) (int, error) {
	if w.n < w.limit {
		room := w.limit - w.n
		if len(p) < room {
			room = len(p)
		}
		w.b.Write(p[:room])
	}
	w.n += len(p)
	return len(p), nil
}

func (w *capWriter) String() string { return w.b.String() }
