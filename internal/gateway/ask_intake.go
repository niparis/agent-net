package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/redact"
)

// Ask intake (decision 0114, part 3): the chief of staff finds questions that
// sit inside a session waiting for its user, reads the exact last turn with
// secrets masked, and imports one as an owner ask with its source turn. Only
// the live chief may call it. It never sends input to a session, answers an
// ask, or changes a session's state.

const (
	// askIntakeTurnWait is how long one machine may take to read a turn. A
	// plugin that does not answer gives unavailable instead of failing the list.
	askIntakeTurnWait = 12 * time.Second
	// askIntakeListBudget bounds the whole list, however many sessions it reads.
	askIntakeListBudget = 30 * time.Second
	// askIntakeFanOut is how many session reads run at once.
	askIntakeFanOut = 8
)

// askIntakeCandidate is one row of the session roster the intake reads.
type askIntakeCandidate struct {
	id, name, harness, machine, nodeID, waiting string
}

// askIntake serves FrameAskIntake. It checks that the caller is the live chief
// on the node that sent the frame before it reads anything.
func (g *Gateway) askIntake(ctx context.Context, nc *nodeConn, q protocol.AskIntakeRequest) (protocol.AskIntakeResult, error) {
	if err := q.Validate(); err != nil {
		return protocol.AskIntakeResult{}, reject("%v", err)
	}
	chief, err := liveChief(g.store.db)
	if err != nil {
		return protocol.AskIntakeResult{}, err
	}
	if chief == "" || chief != q.SessionID {
		return protocol.AskIntakeResult{}, reject("only the chief of staff may read session turns or import asks")
	}
	var chiefNode string
	if err := g.store.db.QueryRow(`SELECT node_id FROM sessions WHERE id=?`, chief).Scan(&chiefNode); err != nil || chiefNode != nc.id {
		return protocol.AskIntakeResult{}, reject("the chief of staff is not on this machine")
	}
	switch q.Action {
	case protocol.AskIntakeList:
		return g.askIntakeList(ctx, q)
	case protocol.AskIntakeRead:
		return g.askIntakeRead(ctx, q)
	default:
		return g.askIntakeImport(ctx, q)
	}
}

// askIntakeList returns the ended last turn of each candidate session that has
// not been imported yet. A harness with no reader appears with state
// unavailable and the machine's reason; it is never given invented text.
func (g *Gateway) askIntakeList(ctx context.Context, q protocol.AskIntakeRequest) (protocol.AskIntakeResult, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = protocol.AskIntakeDefaultListLimit
	}
	if limit > protocol.AskIntakeMaxListLimit {
		limit = protocol.AskIntakeMaxListLimit
	}
	rows, err := g.store.db.Query(`SELECT s.id,
		COALESCE(NULLIF(p.name,''),NULLIF(s.name,''),''),s.harness,n.hostname,s.node_id,s.waiting
		FROM sessions s JOIN nodes n ON n.id=s.node_id
		LEFT JOIN profiles p ON p.session_id=s.id
		WHERE s.archived_at=0 AND s.harness NOT IN ('owner','cto') AND s.worker=''
		  AND s.id<>COALESCE((SELECT value FROM owner_settings WHERE key='cto_session'),'')
		  AND (s.online=1 OR s.status='idle') AND s.status<>'busy'
		ORDER BY s.last_seen DESC,s.id LIMIT ?`, limit)
	if err != nil {
		return protocol.AskIntakeResult{}, err
	}
	var candidates []askIntakeCandidate
	for rows.Next() {
		var c askIntakeCandidate
		if err := rows.Scan(&c.id, &c.name, &c.harness, &c.machine, &c.nodeID, &c.waiting); err != nil {
			rows.Close()
			return protocol.AskIntakeResult{}, err
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return protocol.AskIntakeResult{}, err
	}
	rows.Close()

	turns := g.askIntakeTurns(ctx, candidates)

	out := protocol.AskIntakeResult{Action: protocol.AskIntakeList, State: protocol.AskIntakeOK, Sessions: []protocol.AskIntakeSession{}}
	for i, c := range candidates {
		turn := turns[i]
		if turn.State != protocol.AskIntakeOK {
			out.Sessions = append(out.Sessions, protocol.AskIntakeSession{Session: c.id, Name: c.name, Harness: c.harness,
				Machine: c.machine, State: protocol.AskIntakeUnavailable, Reason: turn.Reason})
			continue
		}
		if !turn.Ended || turn.Ref == "" {
			// The session is in a turn, or its last entry is not a reply yet.
			continue
		}
		ref := protocol.AskIntakeSourceRef(c.id, turn.Ref)
		imported, err := g.askIntakeImported(ref)
		if err != nil {
			return protocol.AskIntakeResult{}, err
		}
		if imported {
			continue
		}
		row := protocol.AskIntakeSession{Session: c.id, Name: c.name, Harness: c.harness, Machine: c.machine,
			State: protocol.AskIntakeOK, LastTurn: ref, LastTurnAt: turn.At, Waiting: askIntakeWaiting(c.waiting)}
		if row.OpenAsk, err = g.askIntakeOpenAsk(c.id); err != nil {
			return protocol.AskIntakeResult{}, err
		}
		out.Sessions = append(out.Sessions, row)
	}
	return out, nil
}

