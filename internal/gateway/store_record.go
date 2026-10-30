package gateway

import (
	"math"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// ---- projects ----

// Projects lists the known projects, one row per machine and folder: the
// registered ones (see store_projects.go) merged with the project folders of
// the stored sessions. Source says which of the two a row comes from.
func (s *Store) Projects(name string) ([]protocol.ProjectView, error) {
	seen, err := s.seenProjects(name)
	if err != nil {
		return nil, err
	}
	reg, err := s.registeredProjects(name)
	if err != nil {
		return nil, err
	}
	type key struct{ node, path string }
	byKey := map[key]int{}
	out := make([]protocol.ProjectView, 0, len(seen)+len(reg))
	for _, p := range seen {
		p.Source = protocol.SourceSeen
		byKey[key{p.NodeID, p.Path}] = len(out)
		out = append(out, p)
	}
	for _, r := range reg {
		i, ok := byKey[key{r.NodeID, r.Path}]
		if !ok {
			out = append(out, r)
			continue
		}
		p := &out[i]
		p.Source, p.Name = protocol.SourceBoth, r.Name
		p.DefaultBranch, p.AddedBy, p.AddedAt, p.CheckedAt = r.DefaultBranch, r.AddedBy, r.AddedAt, r.CheckedAt
		if p.Remote == "" {
			p.Remote = r.Remote
		}
	}
	for i := range out {
		out[i].Permissions, err = s.WorkerPermissions(out[i].NodeID, out[i].Name)
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if !strings.EqualFold(a.Name, b.Name) {
			return strings.ToLower(a.Name) < strings.ToLower(b.Name)
		}
		if a.Hostname != b.Hostname {
			return a.Hostname < b.Hostname
		}
		return a.Path < b.Path
	})
	return out, nil
}

