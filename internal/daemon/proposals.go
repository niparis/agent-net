package daemon

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

type dialogProposalKey struct{}

// Propose validates and stores a call for the owner's decision.
func (d *Daemon) Propose(ctx context.Context, tool, action string, args json.RawMessage, reason string) (string, error) {
	return d.propose(ctx, tool, action, args, reason, true)
}

// propose stores the proposal. announce sends the open list to the Desk. A
// call the owner's own words approve passes false: it is decided at once and
// must not flash on the Desk.
func (d *Daemon) propose(ctx context.Context, tool, action string, args json.RawMessage, reason string, announce bool) (string, error) {
	if err := d.ctoDispatchMu.lock(ctx); err != nil {
		return "", err
	}
	defer d.ctoDispatchMu.unlock()
	if tool == "session_change" && (action == "merger_start" || action == "merger_stop") {
		var p protocol.SessionStartRequest
		if err := json.Unmarshal(args, &p); err != nil {
			return "", err
		}
		project, node, err := d.mergerProject(ctx, p)
		if err != nil {
			return "", err
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(args, &fields); err != nil {
			return "", err
		}
		fields["project"], _ = json.Marshal(project.Name)
		fields["node_id"], _ = json.Marshal(node)
		args, err = json.Marshal(fields)
		if err != nil {
			return "", err
		}
		if action == "merger_start" {
			chosen := firstNonEmpty(p.Model, p.Harness)
			if chosen == "" {
				// The gate chooses at approval time; the reason names the list's
				// first entry, which is what it will start on unless the
				// checks move it.
				if list, err := protocol.ParseRoleModels(d.SettingsSnapshot().String(protocol.RoleModelsKey(protocol.RoleMerger), settings.Where{})); err == nil && len(list) > 0 {
					chosen = list[0].Display()
				}
			}
			reason = project.Name + " merger: " + chosen + " session in " + project.Name + " on " + project.Hostname + ", owns test and merge."
			raw, err := d.call(ctx, "merger.plan", map[string]string{"project": project.Name})
			if err != nil {
				return "", err
			}
			var plan struct {
				NeedsDefault bool `json:"needs_default"`
			}
			if err := json.Unmarshal(raw, &plan); err != nil {
				return "", err
			}
			if plan.NeedsDefault {
				reason += " Enables the default pipeline."
			}
			reason += " OK?"
		} else {
			reason = project.Name + " merger: clear registration on " + project.Hostname + " and return test and merge to the caller. OK?"
		}
	}
	if (tool == "session_change" && action == "session_start") || (tool == "project_change" && action == "create") {
		var fields map[string]json.RawMessage
		var req protocol.SessionStartRequest
		if err := json.Unmarshal(args, &fields); err != nil {
			return "", err
		}
		if fields == nil {
			return "", errors.New("bad proposal arguments")
		}
		if err := json.Unmarshal(args, &req); err != nil {
			return "", err
		}
		if req.NodeID == "" {
			target := ""
			if req.Path == "" && req.Project != "" {
				var err error
				target, err = d.sessionStartMachine(ctx, req)
				if err != nil {
					return "", err
				}
			}
			if target == "" {
				target = d.cfg.NodeID
			}
			fields["node_id"], _ = json.Marshal(target)
			var err error
			args, err = json.Marshal(fields)
			if err != nil {
				return "", err
			}
		}
	}
	if tool == "sessions_archive" {
		var q protocol.SessionsArchiveRequest
		if err := json.Unmarshal(args, &q); err != nil {
			return "", err
		}
		if q.Restore {
			return "", errors.New("restore is owner-only")
		}
		q.DryRun = true
		raw, err := d.call(ctx, "cto.proposal.validate", map[string]any{"tool": tool, "args": q, "owner": d.ownerSessionID()})
		if err != nil {
			return "", err
		}
		var preview protocol.SessionsArchiveResult
		if err := json.Unmarshal(raw, &preview); err != nil {
			return "", err
		}
		if preview.Count == 0 {
			return "", errors.New("no offline sessions match")
		}
		q.IDs = nil
		reason += fmt.Sprintf("\nArchive %d offline sessions (restorable):", preview.Count)
		for _, a := range preview.Sessions {
			q.IDs = append(q.IDs, a.SessionID)
			reason += "\n" + a.SessionID + " " + a.Name + " (" + a.Folder + ")"
		}
		q.Expected = q.IDs
		q.DryRun = false
		args, err = json.Marshal(q)
		if err != nil {
			return "", err
		}
	}
	id, err := d.proposalRunner().propose(ctx, proposalCall{Tool: tool, Action: action, Args: args}, reason)
	if err == nil {
		if announce {
			d.syncProposalAsks(ctx)
		}
		if dest, ok := ctx.Value(dialogProposalKey{}).(*string); ok {
			*dest = id
		}
	}
	return id, err
}

// ProposalToolResult is the brief response intended for the CTO tool call.
func ProposalToolResult(id string) string {
	return "proposal " + id + " is in the owner's Inbox (tab 7 of the TUI); the owner decides there. You will get a note with the outcome."
}

func (s *state) recordCTODelivery(sender string, at time.Time) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO cto_deliveries(sender,at) VALUES(?,?)`, sender, at.UnixNano()); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM cto_deliveries WHERE id NOT IN (SELECT id FROM cto_deliveries ORDER BY id DESC LIMIT 3)`); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *Daemon) networkProposals(ctx context.Context) ([]protocol.CTOProposal, error) {
	value, _, err := d.proposalOperator(ctx, protocol.OpProposalList, protocol.CTOProposalAnswer{})
	if err != nil {
		return nil, err
	}
	out := value.([]protocol.CTOProposal)
	// Only who is online matters here. The full answer carries the quality
	// tables of every machine and is hundreds of kilobytes; this list is asked
	// for on every refresh of the Desk.
	data, err := d.call(ctx, "query.nodes", protocol.QueryNodes{Brief: true})
	if err != nil {
		return nil, err
	}
	var nodes protocol.NodesInfo
	if err := json.Unmarshal(data, &nodes); err != nil {
		return nil, err
	}
	// The other machines are asked together: one after the other, the list took
	// the sum of their times and one slow machine held up the rest. The first
	// failure in machine order is the one reported, as before.
	type answer struct {
		items []protocol.CTOProposal
		err   error
	}
	var remote []protocol.NodeView
	for _, node := range nodes.Nodes {
		if node.Online && node.NodeID != d.cfg.NodeID {
			remote = append(remote, node)
		}
	}
	answers := make([]answer, len(remote))
	var wg sync.WaitGroup
	for i, node := range remote {
		wg.Add(1)
		go func() {
			defer wg.Done()
			data, err := d.call(ctx, "query.proposal_list", protocol.CTOProposalAnswer{NodeID: node.NodeID})
			if err != nil {
				answers[i].err = fmt.Errorf("list proposals on %s: %w", node.Hostname, err)
				return
			}
			answers[i].err = json.Unmarshal(data, &answers[i].items)
		}()
	}
	wg.Wait()
	for _, a := range answers {
		if a.err != nil {
			return nil, a.err
		}
		out = append(out, a.items...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].NodeID < out[j].NodeID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func (d *Daemon) approveNetworkProposals(ctx context.Context, confirmed []protocol.CTOProposalRef) error {
	items, err := d.networkProposals(ctx)
	if err != nil {
		return err
	}
	if confirmed != nil {
		var open []protocol.CTOProposalRef
		for _, p := range items {
			if p.Status == "open" {
				open = append(open, protocol.CTOProposalRef{NodeID: p.NodeID, ID: p.ID})
			}
		}
		if len(open) != len(confirmed) {
			return errors.New("proposals changed; review them again")
		}
		for i := range open {
			if open[i] != confirmed[i] {
				return errors.New("proposals changed; review them again")
			}
		}
	}
	for _, item := range items {
		if item.Status != "open" {
			continue
		}
		q := protocol.CTOProposalAnswer{NodeID: item.NodeID, ID: item.ID, Action: "a"}
		if item.NodeID == d.cfg.NodeID {
			_, _, err = d.proposalOperator(ctx, protocol.OpProposalAnswer, q)
		} else {
			_, err = d.call(ctx, "query.proposal_answer", q)
			if err == nil {
				d.openApprovedRemoteSession(ctx, q.NodeID, q.ID)
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// An approval handled on another node can start a session after this call
// returns. Follow its start from the owner's node to open the local client.
func (d *Daemon) openApprovedRemoteSession(ctx context.Context, node, id string) {
	data, err := d.call(ctx, "query.proposal_list", protocol.CTOProposalAnswer{NodeID: node})
	if err != nil {
		return
	}
	var items []protocol.CTOProposal
	if json.Unmarshal(data, &items) != nil {
		return
	}
	for _, item := range items {
		if item.ID != id || item.Status != "applied" || !((item.Tool == "session_change" && item.Action == "session_start") || (item.Tool == "project_change" && item.Action == "create")) {
			continue
		}
		var res protocol.StartedSession
		if json.Unmarshal(item.Result, &res) != nil || res.ID == "" || (res.Status != "starting" && res.Status != "started") {
			return
		}
		if res.Background {
			return
		}
		loc, _ := ctx.Value(proposalHerdrLocationKey{}).(proposalHerdrLocation)
		if res.NodeID != "" && res.NodeID != d.cfg.NodeID {
			d.rememberRelayedStart(res.ID, res.NodeID, loc.socket, loc.pane)
			res = d.remoteStartResult(ctx, res, loc.socket, loc.pane)
		}
		if res.Status == "starting" {
			d.watchApprovedSessionStart(ctx, res.ID)
		} else if res.Status == "started" {
			d.notifySessionStart(res)
		}
		return
	}
}

func (d *Daemon) proposalRunner() proposalRunner {
	var logf func(string, ...any)
	if d.log != nil {
		logf = d.log.Printf
	}
	return proposalRunner{state: d.st, now: time.Now, validate: d.validateProposal, run: d.runProposal, log: logf}
}

func (d *Daemon) proposalOperator(ctx context.Context, method string, q protocol.CTOProposalAnswer) (any, bool, error) {
	p := d.proposalRunner()
	if method == protocol.OpProposalAnswer {
		// The ask closes as soon as the proposal is decided.
		defer d.syncProposalAsks(ctx)
		switch q.Action {
		case "a":
			return nil, true, p.decide(ctx, q.ID, true)
		case "x":
			return nil, true, p.decide(ctx, q.ID, false)
		case "A":
			return nil, true, p.approveAll(ctx)
		default:
			return nil, true, errors.New("invalid proposal action")
		}
	}
	items, err := p.list()
	if err != nil {
		return nil, true, err
	}
	out := make([]protocol.CTOProposal, 0, len(items))
	for _, v := range items {
		out = append(out, protocol.CTOProposal{ID: v.ID, NodeID: d.cfg.NodeID, Tool: v.Call.Tool, Action: v.Call.Action, Args: v.Call.Args, Reason: v.Reason, CreatedAt: v.CreatedAt, Status: v.Status, Note: v.Note, Result: v.Result, Messages: v.Messages})
	}
	return out, true, nil
}

// proposalCall is the complete call the owner is asked to approve. The
// validator and runner must interpret its arguments in the same way.
type proposalCall struct {
	Tool   string
	Action string
	Args   json.RawMessage
}

type proposal struct {
	Messages  []protocol.CTOProposalMessage
	ID        string
	Call      proposalCall
	Reason    string
	CreatedAt time.Time
	Status    string
	Note      string
	Result    json.RawMessage
}

// proposalRunner is deliberately independent of the CTO tool dispatcher. A
// caller supplies validation and execution for the calls it supports.
type proposalRunner struct {
	log      func(string, ...any)
	state    *state
	now      func() time.Time
	validate func(context.Context, proposalCall) error
	run      func(context.Context, proposalCall, string) (json.RawMessage, error)
}

// openProposalCount is how many proposals wait for the owner. It is cheap
// enough for the info call the screen makes every refresh, so the screen can
// notice a new proposal without listing every proposal on every refresh.
func (p proposalRunner) openProposalCount() int {
	if p.state == nil || p.state.db == nil {
		return 0
	}
	now := p.now
	if now == nil {
		now = time.Now
	}
	var count int
	if err := p.state.db.QueryRow(`SELECT count(*) FROM cto_proposals WHERE status='open' AND created_at>?`,
		now().Add(-24*time.Hour).UnixNano()).Scan(&count); err != nil {
		return 0
	}
	return count
}

// expire closes the proposals that waited a day. A read of the open proposals
// comes first, and the write only when one is due: a write waits for the one
// connection and for every other writer of the file, and a list asks every
// second.
func (p proposalRunner) expire() error {
	cutoff := p.now().Add(-24 * time.Hour).UnixNano()
	var due int
	err := p.state.db.QueryRow(`SELECT EXISTS (SELECT 1 FROM cto_proposals WHERE status='open' AND created_at<=?)`, cutoff).Scan(&due)
	if err != nil || due == 0 {
		return err
	}
	_, err = p.state.db.Exec(`UPDATE cto_proposals SET status='expired', note='p_' || id || ' expired' WHERE status='open' AND created_at<=?`, cutoff)
	return err
}

func (p proposalRunner) propose(ctx context.Context, call proposalCall, reason string) (string, error) {
	if call.Tool == "" || p.validate == nil || p.run == nil {
		return "", errors.New("proposal requires a supported tool and runner")
	}
	var args map[string]any
	decoder := json.NewDecoder(bytes.NewReader(call.Args))
	decoder.UseNumber()
	if !json.Valid(call.Args) || decoder.Decode(&args) != nil || args == nil {
		return "", errors.New("proposal arguments must be a JSON object")
	}
	canonical, err := json.Marshal(args)
	if err != nil {
		return "", err
	}
	call.Args = canonical
	if err := p.validate(ctx, call); err != nil {
		return "", err
	}
	// The count and insertion share a transaction so concurrent proposals
	// cannot exceed the cap. The database has one connection.
	tx, err := p.state.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE cto_proposals SET status='expired', note='p_' || id || ' expired' WHERE status='open' AND created_at<=?`, p.now().Add(-24*time.Hour).UnixNano()); err != nil {
		return "", err
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM cto_proposals WHERE status='open'`).Scan(&count); err != nil {
		return "", err
	}
	var duplicate int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM cto_proposals WHERE status='open' AND tool=? AND action=? AND args=?`, call.Tool, call.Action, string(call.Args)).Scan(&duplicate); err != nil {
		return "", err
	}
	if duplicate != 0 {
		return "", errors.New("identical proposal is already open")
	}
	if count >= 20 {
		return "", errors.New("at most 20 proposals may be open")
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO cto_proposals(tool,action,args,reason,created_at,status) VALUES(?,?,?,?,?,'open')`, call.Tool, call.Action, string(call.Args), reason, p.now().UnixNano())
	if err != nil {
		return "", err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO cto_proposal_messages(proposal_id,sender,at) SELECT ?,sender,at FROM (SELECT id,sender,at FROM cto_deliveries ORDER BY id DESC LIMIT 3) ORDER BY id`, id); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return fmt.Sprintf("p_%d", id), nil
}

// deliverNote passes only the stored one-line note to the harness. Results are
// kept separately and are never selected by this delivery path.
func (p proposalRunner) deliverNote(ctx context.Context, send func(context.Context, string) error) error {
	p.state.proposalDeliveryMu.Lock()
	defer p.state.proposalDeliveryMu.Unlock()
	if err := p.expire(); err != nil {
		return err
	}
	var id int64
	var note string
	err := p.state.db.QueryRowContext(ctx, `SELECT id,note FROM cto_proposals WHERE note!='' AND note_delivered=0 ORDER BY id LIMIT 1`).Scan(&id, &note)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if err = send(ctx, note); err != nil {
		return err
	}
	_, err = p.state.db.ExecContext(ctx, `UPDATE cto_proposals SET note_delivered=1 WHERE id=?`, id)
	return err
}

// markNoteDelivered records that the outcome note already reached the CTO by
// another path, so the notes loop does not send it again.
func (p proposalRunner) markNoteDelivered(ctx context.Context, id string) error {
	_, err := p.state.db.ExecContext(ctx, `UPDATE cto_proposals SET note_delivered=1 WHERE id=?`, strings.TrimPrefix(id, "p_"))
	return err
}

func (d *Daemon) proposalNotesLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := d.deliverProposalNote(ctx); err != nil {
				d.Logf("proposal outcome delivery: %v", err)
			}
		}
	}
}

func (d *Daemon) deliverProposalNote(ctx context.Context) error {
	d.cto.mu.Lock()
	base, password, version, identity, session := d.cto.base, d.cto.password, d.cto.version, d.cto.identity, d.cto.session
	ref := d.claudeCTO.ref
	d.cto.mu.Unlock()
	idle := false
	if ref != "" {
		dir := filepath.Join(d.cfg.StateDir, "cto", "claude")
		if sf, ok := findClaudeCTOBg(d.cfg.ClaudeSessionsDir, dir, ref); ok {
			idle = sf.Status == "idle"
		}
	} else if base != "" && session != "" {
		busy, known := d.openCodeSessionBusy(ctx, base, password, version, identity, session)
		idle = known && !busy
	}
	if !idle {
		return d.proposalRunner().expire()
	}
	return d.proposalRunner().deliverNote(ctx, func(ctx context.Context, note string) error { _, err := d.ctoSend(ctx, note); return err })
}

// list returns every proposal with its delivery lines, oldest first.
func (p proposalRunner) list() ([]proposal, error) { return p.read("") }

// listOpen returns the proposals that wait for the owner. Callers that only
// look at open proposals use it, so they do not read the whole history.
func (p proposalRunner) listOpen() ([]proposal, error) { return p.read("status='open'") }

// get returns one proposal by its ID (p_1). ok is false when there is none.
func (p proposalRunner) get(id string) (item proposal, ok bool, err error) {
	n, perr := strconv.ParseInt(strings.TrimPrefix(id, "p_"), 10, 64)
	if perr != nil || !strings.HasPrefix(id, "p_") {
		return proposal{}, false, nil
	}
	items, err := p.read("id=?", n)
	if err != nil || len(items) == 0 {
		return proposal{}, false, err
	}
	return items[0], true, nil
}

// read loads the proposals and their delivery lines with two queries, however
// many proposals there are. It used to ask for the lines of each proposal
// alone, on a table without an index, so the work grew with the square of the
// history and took seconds on a CTO host that had run for a few months.
//
// filter is a condition on cto_proposals, or empty for all of them; args are
// its values.
func (p proposalRunner) read(filter string, args ...any) ([]proposal, error) {
	if err := p.expire(); err != nil {
		return nil, err
	}
	where, linesWhere := "", ""
	if filter != "" {
		where = ` WHERE ` + filter
		linesWhere = ` WHERE proposal_id IN (SELECT id FROM cto_proposals WHERE ` + filter + `)`
	}
	rows, err := p.state.db.Query(`SELECT id,tool,action,args,reason,created_at,status,note,result FROM cto_proposals`+where+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	var out []proposal
	index := map[int64]int{}
	for rows.Next() {
		var v proposal
		var id, at int64
		var argsText, result string
		if err := rows.Scan(&id, &v.Call.Tool, &v.Call.Action, &argsText, &v.Reason, &at, &v.Status, &v.Note, &result); err != nil {
			rows.Close()
			return nil, err
		}
		v.ID = "p_" + strconv.FormatInt(id, 10)
		v.CreatedAt = time.Unix(0, at)
		v.Call.Args = json.RawMessage(argsText)
		if result != "" {
			v.Result = json.RawMessage(result)
		}
		index[id] = len(out)
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if len(out) == 0 {
		return out, nil
	}
	lines, err := p.state.db.Query(`SELECT proposal_id,sender,at FROM cto_proposal_messages`+linesWhere+` ORDER BY rowid`, args...)
	if err != nil {
		return nil, err
	}
	defer lines.Close()
	for lines.Next() {
		var proposalID, at int64
		var item protocol.CTOProposalMessage
		if err := lines.Scan(&proposalID, &item.Sender, &at); err != nil {
			return nil, err
		}
		item.At = time.Unix(0, at)
		if i, ok := index[proposalID]; ok {
			out[i].Messages = append(out[i].Messages, item)
		}
	}
	return out, lines.Err()
}

func (p proposalRunner) decide(ctx context.Context, id string, approve bool) error {
	item, found, err := p.get(id)
	if err != nil {
		return err
	}
	if !found || item.Status != "open" {
		return errors.New("proposal is not open")
	}
	status := "declined"
	initialNote := id + " declined"
	if approve {
		status = "running"
		initialNote = ""
	}
	res, err := p.state.db.ExecContext(ctx, `UPDATE cto_proposals SET status=?,note=? WHERE id=? AND status='open'`, status, initialNote, strings.TrimPrefix(id, "p_"))
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("proposal is not open")
	}
	if !approve {
		return nil
	}
	note := id + " declined"
	var result json.RawMessage
	if approve {
		err = p.validate(ctx, item.Call)
		if err == nil {
			result, err = p.run(ctx, item.Call, "owner (CTO proposal "+id+")")
		}
		if err == nil && startProposal(item.Call) {
			var start protocol.StartedSession
			if json.Unmarshal(result, &start) == nil && start.Status == "failed" {
				message := start.Error
				if message == "" {
					message = "session start failed"
				}
				err = errors.New(message)
			}
		}
		if err != nil {
			status = "failed"
			message := []rune(cleanLine(err.Error()))
			if len(message) > 200 {
				message = message[:200]
			}
			note = id + " approved, failed: " + string(message)
		} else {
			status = "applied"
			note = id + " approved and applied"
			if startProposal(item.Call) {
				var start protocol.StartedSession
				if json.Unmarshal(result, &start) == nil {
					switch start.Status {
					case "needs_trust":
						note = id + " approved; Claude folder trust needs the owner's answer; no session started"
					case "needs_create":
						note = id + " approved; folder creation needs the owner's answer; no session started"
					}
				}
			}
		}
	}
	if p.log != nil {
		var start protocol.StartedSession
		_ = json.Unmarshal(result, &start)
		p.log("proposal %s session %s outcome %s: %s", id, start.ID, status, note)
	}
	_, saveErr := p.state.db.ExecContext(context.WithoutCancel(ctx), `UPDATE cto_proposals SET status=?,note=?,result=? WHERE id=?`, status, note, string(result), strings.TrimPrefix(id, "p_"))
	if saveErr != nil {
		return saveErr
	}
	return err
}

func (p proposalRunner) approveAll(ctx context.Context) error {
	items, err := p.listOpen()
	if err != nil {
		return err
	}
	for _, item := range items {
		if err := p.decide(ctx, item.ID, true); err != nil {
			return err
		}
	}
	return nil
}

// startProposal says the approved call starts a session, so its answer can say
// that nothing started (needs_trust, needs_create, failed).
func startProposal(c proposalCall) bool {
	return c.Tool == "session_change" && (c.Action == "session_start" || c.Action == "merger_start") ||
		c.Tool == "project_change" && c.Action == "create"
}
