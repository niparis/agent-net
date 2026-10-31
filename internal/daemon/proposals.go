package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// proposalCall is the complete call the owner is asked to approve. The
// validator and runner must interpret its arguments in the same way.
type proposalCall struct {
	Tool   string
	Action string
	Args   json.RawMessage
}

type proposal struct {
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
	if json.Unmarshal(call.Args, &args) != nil || args == nil {
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
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return fmt.Sprintf("p_%d", id), nil
}

func (p proposalRunner) list() ([]proposal, error) {
	if err := p.expire(); err != nil {
		return nil, err
	}
	rows, err := p.state.db.Query(`SELECT id,tool,action,args,reason,created_at,status,note,result FROM cto_proposals ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
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
	return out, rows.Err()
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
	if approve {
		status = "running"
	}
	res, err := p.state.db.ExecContext(ctx, `UPDATE cto_proposals SET status=? WHERE id=? AND status='open'`, status, strings.TrimPrefix(id, "p_"))
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
	note := id + " declined"
	var result json.RawMessage
	if approve {
		err = p.validate(ctx, item.Call)
		if err == nil {
			result, err = p.run(ctx, item.Call, "owner (CTO proposal "+id+")")
		}
		if err != nil {
			status = "failed"
			message := []rune(strings.ReplaceAll(err.Error(), "\n", " "))
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