// askIntakeTurns reads the last turn of every candidate, a few at a time, so a
// slow or unresponsive machine costs one wait and not a whole serial list.
func (g *Gateway) askIntakeTurns(ctx context.Context, candidates []askIntakeCandidate) []protocol.AskIntakeTurnAnswer {
	turns := make([]protocol.AskIntakeTurnAnswer, len(candidates))
	if len(candidates) == 0 {
		return turns
	}
	ctx, cancel := context.WithTimeout(ctx, askIntakeListBudget)
	defer cancel()
	sem := make(chan struct{}, askIntakeFanOut)
	var wg sync.WaitGroup
	for i := range candidates {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			turns[i] = g.askIntakeTurn(ctx, candidates[i])
		}(i)
	}
	wg.Wait()
	return turns
}

// askIntakeTurn asks the daemon that holds the session for its last turn.
func (g *Gateway) askIntakeTurn(ctx context.Context, c askIntakeCandidate) protocol.AskIntakeTurnAnswer {
	unavailable := func(reason string) protocol.AskIntakeTurnAnswer {
		return protocol.AskIntakeTurnAnswer{Session: c.id, State: protocol.AskIntakeUnavailable, Reason: reason}
	}
	nc := g.conn(c.nodeID)
	if nc == nil {
		return unavailable("the machine that holds this session is offline")
	}
	raw, err := nc.request(ctx, protocol.FrameAskIntakeTurn, protocol.AskIntakeTurnRequest{SessionID: c.id}, askIntakeTurnWait)
	if err != nil {
		reason := "the machine did not answer: " + err.Error()
		if strings.Contains(err.Error(), "unknown frame type") {
			reason = "the machine runs an agent-net version that cannot read session turns"
		}
		return unavailable(protocol.CleanWaitingText(redact.Secrets(reason)))
	}
	var turn protocol.AskIntakeTurnAnswer
	if err := json.Unmarshal(raw, &turn); err != nil || turn.Session != c.id {
		return unavailable("the machine gave an unreadable answer")
	}
	if turn.State == "" {
		turn.State = protocol.AskIntakeOK
	}
	return turn
}

// askIntakeRead returns the exact last turn of one session, with the input
// before it and the source of that input.
func (g *Gateway) askIntakeRead(ctx context.Context, q protocol.AskIntakeRequest) (protocol.AskIntakeResult, error) {
	a, err := g.askIntakeTarget(q.Session)
	if err != nil {
		return protocol.AskIntakeResult{}, err
	}
	turn := g.askIntakeTurnOf(ctx, a)
	source := turn.InputSource
	if source == "" && turn.PrecedingInput != "" {
		// A machine on an older version sends the input without its source.
		// It is not the owner's by default.
		source = protocol.AskIntakeUnknown
	}
	out := protocol.AskIntakeResult{Action: protocol.AskIntakeRead, State: turn.State}
	row := protocol.AskIntakeTurn{Session: a.SessionID, State: turn.State, Reason: turn.Reason, At: turn.At,
		Text: turn.Text, PrecedingInput: turn.PrecedingInput, PrecedingInputAt: turn.PrecedingInputAt,
		InputSource: source, InputFrom: turn.InputFrom, InputSessionID: turn.InputSessionID,
		Cut: turn.Cut, Ended: turn.Ended}
	if turn.State == protocol.AskIntakeOK && turn.Ref != "" {
		row.Ref = protocol.AskIntakeSourceRef(a.SessionID, turn.Ref)
	}
	out.Turn = &row
	return out, nil
}

