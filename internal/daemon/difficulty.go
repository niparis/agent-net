package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/difficulty"
	"github.com/niparis/agent-net/internal/protocol"
)

// Task difficulty from evidence (docs/design/task-difficulty.md sections 2, 4,
// 5 and 7). assessDifficulty runs two short one-shot models and turns their
// answer into a label. It changes nothing and is not called by a worker start
// yet.
//
// The analyzer runs through `codex exec`, the worker harness that supports a
// read-only sandbox in the same command: -s read-only, -C <folder>, -m <model>,
// and -o <file> for the final message. The independent judge runs through
// `opencode run` in an empty folder and gets only the profile. The analyzer's
// model and harness come from the worker roster unless explicitly configured.
//
// The analyzer runs in the project folder and may read it. The judge runs in an
// empty temporary folder and gets only the profile. Each run has its own time
// limit. Any failure, timeout, low confidence or second insufficient answer
// returns "normal" with the reason in Notes: the start must go ahead.

const defaultDifficultyTimeout = 3 * time.Minute

// difficultyLookupTimeout bounds the roster and score reads, so a slow gateway
// cannot hold a worker start past the run limits.
const difficultyLookupTimeout = 10 * time.Second

// difficultyOneShotHarnesses are the harnesses considered for the repository
// reading analyzer. Codex alone supports the required read-only sandbox.
var difficultyOneShotHarnesses = map[string]bool{"codex": true}

// difficultySchema is the evidence profile of section 4, as the analyzer sees
// it.
const difficultySchema = `{
  "reasoning":    {"closest_pattern": "...", "new_logic": "...", "evidence": ["path:lines"]},
  "scope":        {"components": [...], "distinct_changes": [...], "repeated_changes": [...]},
  "coupling":     {"contracts": [...], "invariants": [...], "evidence": [...]},
  "uncertainty":  {"open_questions": [...], "inspected": [...]},
  "verification": {"tests": [...], "needed_checks": [...], "gaps": [...]},
  "risk":         {"failures": [...], "reversibility": "...", "evidence": [...]},
  "confidence":   "low | medium | high"
}`

// assessDifficulty analyzes task and has a judge score the evidence. It is the
// daemon's call: the same work runs outside a daemon through
// NewDifficultyAssessor (see difficulty_assessor.go).
func (d *Daemon) assessDifficulty(ctx context.Context, project, task string) (difficulty.Assessment, error) {
	return NewDifficultyAssessor(d.cfg, DifficultySources{
		Profiles: d.difficultyProfiles,
		Scores: func(ctx context.Context) ([]protocol.ModelScore, error) {
			return d.difficultyScores(ctx), nil
		},
		Minimums: func(ctx context.Context) (map[string]float64, error) {
			return d.difficultyMinimums(ctx), nil
		},
		Binary: d.harnessBinary,
	}).Assess(ctx, project, task)
}

// difficultyProfiles reads the worker roster through the gateway link. The
// gateway checks the requester, so use a local agent session.
func (d *Daemon) difficultyProfiles(ctx context.Context) ([]protocol.WorkerProfile, error) {
	requester := d.difficultyRequester()
	if requester == "" {
		return nil, errors.New("the worker roster cannot be read without an agent session")
	}
	raw, err := d.call(ctx, "worker.profile.list", protocol.WorkerProfileFrame{Requester: requester})
	if err != nil {
		return nil, fmt.Errorf("the worker roster could not be read: %w", err)
	}
	var profiles []protocol.WorkerProfile
	if err := json.Unmarshal(raw, &profiles); err != nil {
		return nil, fmt.Errorf("the worker roster is not readable: %w", err)
	}
	return profiles, nil
}

