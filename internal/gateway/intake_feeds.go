package gateway

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/niparis/agent-net/internal/model"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

// Feeds of the CTO intake queue that the gateway sees itself (decision 0152).

// quotaFeed reads one machine's quota report: a provider with a window used
// up is a quota entry, and a usable reading with room again closes the entry
// and its thread. A provider the report cannot read changes nothing.
func (g *Gateway) quotaFeed(q *protocol.Quotas) {
	if q == nil {
		return
	}
	// One read per report; writes happen only when a provider is out or an
	// open entry can close.
	open, err := g.store.openIntakeSubjects(protocol.IntakeQuota)
	if err != nil {
		if g.logger != nil {
			g.logLine("error", "where", "quota intake", "err", err)
		}
		return
	}
	for _, p := range q.Providers {
		if !quotaReadingUsable(p) {
			continue
		}
		var err error
		if w, out := q.ExhaustedWindow(p.Provider); out {
			text := fmt.Sprintf("%s ran out (%s; %s)", p.Provider, protocol.QuotaWindowName(w), protocol.QuotaResetText(w))
			_, err = g.store.RecordIntake(intakeEvent{Kind: protocol.IntakeQuota, Subject: p.Provider, Text: text})
		} else if open[p.Provider] {
			err = g.store.CloseIntake(protocol.IntakeQuota, p.Provider, intakeResolved)
		}
		if err != nil && g.logger != nil {
			g.logLine("error", "where", "quota intake", "provider", p.Provider, "err", err)
		}
	}
}

// quotaFloorFeed records a start the worker floor refused.
func (g *Gateway) quotaFloorFeed(harness, model, sentence string) {
	provider := protocol.QuotaProviderForStart(harness, model)
	if provider == "" {
		return
	}
	if _, err := g.store.RecordIntake(intakeEvent{Kind: protocol.IntakeQuota, Subject: provider, Text: provider + " reached the worker floor: " + sentence}); err != nil && g.logger != nil {
		g.logLine("error", "where", "quota floor intake", "provider", provider, "err", err)
	}
}

// quotaStuckFeed records a start refused because its provider has no quota
// left: the CTO entry and the chief's stuck-work item, one thread.
func (g *Gateway) quotaStuckFeed(harness, model, reason string) {
	if provider := protocol.QuotaProviderForStart(harness, model); provider != "" {
		g.quotaStuckProvider(provider, reason)
	}
}

// quotaStuckProvider opens or joins the quota thread of one provider.
func (g *Gateway) quotaStuckProvider(provider, reason string) {
	tx, err := g.store.db.Begin()
	if err == nil {
		defer tx.Rollback()
		if _, err = g.store.quotaStuck(tx, provider, reason, g.store.now().UTC()); err == nil {
			err = tx.Commit()
		}
	}
	if err != nil && g.logger != nil {
		g.logLine("error", "where", "quota stuck intake", "provider", provider, "err", err)
	}
}

// struggleFeed runs when a worker run ended with struggle signs. When
// StruggleWorkers distinct workers struggled on the same model within
// StruggleWindow, it is a pattern across workers and goes to the CTO; one
// worker alone stays in its caller's accept task.
func (s *Store) struggleFeed(tx askDB, model string, now time.Time) error {
	var workers int
	if err := tx.QueryRow(`SELECT COUNT(DISTINCT session_id) FROM worker_runs WHERE model=? AND ended_at>=?
		AND struggle_trace NOT IN ('',?) AND (struggle_failed>=? OR struggle_repeats>0)`,
		model, now.Add(-protocol.StruggleWindow).Unix(), protocol.StruggleNoTrace, protocol.StruggleFailedThreshold).Scan(&workers); err != nil {
		return err
	}
	if workers < protocol.StruggleWorkers {
		return nil
	}
	_, err := s.recordIntake(tx, intakeEvent{Kind: protocol.IntakeStruggle, Subject: model,
		Text: fmt.Sprintf("%d workers struggled on %s in the last %s", workers, model, protocol.StruggleWindow)}, now)
	return err
}

// trainMode says whether the project's merger tests one train tip for many
// changes (delivery.ci_mode train).
func (s *Store) trainMode(project string) bool {
	snap, err := s.SettingsSnapshot()
	return err == nil && snap.String("delivery.ci_mode", settings.Where{Repo: project}) == "train"
}

// trainRunFeed is the merge queue detector: MergeQueueFailRuns failed test
// runs in a row on the project's train tip go to the CTO. Every change of a
// train cites the same run, so a run counts once. A pass resets the count and
// closes the entry. A failure the merger marks as a known flake does not count.
func (s *Store) trainRunFeed(tx askDB, project string, e protocol.Evidence, now time.Time) error {
	run := e.Fields[protocol.FieldRun]
	if run == "" {
		run = e.Commit
	}
	switch {
	case e.Result == protocol.StepResultPass:
		if _, err := tx.Exec(`UPDATE merge_train_fails SET fails=0,last_run=?,updated_at=? WHERE project=?`, run, now.UnixNano(), project); err != nil {
			return err
		}
		return s.closeIntake(tx, protocol.IntakeMergeQueue, project, intakePassed, now)
	case e.Result != protocol.StepResultFail || e.Fields[protocol.FieldFlake] == protocol.FieldYes:
		return nil
	}
	var fails int
	var last string
	err := tx.QueryRow(`SELECT fails,last_run FROM merge_train_fails WHERE project=?`, project).Scan(&fails, &last)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if run != "" && run == last && fails > 0 {
		return nil
	}
	fails++
	if _, err := tx.Exec(`INSERT INTO merge_train_fails(project,fails,last_run,updated_at) VALUES(?,?,?,?)
		ON CONFLICT(project) DO UPDATE SET fails=excluded.fails,last_run=excluded.last_run,updated_at=excluded.updated_at`,
		project, fails, run, now.UnixNano()); err != nil {
		return err
	}
	if fails < protocol.MergeQueueFailRuns {
		return nil
	}
	_, err = s.recordIntake(tx, intakeEvent{Kind: protocol.IntakeMergeQueue, Subject: project, Project: project,
		AboutKind: protocol.AskAboutProject, AboutID: project,
		Text:   fmt.Sprintf("the %s merge queue failed %d test runs in a row on its train tip", project, fails),
		Detail: "Last run: " + run}, now)
	return err
}

// noProviderFeed runs when the automatic picker found no worker. When every
// candidate's provider was skipped because it has no quota left, the work is
// stuck because no provider is available: each of those providers' quota
// threads gets it. The gate already made each refusal, so this reads its skips
// instead of asking the quota check a second time (decision 0162).
func (g *Gateway) noProviderFeed(skips []model.Skip) {
	for provider, reason := range quotaBlockedProviders(skips) {
		g.quotaStuckProvider(provider, "no worker could start: "+reason)
	}
}

// quotaBlockedProviders maps each exhausted provider to the gate's reason, or
// returns nil when any candidate was skipped for another check.
func quotaBlockedProviders(skips []model.Skip) map[string]string {
	out := map[string]string{}
	for _, skip := range skips {
		if skip.Check != "exhausted" {
			return nil
		}
		provider := skip.Candidate.QuotaProvider()
		if provider == "" {
			return nil
		}
		out[provider] = skip.Reason
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