// askIntakeTurnOf reads one session's last turn from its machine.
func (g *Gateway) askIntakeTurnOf(ctx context.Context, a protocol.AgentView) protocol.AskIntakeTurnAnswer {
	nc := g.conn(a.NodeID)
	if nc == nil {
		return protocol.AskIntakeTurnAnswer{Session: a.SessionID, State: protocol.AskIntakeUnavailable, Reason: "the machine that holds this session is offline"}
	}
	raw, err := nc.request(ctx, protocol.FrameAskIntakeTurn, protocol.AskIntakeTurnRequest{SessionID: a.SessionID}, askIntakeTurnWait)
	if err != nil {
		reason := "the machine did not answer: " + err.Error()
		if strings.Contains(err.Error(), "unknown frame type") {
			reason = "the machine runs an agent-net version that cannot read session turns"
		}
		return protocol.AskIntakeTurnAnswer{Session: a.SessionID, State: protocol.AskIntakeUnavailable, Reason: protocol.CleanWaitingText(redact.Secrets(reason))}
	}
	var turn protocol.AskIntakeTurnAnswer
	if err := json.Unmarshal(raw, &turn); err != nil || turn.Session != a.SessionID {
		return protocol.AskIntakeTurnAnswer{Session: a.SessionID, State: protocol.AskIntakeUnavailable, Reason: "the machine gave an unreadable answer"}
	}
	if turn.State == "" {
		turn.State = protocol.AskIntakeOK
	}
	return turn
}

// askIntakeImport creates one owner ask from a turn, or reports the ask that
// already covers it. The turn reference is the source key, so the same turn
// never makes a second ask, even after its first ask closed.
func (g *Gateway) askIntakeImport(ctx context.Context, q protocol.AskIntakeRequest) (protocol.AskIntakeResult, error) {
	a, err := g.askIntakeTarget(q.Session)
	if err != nil {
		return protocol.AskIntakeResult{}, err
	}
	turn := strings.TrimSpace(q.Turn)
	prefix := protocol.AskIntakeSourceRefPrefix + a.SessionID + ":"
	if !strings.HasPrefix(turn, prefix) || len(turn) <= len(prefix) {
		return protocol.AskIntakeResult{}, reject("turn %q does not belong to session %q; use the reference from list or read", q.Turn, a.SessionID)
	}
	out := protocol.AskIntakeResult{Action: protocol.AskIntakeImport, State: protocol.AskIntakeOK, Ask: &protocol.AskIntakeAsk{}}
	if id, status, err := g.askIntakeImportedAsk(turn); err != nil {
		return protocol.AskIntakeResult{}, err
	} else if id != "" {
		out.Ask.ID, out.Ask.State = id, protocol.AskIntakeAlreadyImported
		out.Ask.Detail = "this turn was imported already; that ask is " + status
		return out, nil
	}
	if id, text, err := g.askIntakeExistingAsk(a.SessionID); err != nil {
		return protocol.AskIntakeResult{}, err
	} else if id != "" {
		out.Ask.ID, out.Ask.State = id, protocol.AskIntakeExistingOpen
		out.Ask.Detail = "this session already has an open ask: " + askFirstLine(text, 160)
		return out, nil
	}
	project, _, _, _, err := g.store.sessionIdentity(a.SessionID)
	if err != nil {
		return protocol.AskIntakeResult{}, err
	}
	name := a.Profile.Name
	if name == "" {
		name = a.Title
	}
	if name == "" || strings.HasPrefix(name, "s_") {
		name = "an agent"
	}
	item := protocol.InboxItem{
		Kind:           protocol.AskKindQuestion,
		SessionID:      a.SessionID,
		Node:           project,
		Text:           redact.Secrets(q.Text),
		Recommendation: redact.Secrets(q.Recommendation),
		Options:        redactOptions(q.Options),
		AskerName:      name,
		FromKind:       protocol.AskFromSession,
		AboutKind:      protocol.AskAboutSession,
		AboutID:        a.SessionID,
		SourceRef:      turn,
		Detail:         "Imported by the chief of staff from the last turn of " + name + ".",
	}
	tx, err := g.store.db.Begin()
	if err != nil {
		return protocol.AskIntakeResult{}, err
	}
	defer tx.Rollback()
	item, err = g.store.createInboxItem(tx, item)
	if err != nil {
		// A concurrent import of the same turn hit the open source index.
		// Close this transaction before reading again on the store's handle.
		_ = tx.Rollback()
		if id, status, qerr := g.askIntakeImportedAsk(turn); qerr == nil && id != "" {
			out.Ask.ID, out.Ask.State = id, protocol.AskIntakeAlreadyImported
			out.Ask.Detail = "this turn was imported already; that ask is " + status
			return out, nil
		}
		return protocol.AskIntakeResult{}, err
	}
	if err := tx.QueryRow(`SELECT next_who FROM inbox_items WHERE id=?`, item.ID).Scan(&out.Ask.Next); err != nil {
		return protocol.AskIntakeResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return protocol.AskIntakeResult{}, err
	}
	out.Ask.ID, out.Ask.State = item.ID, protocol.AskIntakeCreated
	return out, nil
}