// seenProjects lists the project folders of the stored sessions, one row per
// machine and folder. The folder is the repository root, or the session's
// working folder when it is not in a repository. Sessions from daemons that
// send no repository info are grouped by working folder and named after it.
// name, when set, keeps only projects with that name (case-insensitive).
func (s *Store) seenProjects(name string) ([]protocol.ProjectView, error) {
	rows, err := s.db.Query(`SELECT s.node_id, n.hostname, s.harness, s.cwd, s.online, s.last_seen,
		s.repo_root, s.repo_project, s.repo_remote, s.repo_worktree
		FROM sessions s JOIN nodes n ON n.id = s.node_id WHERE s.harness != 'owner' ORDER BY s.last_seen DESC, s.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type key struct{ node, path string }
	byKey := map[key]*protocol.ProjectView{}
	harness := map[key]map[string]bool{}
	var order []key
	for rows.Next() {
		var node, host, h, cwd, root, project, remote, worktree string
		var online int
		var seen int64
		if err := rows.Scan(&node, &host, &h, &cwd, &online, &seen, &root, &project, &remote, &worktree); err != nil {
			return nil, err
		}
		path := root
		if path == "" {
			path = cwd
		}
		if path == "" {
			continue
		}
		if project == "" {
			project = filepath.Base(path)
		}
		if name != "" && !strings.EqualFold(project, name) {
			continue
		}
		k := key{node, path}
		p := byKey[k]
		if p == nil {
			// Rows come newest first, so the first one has the newest remote.
			p = &protocol.ProjectView{Name: project, NodeID: node, Hostname: host, Path: path, Remote: remote,
				LastSeen: seen / int64(time.Second)}
			byKey[k] = p
			harness[k] = map[string]bool{}
			order = append(order, k)
		}
		if p.Remote == "" {
			p.Remote = remote
		}
		if worktree != "" && worktree != path && !contains(p.Worktrees, worktree) {
			p.Worktrees = append(p.Worktrees, worktree)
		}
		p.Sessions++
		if online == 1 {
			p.Online++
		}
		harness[k][h] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]protocol.ProjectView, 0, len(order))
	for _, k := range order {
		p := byKey[k]
		for h := range harness[k] {
			p.Harnesses = append(p.Harnesses, h)
		}
		sort.Strings(p.Harnesses)
		sort.Strings(p.Worktrees)
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if !strings.EqualFold(a.Name, b.Name) {
			return strings.ToLower(a.Name) < strings.ToLower(b.Name)
		}
		if a.Hostname != b.Hostname {
			return a.Hostname < b.Hostname
		}
		return a.Path < b.Path
	})
	return out, nil
}

// ---- events ----

// EventFilter selects events. Empty fields match everything.
type EventFilter struct {
	// Node is a node ID or a host name. It matches events of the node, of its
	// sessions and of messages to or from them.
	Node string
	// Session matches events of the session and of messages to or from it.
	Session string
	// Kind matches exactly, or as a prefix up to a dot: "msg" matches
	// "msg.stored" and "msg.failed".
	Kind string
	// Since keeps events at or after this time.
	Since time.Time
	// AfterID keeps events with a larger ID. Results are then oldest first and
	// the first Limit are returned (this is what follow mode uses).
	AfterID int64
	// Limit is the number of events. Default 100, at most 1000. Without
	// AfterID the newest Limit events are returned.
	Limit int
}

// QueryEvents returns audit records, oldest first.
func (s *Store) QueryEvents(f EventFilter) ([]protocol.Event, error) {
	if f.Limit <= 0 {
		f.Limit = 100
	}
	if f.Limit > 1000 {
		f.Limit = 1000
	}
	where := []string{"1=1"}
	var args []any
	if f.Node != "" {
		ids, err := s.nodeIDs(f.Node)
		if err != nil {
			return nil, err
		}
		if len(ids) == 0 {
			return []protocol.Event{}, nil
		}
		in := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
		var a []any
		for _, id := range ids {
			a = append(a, id)
		}
		clause := `(e.node_id IN (` + in + `)
			OR e.session_id IN (SELECT id FROM sessions WHERE node_id IN (` + in + `))
			OR e.message_id IN (SELECT m.id FROM messages m WHERE
				m.sender IN (SELECT id FROM sessions WHERE node_id IN (` + in + `))
				OR m.recipient IN (SELECT id FROM sessions WHERE node_id IN (` + in + `))))`
		where = append(where, clause)
		for i := 0; i < 4; i++ {
			args = append(args, a...)
		}
	}
	if f.Session != "" {
		where = append(where, `(e.session_id = ? OR e.message_id IN (SELECT id FROM messages WHERE sender = ? OR recipient = ?))`)
		args = append(args, f.Session, f.Session, f.Session)
	}
	if f.Kind != "" {
		where = append(where, `(e.kind = ? OR substr(e.kind, 1, ?) = ?)`)
		args = append(args, f.Kind, len(f.Kind)+1, f.Kind+".")
	}
	if !f.Since.IsZero() {
		// UnixNano overflows outside the years 1678 to 2262.
		ns := f.Since.UnixNano()
		switch {
		case f.Since.Year() > 2200:
			ns = math.MaxInt64
		case f.Since.Year() < 1700:
			ns = 0
		}
		where = append(where, `e.at >= ?`)
		args = append(args, ns)
	}
	order := "DESC"
	if f.AfterID > 0 {
		where = append(where, `e.id > ?`)
		args = append(args, f.AfterID)
		order = "ASC"
	}
	args = append(args, f.Limit)
	rows, err := s.db.Query(`SELECT e.id, e.at, e.kind, e.node_id, COALESCE(n.hostname,''), e.session_id, e.message_id, e.detail
		FROM events e LEFT JOIN nodes n ON n.id = e.node_id
		WHERE `+strings.Join(where, " AND ")+` ORDER BY e.id `+order+` LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.Event{}
	for rows.Next() {
		var e protocol.Event
		var at int64
		if err := rows.Scan(&e.ID, &at, &e.Kind, &e.NodeID, &e.Hostname, &e.SessionID, &e.MessageID, &e.Detail); err != nil {
			return nil, err
		}
		e.Time = time.Unix(0, at).UTC()
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if order == "DESC" {
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	}
	return out, nil
}

// Feed limits.
const (
	DefaultFeedLimit = 100
	MaxFeedLimit     = 500
	maxFeedDetail    = 200
)

// feedKinds are the event kinds any node may read, as prefixes up to a dot.
// MCP events and enrollment events are left out.
var feedKinds = []string{"msg", "session", "node", "worker"}

// FeedEvents returns the events any node may read, oldest first. It is the
// admin events table without bodies, MCP records and addresses: messages are
// named by ID, both ends and type only. With afterID 0 it returns the newest
// limit events; otherwise the first limit events after afterID.
func (s *Store) FeedEvents(afterID int64, limit int) ([]protocol.FeedEvent, error) {
	if limit <= 0 {
		limit = DefaultFeedLimit
	}
	if limit > MaxFeedLimit {
		limit = MaxFeedLimit
	}
	var kinds []string
	var args []any
	for _, k := range feedKinds {
		kinds = append(kinds, `substr(e.kind, 1, ?) = ?`)
		args = append(args, len(k)+1, k+".")
	}
	where := `(` + strings.Join(kinds, " OR ") + `)`
	order := "DESC"
	if afterID > 0 {
		where += ` AND e.id > ?`
		args = append(args, afterID)
		order = "ASC"
	}
	args = append(args, limit)
	rows, err := s.db.Query(`SELECT e.id, e.at, e.kind, e.node_id, COALESCE(n.hostname,''), e.session_id, e.message_id, e.detail,
		COALESCE(m.sender,''), COALESCE(m.recipient,''), COALESCE(m.type,''), COALESCE(m.parent_id,'')
		FROM events e LEFT JOIN nodes n ON n.id = e.node_id LEFT JOIN messages m ON m.id = e.message_id AND e.message_id != ''
		WHERE `+where+` ORDER BY e.id `+order+` LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.FeedEvent{}
	for rows.Next() {
		var e protocol.FeedEvent
		var at int64
		if err := rows.Scan(&e.ID, &at, &e.Kind, &e.NodeID, &e.Hostname, &e.SessionID, &e.MessageID, &e.Detail,
			&e.From, &e.To, &e.Type, &e.Parent); err != nil {
			return nil, err
		}
		e.Time = time.Unix(0, at).UTC()
		if e.Kind == "node.connected" {
			e.Detail = "" // the address of the machine
		}
		if len(e.Detail) > maxFeedDetail {
			e.Detail = strings.ToValidUTF8(e.Detail[:maxFeedDetail], "")
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if order == "DESC" {
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	}
	return out, nil
}

// nodeIDs resolves a node ID or host name to node IDs.
func (s *Store) nodeIDs(idOrHost string) ([]string, error) {
	rows, err := s.db.Query(`SELECT id FROM nodes WHERE id = ? OR hostname = ?`, idOrHost, idOrHost)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ---- retention ----

// Default retention periods. A period of 0 keeps the data for ever.
const (
	DefaultKeepMessages = 30 * 24 * time.Hour
	DefaultKeepEvents   = 90 * 24 * time.Hour
	DefaultKeepSessions = 30 * 24 * time.Hour
)

// Retention says how long the gateway keeps data. A zero period keeps it for ever.
type Retention struct {
	// Messages covers message rows (with their bodies) and delivery rows.
	Messages time.Duration
	// Events covers the audit events.
	Events time.Duration
	// Sessions covers sessions that are offline: they go when they were last
	// seen longer ago than this.
	Sessions time.Duration
}

// DefaultRetention is what `agent-net gateway` uses.
func DefaultRetention() Retention {
	return Retention{Messages: DefaultKeepMessages, Events: DefaultKeepEvents, Sessions: DefaultKeepSessions}
}

// PruneResult counts the rows a Prune deleted.
type PruneResult struct {
	Messages, Deliveries, Events, Sessions int
}

func (r PruneResult) Any() bool {
	return r.Messages+r.Deliveries+r.Events+r.Sessions > 0
}

// Prune deletes data older than the retention periods. It records a
// "retention" event when it deleted anything.
func (s *Store) Prune(r Retention) (PruneResult, error) {
	var res PruneResult
	now := s.now()
	del := func(dst *int, query string, args ...any) error {
		out, err := s.db.Exec(query, args...)
		if err != nil {
			return err
		}
		n, _ := out.RowsAffected()
		*dst += int(n)
		return nil
	}
	if r.Messages > 0 {
		cut := now.Add(-r.Messages).UnixNano()
		if err := del(&res.Deliveries, `DELETE FROM deliveries WHERE at < ? OR message_id IN (SELECT id FROM messages WHERE created_at < ?)`, cut, cut); err != nil {
			return res, err
		}
		if err := del(&res.Messages, `DELETE FROM messages WHERE created_at < ? AND NOT EXISTS (SELECT 1 FROM graph_outcomes o JOIN messages req ON req.id=o.message_id JOIN sessions src ON src.id=req.sender WHERE o.resolution='' AND o.outcome IN ('reject','escalate') AND src.harness<>'owner' AND (messages.id=o.message_id OR messages.id=o.reply_id))`, cut); err != nil {
			return res, err
		}
	}
	if r.Sessions > 0 {
		cut := now.Add(-r.Sessions).UnixNano()
		if err := del(new(int), `DELETE FROM profiles WHERE session_id IN (SELECT id FROM sessions WHERE online = 0 AND last_seen < ? AND NOT EXISTS (SELECT 1 FROM graph_outcomes o JOIN messages req ON req.id=o.message_id JOIN sessions src ON src.id=req.sender WHERE o.resolution='' AND o.outcome IN ('reject','escalate') AND src.harness<>'owner' AND (sessions.id=req.sender OR sessions.id=req.recipient)))`, cut); err != nil {
			return res, err
		}
		if err := del(&res.Sessions, `DELETE FROM sessions WHERE online = 0 AND last_seen < ? AND NOT EXISTS (SELECT 1 FROM graph_outcomes o JOIN messages req ON req.id=o.message_id JOIN sessions src ON src.id=req.sender WHERE o.resolution='' AND o.outcome IN ('reject','escalate') AND src.harness<>'owner' AND (sessions.id=req.sender OR sessions.id=req.recipient))`, cut); err != nil {
			return res, err
		}
	}
	if r.Events > 0 {
		if err := del(&res.Events, `DELETE FROM events WHERE at < ?`, now.Add(-r.Events).UnixNano()); err != nil {
			return res, err
		}
	}
	if res.Any() {
		detail := strings.TrimPrefix(pairs("messages", res.Messages, "deliveries", res.Deliveries,
			"events", res.Events, "sessions", res.Sessions), " ")
		s.Event("retention", "", "", "", detail)
		s.logf("retention", "messages", res.Messages, "deliveries", res.Deliveries,
			"events", res.Events, "sessions", res.Sessions)
	}
	return res, nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// ModelUsage counts the sessions of a harness on a node that ran each model
// and were seen within the last window. A resolved model name prefers the one
// the machine uses most (protocol.ResolveModel).
func (s *Store) ModelUsage(nodeID, harness string, window time.Duration) (map[string]int, error) {
	rows, err := s.db.Query(`SELECT model, COUNT(*) FROM sessions
		WHERE node_id = ? AND harness = ? AND model != '' AND last_seen >= ? GROUP BY model`,
		nodeID, harness, s.now().Add(-window).UnixNano())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var m string
		var n int
		if err := rows.Scan(&m, &n); err != nil {
			return nil, err
		}
		out[m] = n
	}
	return out, rows.Err()
}

// RecordModelFailure notes that a worker on a node failed on a model call
// (provider, billing or auth error). ResolveModel ranks the model last for
// protocol.ModelFailureHours.
func (s *Store) RecordModelFailure(nodeID, harness, model string) error {
	if model == "" {
		return nil
	}
	_, err := s.db.Exec(`INSERT INTO model_failures (node_id, harness, model, at) VALUES (?, ?, ?, ?)
		ON CONFLICT (node_id, harness, model) DO UPDATE SET at = excluded.at`,
		nodeID, harness, model, s.now().UnixNano())
	return err
}

// FailedModels returns the models of a harness on a node that failed a worker
// within protocol.ModelFailureHours, and when each last failed.
func (s *Store) FailedModels(nodeID, harness string) (map[string]time.Time, error) {
	rows, err := s.db.Query(`SELECT model, at FROM model_failures WHERE node_id = ? AND harness = ? AND at >= ?`,
		nodeID, harness, s.now().Add(-protocol.ModelFailureHours*time.Hour).UnixNano())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]time.Time{}
	for rows.Next() {
		var m string
		var at int64
		if err := rows.Scan(&m, &at); err != nil {
			return nil, err
		}
		out[m] = time.Unix(0, at)
	}
	return out, rows.Err()
}

// failedSet is the model IDs of a FailedModels result, as ResolveModelFailed
// takes them.
func failedSet(m map[string]time.Time) map[string]bool {
	out := make(map[string]bool, len(m))
	for k := range m {
		out[k] = true
	}
	return out
}