func (d *Daemon) difficultyRequester() string {
	if id := d.ownerSessionID(); id != "" {
		return id
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, ls := range d.sessions {
		if ls == nil || ls.sessionID == "" || ls.info.Worker != nil {
			continue
		}
		return ls.sessionID
	}
	return ""
}

// difficultyScores prefers the gateway's effective scores and falls back to the
// bundled table when the network is not reachable.
func (d *Daemon) difficultyScores(ctx context.Context) []protocol.ModelScore {
	if scores, err := d.scores(ctx, nil); err == nil && len(scores) > 0 {
		return scores
	}
	return protocol.EmbeddedModelScores()
}

// difficultyMinimums prefers the owner's cut points and falls back to the
// defaults.
func (d *Daemon) difficultyMinimums(ctx context.Context) map[string]float64 {
	if minimums, err := d.difficulty(ctx, nil); err == nil && minimums != nil {
		return minimums
	}
	return protocol.DifficultyMinimums
}

// cheapestDifficultyProfile picks the profile with the lowest maximum
// difficulty among the harnesses that can run a one-shot. Ties go by model and
// profile name, so the choice is stable.
func cheapestDifficultyProfile(profiles []protocol.WorkerProfile, scores []protocol.ModelScore, minimums map[string]float64) (string, string, bool) {
	levels := []string{"trivial", "simple", "normal", "hard", "very_complex"}
	type candidate struct {
		harness, model, name string
		rank                 int
	}
	var best *candidate
	for _, p := range profiles {
		harness := strings.ToLower(strings.TrimSpace(p.Harness))
		if !difficultyOneShotHarnesses[harness] {
			continue
		}
		model := strings.TrimSpace(p.Model)
		if model == "" {
			continue
		}
		score, ok := protocol.LookupModelScoreWithDefaultEffort(model, p.Effort, scores)
		if !ok {
			continue
		}
		max := protocol.WorkerMaximumLevel(score.Score, minimums)
		rank := -1
		for i, level := range levels {
			if level == max {
				rank = i
				break
			}
		}
		if rank < 0 {
			continue
		}
		c := candidate{harness: harness, model: model, name: p.Name, rank: rank}
		if best == nil || c.rank < best.rank ||
			(c.rank == best.rank && (c.model < best.model || (c.model == best.model && c.name < best.name))) {
			best = &c
		}
	}
	if best == nil {
		return "", "", false
	}
	return best.harness, best.model, true
}

func analyzerBrief(task string) string {
	return "AGENT_NET_DIFFICULTY_KIND=analyzer\n" +
		"[difficulty analyzer] Read the repository in this folder. Report evidence only; do not rate the task.\n\n" +
		"Task:\n" + task + "\n\n" +
		"Rules:\n" + difficulty.AnalyzerRules() + "\n\n" +
		"Reply with a JSON object matching this schema:\n" + difficultySchema + "\n" +
		"Reply with the JSON only.\n"
}

func analysisBrief(task string, dimensions []string) string {
	return "AGENT_NET_DIFFICULTY_KIND=analysis\n" +
		"[difficulty analyzer] The judge needs more evidence. Read the repository in this folder and report evidence only.\n\n" +
		"Task:\n" + task + "\n\n" +
		"Only these dimensions need evidence: " + strings.Join(dimensions, ", ") + ".\n" +
		"Rules:\n" + difficulty.AnalyzerRules() + "\n\n" +
		"Reply with a JSON object with only these keys, each matching its section in this schema:\n" + difficultySchema + "\n" +
		"Reply with the JSON only.\n"
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

// mergeDifficultyProfile replaces the named sections of base with the sections
// the targeted analyzer returned, then validates the whole profile again.
func mergeDifficultyProfile(base difficulty.Profile, patch []byte, dimensions []string) (difficulty.Profile, error) {
	baseRaw, err := json.Marshal(base)
	if err != nil {
		return difficulty.Profile{}, err
	}
	var full map[string]json.RawMessage
	if err := json.Unmarshal(baseRaw, &full); err != nil {
		return difficulty.Profile{}, err
	}
	var sections map[string]json.RawMessage
	if err := json.Unmarshal(patch, &sections); err != nil {
		return difficulty.Profile{}, fmt.Errorf("targeted profile: %w", err)
	}
	for _, dimension := range dimensions {
		value, ok := sections[dimension]
		if !ok {
			return difficulty.Profile{}, fmt.Errorf("targeted profile lacks %q", dimension)
		}
		full[dimension] = value
	}
	merged, err := json.Marshal(full)
	if err != nil {
		return difficulty.Profile{}, err
	}
	return difficulty.ParseProfile(merged)
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