// askIntakeTarget resolves a session id or exact name and refuses the sessions
// the intake must not read or import: workers, the owner and the CTO.
func (g *Gateway) askIntakeTarget(session string) (protocol.AgentView, error) {
	session = strings.TrimSpace(session)
	a, ok, err := g.store.Agent(session)
	if err != nil {
		return protocol.AgentView{}, err
	}
	if !ok {
		agents, err := g.store.Agents(protocol.QueryAgents{IncludeOwners: true})
		if err != nil {
			return protocol.AgentView{}, err
		}
		var matches []protocol.AgentView
		for _, candidate := range agents {
			if strings.EqualFold(candidate.Profile.Name, session) {
				matches = append(matches, candidate)
			}
		}
		if len(matches) > 1 {
			return protocol.AgentView{}, reject("more than one session is named %q; use its session ID", session)
		}
		if len(matches) == 1 {
			a, ok = matches[0], true
		}
	}
	if !ok {
		return protocol.AgentView{}, reject("unknown session %q; use agent_sessions_find to find its ID", session)
	}
	if a.Worker != nil || a.Harness == "cto" || protocol.IsOwner(a.Harness) {
		return protocol.AgentView{}, reject("session %q is a worker, the owner or the CTO; ask intake reads only agent sessions", a.SessionID)
	}
	return a, nil
}

// askIntakeWaiting turns the stored waiting state into the answer row.
func askIntakeWaiting(raw string) *protocol.AskIntakeWaiting {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var w protocol.Waiting
	if json.Unmarshal([]byte(raw), &w) != nil || strings.TrimSpace(w.Text) == "" {
		return nil
	}
	kind := "harness"
	if w.Explicit {
		kind = "explicit"
	}
	return &protocol.AskIntakeWaiting{Kind: kind, Text: w.Text, Since: w.Since}
}

// askIntakeImported tells whether a source key already has an ask, open or
// closed.
func (g *Gateway) askIntakeImported(ref string) (bool, error) {
	var n int
	err := g.store.db.QueryRow(`SELECT COUNT(*) FROM inbox_items WHERE source_ref=?`, ref).Scan(&n)
	return n > 0, err
}

// askIntakeImportedAsk returns the ask id and status of a source key, or "".
func (g *Gateway) askIntakeImportedAsk(ref string) (string, string, error) {
	var id, status string
	err := g.store.db.QueryRow(`SELECT id,status FROM inbox_items WHERE source_ref=? ORDER BY created_at DESC LIMIT 1`, ref).Scan(&id, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	return id, status, err
}

// askIntakeOpenAsk returns the newest open ask from a session, if any.
func (g *Gateway) askIntakeOpenAsk(session string) (string, error) {
	var id string
	err := g.store.db.QueryRow(`SELECT id FROM inbox_items WHERE session_id=? AND status='open' ORDER BY created_at DESC,id DESC LIMIT 1`, session).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

// askIntakeExistingAsk returns the newest open ask from a session with its
// text, for the import refusal.
func (g *Gateway) askIntakeExistingAsk(session string) (string, string, error) {
	var id, text string
	err := g.store.db.QueryRow(`SELECT id,text FROM inbox_items WHERE session_id=? AND status='open' ORDER BY created_at DESC,id DESC LIMIT 1`, session).Scan(&id, &text)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	return id, text, err
}

// redactOptions masks secrets in each answer the chief proposed.
func redactOptions(options []string) []string {
	if len(options) == 0 {
		return nil
	}
	out := make([]string, len(options))
	for i, option := range options {
		out[i] = redact.Secrets(option)
	}
	return out
}
