package gateway

import (
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The gateway side of the quota burn check (decision 0144). It measures the
// workers of each provider, learns the size of a quota window from a quota
// failure, and hands both to the picker with the start's quota reading.

// burnRunRow is one finished worker session: its provider and its run as the
// burn check reads it.
type burnRunRow struct {
	session  string
	provider string
	run      protocol.BurnRun
}

// finishedBurnRuns reads the worker sessions that ended since a time, one row
// per session. A session that was continued has several run rows and one
// token count (the session's total), so the rows merge: the session began at
// its first run and ended at its last. The tokens come from the session record.
func (s *Store) finishedBurnRuns(since time.Time) ([]burnRunRow, error) {
	rows, err := s.db.Query(`
		SELECT r.id, r.session_id, r.harness, r.model, r.started_at, r.ended_at,
			COALESCE(CAST(json_extract(CASE WHEN json_valid(s.worker) THEN s.worker ELSE '{}' END, '$.tokens') AS INTEGER), 0)
		FROM worker_runs r LEFT JOIN sessions s ON s.id = r.session_id
		WHERE r.ended_at >= ? ORDER BY r.ended_at, r.id`, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []burnRunRow
	index := map[string]int{}
	for rows.Next() {
		var id int64
		var session, harness, model string
		var started, ended, tokens int64
		if err := rows.Scan(&id, &session, &harness, &model, &started, &ended, &tokens); err != nil {
			return nil, err
		}
		if tokens <= 0 || ended <= 0 {
			continue
		}
		if started <= 0 || started > ended {
			started = ended
		}
		provider := protocol.QuotaProviderForStart(harness, model)
		if provider == "" {
			continue
		}
		if i, ok := index[session]; ok && session != "" {
			r := &out[i].run
			r.StartedAt = min(r.StartedAt, started)
			r.EndedAt = max(r.EndedAt, ended)
			r.Tokens = max(r.Tokens, tokens)
			out[i].provider = provider
			continue
		}
		if session != "" {
			index[session] = len(out)
		}
		out = append(out, burnRunRow{session: session, provider: provider, run: protocol.BurnRun{StartedAt: started, EndedAt: ended, Tokens: tokens}})
	}
	return out, rows.Err()
}

// quotaBudgets reads the learned budgets of one provider.
func (s *Store) quotaBudgets(provider string) ([]protocol.QuotaBudget, error) {
	rows, err := s.db.Query(`SELECT window_seconds, tokens FROM quota_budgets WHERE provider = ?`, provider)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []protocol.QuotaBudget
	for rows.Next() {
		var b protocol.QuotaBudget
		if err := rows.Scan(&b.WindowSeconds, &b.Tokens); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// learnQuotaBudget reads a quota failure of a provider: the tokens that its
// workers used in the window before it are the size of the window. The newest
// failure of a window length replaces the older one. A failure with no token
// to read teaches nothing.
func (s *Store) learnQuotaBudget(provider string, failedAt, until time.Time) error {
	rows, err := s.finishedBurnRuns(failedAt.Add(-protocol.QuotaBurnLookback()))
	if err != nil {
		return err
	}
	var runs []protocol.BurnRun
	for _, r := range rows {
		if r.provider == provider {
			runs = append(runs, r.run)
		}
	}
	budget, ok := protocol.LearnQuotaBudget(runs, failedAt, until)
	if !ok {
		return nil
	}
	_, err = s.db.Exec(`INSERT INTO quota_budgets (provider, window_seconds, tokens, at) VALUES (?, ?, ?, ?)
		ON CONFLICT (provider, window_seconds) DO UPDATE SET tokens = excluded.tokens, at = excluded.at`,
		provider, budget.WindowSeconds, budget.Tokens, failedAt.Unix())
	return err
}

// withQuotaBurn adds the measured burn of each provider to a start's quota
// reading. Only a provider that has a worker running now can be held, so the
// store is read only when one does. The reading itself is not changed: the
// result is a copy.
func (g *Gateway) withQuotaBurn(q *protocol.Quotas, agents []protocol.AgentView) *protocol.Quotas {
	now := g.store.now()
	running := map[string][]protocol.BurnRun{}
	runningSessions := map[string]bool{}
	for _, a := range agents {
		if a.Worker == nil {
			continue
		}
		provider := protocol.QuotaProviderForStart(a.Harness, a.Model)
		if provider == "" {
			continue
		}
		running[provider] = append(running[provider], protocol.BurnRun{StartedAt: a.Worker.StartedAt, Tokens: a.Worker.Tokens})
		runningSessions[a.SessionID] = true
	}
	if len(running) == 0 {
		return q
	}
	finished, err := g.store.finishedBurnRuns(now.Add(-protocol.QuotaBurnLookback()))
	if err != nil {
		g.logLine("quota.burn_error", "error", err)
		return q
	}
	set := &protocol.QuotaBurnSet{Now: now.Unix(), ByProvider: map[string]protocol.QuotaBurn{}}
	for provider, runs := range running {
		burn := protocol.QuotaBurn{}
		// A running session that ran before has a finished row too: the
		// running count replaces it, so its tokens count once.
		for _, r := range finished {
			if r.provider == provider && !runningSessions[r.session] {
				burn.Runs = append(burn.Runs, r.run)
			}
		}
		burn.Runs = append(burn.Runs, runs...)
		if burn.Budgets, err = g.store.quotaBudgets(provider); err != nil {
			g.logLine("quota.burn_error", "error", err)
			return q
		}
		set.ByProvider[provider] = burn
	}
	out := &protocol.Quotas{}
	if q != nil {
		*out = *q
	}
	out.Burn = set
	return out
}
