package gateway

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The typed Graph cutover is an application upgrade, not an owner choice. The
// gateway runs the same backup-gated transaction as the removed Settings
// action as soon as every enrolled machine reports a typed Graph hello.
const (
	graphCutoverBlockerMarker = "graph-cutover-blockers"
	graphCutoverFailureMarker = "graph-cutover-failure"
	graphCutoverStaleAfter    = 24 * time.Hour
)

// noteGatewayVersion records the first start of a gateway version. A later
// start of the same version keeps the original upgrade time.
func (s *Store) noteGatewayVersion(version string, now time.Time) (time.Time, error) {
	if strings.TrimSpace(version) == "" {
		version = "unknown"
	}
	var stored string
	var at int64
	if err := s.db.QueryRow(`SELECT gateway_version,upgraded_at FROM graph_cutover_state WHERE id=1`).Scan(&stored, &at); err != nil {
		return time.Time{}, err
	}
	if stored != version {
		at = now.UnixNano()
		if _, err := s.db.Exec(`UPDATE graph_cutover_state SET gateway_version=?,upgraded_at=? WHERE id=1`, version, at); err != nil {
			return time.Time{}, err
		}
	}
	return time.Unix(0, at).UTC(), nil
}

func (s *Store) gatewayUpgradedAt() (time.Time, error) {
	var at int64
	if err := s.db.QueryRow(`SELECT upgraded_at FROM graph_cutover_state WHERE id=1`).Scan(&at); err != nil {
		return time.Time{}, err
	}
	if at == 0 {
		return time.Time{}, nil
	}
	return time.Unix(0, at).UTC(), nil
}

