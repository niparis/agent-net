package gateway

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

type hideFilter struct {
	Kind        string `json:"kind"`
	Result      string `json:"result"`
	Before      string `json:"before"`
	RepeatsOnly bool   `json:"repeats_only"`
}
type hideResult struct {
	IDs            []int64
	Before, After  int
	Token, Receipt string
}

func hidePreview(tx *sql.Tx, f hideFilter) (hideResult, error) {
	out := hideResult{IDs: []int64{}}
	before, err := time.Parse(time.RFC3339, f.Before)
	if err != nil || before.Location() != time.UTC {
		return out, fmt.Errorf("before must be a UTC RFC3339 time")
	}
	if f.Kind == "" || f.Result == "" {
		return out, fmt.Errorf("kind and result are required")
	}
	if f.RepeatsOnly && (f.Kind != "session_stall" || f.Result != "close") {
		return out, fmt.Errorf("repeats_only requires session_stall and close")
	}
	if err := tx.QueryRow(`SELECT COUNT(*) FROM ask_sorts WHERE hidden_at=0 AND result<>'undo'`).Scan(&out.Before); err != nil {
		return out, err
	}
	query := `SELECT r.id FROM ask_sorts r JOIN inbox_items i ON i.id=r.ask_id WHERE r.hidden_at=0 AND r.result<>'undo' AND i.status<>'open' AND i.kind=? AND r.result=? AND r.at<?`
	if f.RepeatsOnly {
		query += ` AND i.session_id<>'' AND i.conversation_id<>'' AND EXISTS (SELECT 1 FROM ask_sorts p JOIN inbox_items j ON j.id=p.ask_id WHERE j.kind='session_stall' AND j.session_id=i.session_id AND j.conversation_id=i.conversation_id AND p.result='close' AND p.id<r.id AND NOT EXISTS (SELECT 1 FROM ask_sorts x JOIN inbox_items k ON k.id=x.ask_id WHERE k.kind='session_stall' AND k.session_id=i.session_id AND k.conversation_id<>i.conversation_id AND x.result='close' AND x.id>p.id AND x.id<r.id))`
	}
	query += ` ORDER BY r.id`
	rows, err := tx.Query(query, f.Kind, f.Result, before.UnixNano())
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return out, err
		}
		out.IDs = append(out.IDs, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	out.After = out.Before - len(out.IDs)
	data, err := json.Marshal(struct {
		Filter hideFilter
		IDs    []int64
	}{f, out.IDs})
	if err != nil {
		return out, err
	}
	hash := sha256.Sum256(data)
	out.Token = hex.EncodeToString(hash[:])
	return out, nil
}

func (s *Store) hideApply(tx *sql.Tx, f hideFilter, token string) (hideResult, error) {
	out, err := hidePreview(tx, f)
	if err != nil {
		return out, err
	}
	if token == "" || token != out.Token {
		return out, fmt.Errorf("preview token is stale or invalid")
	}
	var b [16]byte
	if _, err = rand.Read(b[:]); err != nil {
		return out, err
	}
	out.Receipt = hex.EncodeToString(b[:])
	ids, err := json.Marshal(out.IDs)
	if err != nil {
		return out, err
	}
	if _, err = tx.Exec(`INSERT INTO ask_sort_hide_receipts(id,ids) VALUES(?,?)`, out.Receipt, string(ids)); err != nil {
		return out, err
	}
	for _, id := range out.IDs {
		if _, err = tx.Exec(`UPDATE ask_sorts SET hidden_at=? WHERE id=? AND hidden_at=0`, s.now().UnixNano(), id); err != nil {
			return out, err
		}
	}
	return out, nil
}

func (s *Store) hideUndo(tx *sql.Tx, receipt string) (hideResult, error) {
	out := hideResult{Receipt: receipt}
	var ids string
	if err := tx.QueryRow(`SELECT ids FROM ask_sort_hide_receipts WHERE id=? AND undone=0`, receipt).Scan(&ids); err != nil {
		return out, fmt.Errorf("unknown or already undone receipt")
	}
	if err := json.Unmarshal([]byte(ids), &out.IDs); err != nil {
		return out, err
	}
	if err := tx.QueryRow(`SELECT COUNT(*) FROM ask_sorts WHERE hidden_at=0 AND result<>'undo'`).Scan(&out.Before); err != nil {
		return out, err
	}
	for _, id := range out.IDs {
		r, err := tx.Exec(`UPDATE ask_sorts SET hidden_at=0 WHERE id=? AND hidden_at<>0`, id)
		if err != nil {
			return out, err
		}
		n, err := r.RowsAffected()
		if err != nil {
			return out, err
		}
		out.After += int(n)
	}
	out.After += out.Before
	_, err := tx.Exec(`UPDATE ask_sort_hide_receipts SET undone=1 WHERE id=?`, receipt)
	return out, err
}
