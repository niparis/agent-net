package gateway

import (
	"sort"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// briefLaunchWindow is how long a harness stays out for a brief it failed to
// launch. A retry of the same brief inside the window leaves that harness out.
const briefLaunchWindow = 24 * time.Hour

// workerBriefLaunchMigration adds two columns to worker_starts. The column
// check in migrate() adds them to stores that already hold the table.
const workerBriefLaunchMigration = `
-- Worker brief launch columns: a hash of the brief, and when a run of the start
-- ended without attempting the task, so a retry of the brief leaves the harness out.
SELECT 1 /* worker brief launch columns */;
`

// trackBriefLaunch remembers which brief a start ran. The harness is already
// on the start row. A start with no brief text has no hash and is not tracked.
func (s *Store) trackBriefLaunch(ref, hash string) error {
	if ref == "" || hash == "" {
		return nil
	}
	_, err := s.db.Exec(`UPDATE worker_starts SET brief_hash=? WHERE ref=?`, hash, ref)
	return err
}

// markBriefNotAttempted notes that the run of a worker session ended without
// attempting its task. A session that no start tracked has nothing to mark.
func (s *Store) markBriefNotAttempted(sessionID string) error {
	if sessionID == "" {
		return nil
	}
	_, err := s.db.Exec(`UPDATE worker_starts SET not_attempted_at=? WHERE session_id=? AND brief_hash<>''`, s.now().Unix(), sessionID)
	return err
}

// briefFailedHarnesses are the harnesses where a start of this brief ended
// without attempting the task, within the window.
func (s *Store) briefFailedHarnesses(hash string) (map[string]bool, error) {
	out := map[string]bool{}
	if hash == "" {
		return out, nil
	}
	rows, err := s.db.Query(`SELECT DISTINCT harness FROM worker_starts WHERE brief_hash=? AND not_attempted_at>=?`, hash, s.now().Add(-briefLaunchWindow).Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		out[h] = true
	}
	return out, rows.Err()
}

// filterBriefFailedHarnesses leaves out every candidate on a harness that did
// not launch this brief, whatever the model. When that would leave nothing, it
// keeps them all and says so: the start never blocks on this rule.
func filterBriefFailedHarnesses(candidates []protocol.WorkerModelCandidate, failed map[string]bool, roster *notStartedRoster) ([]protocol.WorkerModelCandidate, string) {
	if len(failed) == 0 {
		return candidates, ""
	}
	var kept []protocol.WorkerModelCandidate
	left := map[string]bool{}
	for _, c := range candidates {
		if failed[c.Harness] {
			left[c.Harness] = true
		} else {
			kept = append(kept, c)
		}
	}
	if len(left) == 0 {
		return candidates, ""
	}
	names := make([]string, 0, len(left))
	for h := range left {
		names = append(names, h)
	}
	sort.Strings(names)
	if len(kept) == 0 {
		return candidates, "every harness failed to launch this brief (" + strings.Join(names, ", ") + "); running on the best choice anyway"
	}
	if roster != nil {
		for _, c := range candidates {
			if failed[c.Harness] {
				roster.add(rosterName(c.Name, c.Harness, c.Model), "harness "+c.Harness+" failed to launch this brief", rosterFamily)
			}
		}
	}
	return kept, "left out: " + strings.Join(names, ", ") + " (failed to launch this brief)"
}