// ensureGraphCutoverInbox leaves one open or dismissed notice per marker. A
// dismissed notice stays dismissed; a closed one can be raised again if the
// condition returns.
func (s *Store) ensureGraphCutoverInbox(marker, text string) error {
	var open int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM inbox_items WHERE message_id=? AND status IN ('open','dismissed')`, marker).Scan(&open); err != nil {
		return err
	}
	if open > 0 {
		return nil
	}
	_, err := s.CreateInboxItem(protocol.InboxItem{Kind: "check", SessionID: settingsCutoverActor, MessageID: marker, Text: text})
	return err
}

func (s *Store) closeGraphCutoverInbox(marker string) error {
	_, err := s.db.Exec(`UPDATE inbox_items SET status='closed',answer='resolved',answered_at=? WHERE message_id=? AND status IN ('open','dismissed')`, s.now().UnixNano(), marker)
	return err
}

func graphCutoverBackupPath(dataDir string, now time.Time) string {
	dir, err := filepath.Abs(dataDir)
	if err != nil {
		dir = dataDir
	}
	base := filepath.Join(filepath.Dir(dir), "gateway-before-typed-graph-"+now.Format("20060102-150405"))
	path := base
	for i := 2; ; i++ {
		if _, err := os.Stat(path); err != nil {
			return path
		}
		path = base + "-" + strconv.Itoa(i)
	}
}

// RunGraphCutover checks once at startup and then only watches the blocker
// deadline. A failed enable retries on the next hello, never on this ticker.
func (g *Gateway) RunGraphCutover(ctx context.Context) {
	g.triggerGraphCutover(ctx)
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-g.appsDone:
			return
		case <-ticker.C:
			if err := g.checkGraphCutoverStale(); err != nil {
				g.logLine("error", "where", "graph cutover deadline", "err", err)
			}
		}
	}
}

// triggerGraphCutover coalesces a burst of hellos into one check and one
// pending follow-up, so a retry does not become a tight loop.
func (g *Gateway) triggerGraphCutover(ctx context.Context) {
	if !g.autoCutover {
		return
	}
	g.graphCutoverMu.Lock()
	if g.graphCutoverRunning {
		g.graphCutoverPending = true
		g.graphCutoverMu.Unlock()
		return
	}
	g.graphCutoverRunning = true
	g.graphCutoverMu.Unlock()
	go func() {
		for {
			g.checkGraphCutover(ctx)
			g.graphCutoverMu.Lock()
			if !g.graphCutoverPending {
				g.graphCutoverRunning = false
				g.graphCutoverMu.Unlock()
				return
			}
			g.graphCutoverPending = false
			g.graphCutoverMu.Unlock()
		}
	}()
}

func (g *Gateway) checkGraphCutover(ctx context.Context) {
	g.graphCutoverCheckMu.Lock()
	defer g.graphCutoverCheckMu.Unlock()
	on, err := g.store.graphRefsEnabled()
	if err != nil {
		g.logLine("error", "where", "graph cutover", "err", err)
		return
	}
	if on {
		g.clearGraphCutoverInbox()
		return
	}
	blockers, err := g.graphRefsBlockers(ctx, g.store.db, false)
	if err != nil {
		g.logLine("error", "where", "graph cutover", "err", err)
		return
	}
	if len(blockers) == 0 {
		if err := g.store.closeGraphCutoverInbox(graphCutoverBlockerMarker); err != nil {
			g.logLine("error", "where", "graph cutover inbox", "err", err)
		}
		g.enableGraphRefsAutomatically(ctx)
		return
	}
	if err := g.noteStaleGraphCutover(blockers); err != nil {
		g.logLine("error", "where", "graph cutover deadline", "err", err)
	}
}

func (g *Gateway) checkGraphCutoverStale() error {
	on, err := g.store.graphRefsEnabled()
	if err != nil {
		return err
	}
	if on {
		g.clearGraphCutoverInbox()
		return nil
	}
	blockers, err := g.graphRefsBlockers(context.Background(), g.store.db, false)
	if err != nil {
		return err
	}
	if len(blockers) == 0 {
		return nil
	}
	return g.noteStaleGraphCutover(blockers)
}

func (g *Gateway) noteStaleGraphCutover(blockers []string) error {
	upgraded, err := g.store.gatewayUpgradedAt()
	if err != nil {
		return err
	}
	if upgraded.IsZero() || g.store.now().Sub(upgraded) <= graphCutoverStaleAfter {
		return nil
	}
	text := "Typed Graph cutover is waiting for all machines to upgrade: " + strings.Join(blockers, "; ")
	return g.store.ensureGraphCutoverInbox(graphCutoverBlockerMarker, text)
}

// enableGraphRefsAutomatically is the old owner action without the owner
// call. It keeps the backup, the blocker checks and the transaction recheck.
func (g *Gateway) enableGraphRefsAutomatically(ctx context.Context) {
	st := g.store
	on, err := st.graphRefsEnabled()
	if err != nil {
		g.graphCutoverFailed("", err)
		return
	}
	if on {
		g.clearGraphCutoverInbox()
		return
	}
	blockers, err := g.graphRefsBlockers(ctx, st.db, true)
	if err != nil {
		g.graphCutoverFailed("", err)
		return
	}
	if len(blockers) > 0 {
		if err := g.noteStaleGraphCutover(blockers); err != nil {
			g.logLine("error", "where", "graph cutover deadline", "err", err)
		}
		return
	}
	if _, err = st.graphForRef(nil); err != nil {
		g.graphCutoverFailed("", err)
		return
	}
	backup := graphCutoverBackupPath(g.opts.DataDir, st.now())
	if err = g.Backup(backup); err != nil {
		g.graphCutoverFailed(backup, err)
		return
	}
	// Connections must remain available during the backup. Once it completes,
	// serialize admission and recheck both live links and stored reports.
	g.machineAuthMu.Lock()
	defer g.machineAuthMu.Unlock()
	on, err = st.graphRefsEnabled()
	if err != nil || on {
		if err != nil {
			g.graphCutoverFailed(backup, err)
		}
		return
	}
	blockers, err = g.graphRefsBlockers(ctx, st.db, true)
	if err != nil {
		g.graphCutoverFailed(backup, err)
		return
	}
	if len(blockers) > 0 {
		g.logLine("graph.cutover.deferred", "backup", backup, "blockers", len(blockers))
		return
	}
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		g.graphCutoverFailed(backup, err)
		return
	}
	fail := func(err error) {
		tx.Rollback()
		g.graphCutoverFailed(backup, err)
	}
	blockers, err = g.graphRefsBlockers(ctx, tx, false)
	if err != nil {
		fail(err)
		return
	}
	if len(blockers) > 0 {
		tx.Rollback()
		g.logLine("graph.cutover.deferred", "backup", backup, "blockers", len(blockers))
		return
	}
	if _, err = tx.Exec(`INSERT OR IGNORE INTO agent_charter(agent_id,attr,value,updated_at,updated_by) SELECT c.node,c.attr,c.value,c.updated_at,c.updated_by FROM graph_charter c JOIN graph_nodes n ON n.name=c.node AND n.kind='architect'`); err != nil {
		fail(err)
		return
	}
	if _, err = tx.Exec(`UPDATE graph_history SET target_kind=CASE WHEN node IN (SELECT name FROM graph_nodes WHERE kind='architect') THEN 'agent' ELSE 'repo' END WHERE type='charter' AND target_kind=''`); err != nil {
		fail(err)
		return
	}
	if _, err = tx.Exec(`UPDATE graph_ref_gate SET enabled=1,backup=? WHERE id=1`, backup); err != nil {
		fail(err)
		return
	}
	if err = tx.Commit(); err != nil {
		g.graphCutoverFailed(backup, err)
		return
	}
	st.graphRefsOn.Store(true)
	g.logLine("graph.cutover", "backup", backup, "result", "enabled")
	g.clearGraphCutoverInbox()
}

func (g *Gateway) graphCutoverFailed(backup string, err error) {
	g.logLine("graph.cutover.failed", "backup", backup, "err", err)
	text := fmt.Sprintf("Typed Graph cutover could not run: %v. It will retry when a machine connects or the gateway restarts.", err)
	if backup != "" {
		text = fmt.Sprintf("Typed Graph cutover could not run: %v. The backup stays at %s. It will retry when a machine connects or the gateway restarts.", err, backup)
	}
	if e := g.store.ensureGraphCutoverInbox(graphCutoverFailureMarker, text); e != nil {
		g.logLine("error", "where", "graph cutover inbox", "err", e)
	}
}

func (g *Gateway) clearGraphCutoverInbox() {
	for _, marker := range []string{graphCutoverBlockerMarker, graphCutoverFailureMarker} {
		if err := g.store.closeGraphCutoverInbox(marker); err != nil {
			g.logLine("error", "where", "graph cutover inbox", "err", err)
		}
	}
}
