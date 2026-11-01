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
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Propose validates and stores a call for the owner's decision.
func (d *Daemon) Propose(ctx context.Context, tool, action string, args json.RawMessage, reason string) (string, error) {
	if err := d.ctoDispatchMu.lock(ctx); err != nil {
		return "", err
	}
	defer d.ctoDispatchMu.unlock()
	return d.proposalRunner().propose(ctx, proposalCall{Tool: tool, Action: action, Args: args}, reason)
}

// ProposalToolResult is the brief response intended for the CTO tool call.
func ProposalToolResult(id string) string {
	return "proposal " + id + " is in Needs you; the owner decides. You will get a note with the outcome."
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
	data, err := d.call(ctx, "query.nodes", nil)
	if err != nil {
		return nil, err
	}
	var nodes protocol.NodesInfo
	if err := json.Unmarshal(data, &nodes); err != nil {
		return nil, err
	}
	for _, node := range nodes.Nodes {
		if !node.Online || node.NodeID == d.cfg.NodeID {
			continue
		}
		data, err := d.call(ctx, "query.proposal_list", protocol.CTOProposalAnswer{NodeID: node.NodeID})
		if err != nil {
			return nil, fmt.Errorf("list proposals on %s: %w", node.Hostname, err)
		}
		var items []protocol.CTOProposal
		if err := json.Unmarshal(data, &items); err != nil {
			return nil, err
		}
		out = append(out, items...)
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
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (d *Daemon) proposalRunner() proposalRunner {
	return proposalRunner{state: d.st, now: time.Now, validate: d.validateProposal, run: d.runProposal}
}

func (d *Daemon) validateProposal(ctx context.Context, call proposalCall) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(call.Args, &fields); err != nil {
		return err
	}
	for name, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("%s must be a string", name)
		}
	}
	switch {
	case (call.Tool == "edge_set" && call.Action == "") || (call.Tool == "graph_change" && call.Action == "edge_set"):
		var q protocol.Edge
		decoder := json.NewDecoder(bytes.NewReader(call.Args))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&q); err != nil {
			return fmt.Errorf("bad edge_set: %w", err)
		}
		_, err := d.call(ctx, "edge.validate", q)
		return err
	case call.Tool == "node_rename" && call.Action == "":
		var q protocol.NodeRenameRequest
		decoder := json.NewDecoder(bytes.NewReader(call.Args))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&q); err != nil {
			return fmt.Errorf("bad node_rename: %w", err)
		}
		_, err := d.call(ctx, "node.rename.validate", q)
		return err
	case (call.Tool == "charter_set" && call.Action == "") || (call.Tool == "graph_change" && call.Action == "charter_set"):
		var q protocol.CharterSetRequest
		decoder := json.NewDecoder(bytes.NewReader(call.Args))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&q); err != nil {
			return fmt.Errorf("bad charter_set: %w", err)
		}
		_, err := d.call(ctx, "charter.validate", q)
		return err
	default:
		return errors.New("unsupported proposal tool or action")
	}
}

func (d *Daemon) runProposal(ctx context.Context, call proposalCall, actor string) (json.RawMessage, error) {
	switch {
	case (call.Tool == "edge_set" && call.Action == "") || (call.Tool == "graph_change" && call.Action == "edge_set"):
		var args map[string]any
		if err := json.Unmarshal(call.Args, &args); err != nil {
			return nil, err
		}
		args["by"] = actor
		return d.call(ctx, "edge.set", args)
	case call.Tool == "node_rename" && call.Action == "":
		var args map[string]any
		if err := json.Unmarshal(call.Args, &args); err != nil {
			return nil, err
		}
		args["by"] = actor
		return d.call(ctx, "node.rename", args)
	case (call.Tool == "charter_set" && call.Action == "") || (call.Tool == "graph_change" && call.Action == "charter_set"):
		var args map[string]any
		if err := json.Unmarshal(call.Args, &args); err != nil {
			return nil, err
		}
		args["by"] = actor
		return d.call(ctx, "charter.set", args)
	default:
		return nil, errors.New("unsupported proposal tool or action")
	}
}

func (d *Daemon) proposalOperator(ctx context.Context, method string, q protocol.CTOProposalAnswer) (any, bool, error) {
	p := d.proposalRunner()
	if method == protocol.OpProposalAnswer {
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
	state    *state
	now      func() time.Time
	validate func(context.Context, proposalCall) error
	run      func(context.Context, proposalCall, string) (json.RawMessage, error)
}

func (p proposalRunner) expire() error {
	_, err := p.state.db.Exec(`UPDATE cto_proposals SET status='expired', note='p_' || id || ' expired' WHERE status='open' AND created_at<=?`, p.now().Add(-24*time.Hour).UnixNano())
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
	base, password, session := d.cto.base, d.cto.password, d.cto.session
	ref := d.claudeCTO.ref
	d.cto.mu.Unlock()
	idle := false
	if ref != "" {
		dir := filepath.Join(d.cfg.StateDir, "cto", "claude")
		if sf, ok := findClaudeCTOBg(d.cfg.ClaudeSessionsDir, dir, ref); ok {
			idle = sf.Status == "idle"
		}
	} else if base != "" && session != "" {
		busy, known := openCodeSessionBusy(ctx, base, password, session)
		idle = known && !busy
	}
	if !idle {
		return d.proposalRunner().expire()
	}
	return d.proposalRunner().deliverNote(ctx, func(ctx context.Context, note string) error { _, err := d.ctoSend(ctx, note); return err })
}

func (p proposalRunner) list() ([]proposal, error) {
	if err := p.expire(); err != nil {
		return nil, err
	}
	rows, err := p.state.db.Query(`SELECT id,tool,action,args,reason,created_at,status,note,result FROM cto_proposals ORDER BY id`)
	if err != nil {
		return nil, err
	}
	var out []proposal
	for rows.Next() {
		var v proposal
		var id, at int64
		var args, result string
		if err := rows.Scan(&id, &v.Call.Tool, &v.Call.Action, &args, &v.Reason, &at, &v.Status, &v.Note, &result); err != nil {
			return nil, err
		}
		v.ID = fmt.Sprintf("p_%d", id)
		v.CreatedAt = time.Unix(0, at)
		v.Call.Args = json.RawMessage(args)
		if result != "" {
			v.Result = json.RawMessage(result)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for i := range out {
		rows, err := p.state.db.Query(`SELECT sender,at FROM cto_proposal_messages WHERE proposal_id=? ORDER BY rowid`, strings.TrimPrefix(out[i].ID, "p_"))
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var item protocol.CTOProposalMessage
			var at int64
			if err = rows.Scan(&item.Sender, &at); err != nil {
				rows.Close()
				return nil, err
			}
			item.At = time.Unix(0, at)
			out[i].Messages = append(out[i].Messages, item)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (p proposalRunner) decide(ctx context.Context, id string, approve bool) error {
	items, err := p.list()
	if err != nil {
		return err
	}
	var item proposal
	found := false
	for _, v := range items {
		if v.ID == id && v.Status == "open" {
			item = v
			found = true
			break
		}
	}
	if !found {
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
		}
	}
	_, saveErr := p.state.db.ExecContext(context.WithoutCancel(ctx), `UPDATE cto_proposals SET status=?,note=?,result=? WHERE id=?`, status, note, string(result), strings.TrimPrefix(id, "p_"))
	if saveErr != nil {
		return saveErr
	}
	return err
}

func (p proposalRunner) approveAll(ctx context.Context) error {
	items, err := p.list()
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.Status == "open" {
			if err := p.decide(ctx, item.ID, true); err != nil {
				return err
			}
		}
	}
	return nil
}
