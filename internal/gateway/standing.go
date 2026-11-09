package gateway

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// StandingOrders lists the owner's standing orders, oldest first.
func (s *Store) StandingOrders() ([]protocol.StandingOrder, error) {
	rows, err := s.db.Query(`SELECT id,text,saved_at,source,tags,pinned,updated_at FROM standing_orders ORDER BY saved_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.StandingOrder{}
	for rows.Next() {
		var o protocol.StandingOrder
		var tags string
		if err := rows.Scan(&o.ID, &o.Text, &o.SavedAt, &o.Source, &tags, &o.Pinned, &o.UpdatedAt); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(tags), &o.Tags)
		out = append(out, o)
	}
	return out, rows.Err()
}

// SaveStandingOrder adds an order. Restore puts back a removed one with its
// old ID, time and source.
func (s *Store) SaveStandingOrder(q protocol.StandingOrdersRequest) (protocol.StandingOrder, error) {
	o := protocol.StandingOrder{ID: protocol.NewID("so"), SavedAt: time.Now().Unix(), Source: q.Source, Tags: q.Tags}
	if q.Pinned != nil {
		o.Pinned = *q.Pinned
	}
	if q.Restore != nil {
		o = *q.Restore
	}
	text, err := protocol.CleanStandingText(firstNonEmpty(o.Text, q.Text))
	if err != nil {
		return o, err
	}
	o.Text = text
	if o.Source != protocol.StandingSourceCTO {
		o.Source = protocol.StandingSourceOwner
	}
	tags, _ := json.Marshal(o.Tags)
	_, err = s.db.Exec(`INSERT INTO standing_orders(id,text,saved_at,source,tags,pinned,updated_at) VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET text=excluded.text,saved_at=excluded.saved_at,source=excluded.source,tags=excluded.tags,pinned=excluded.pinned,updated_at=excluded.updated_at`,
		o.ID, o.Text, o.SavedAt, o.Source, string(tags), o.Pinned, o.UpdatedAt)
	return o, err
}

// RemoveStandingOrder deletes one order and returns it.
func (s *Store) RemoveStandingOrder(id string) (*protocol.StandingOrder, error) {
	var o protocol.StandingOrder
	var tags string
	err := s.db.QueryRow(`SELECT id,text,saved_at,source,tags,pinned,updated_at FROM standing_orders WHERE id=?`, id).Scan(&o.ID, &o.Text, &o.SavedAt, &o.Source, &tags, &o.Pinned, &o.UpdatedAt)
	if err != nil {
		return nil, errors.New("no standing order " + id)
	}
	json.Unmarshal([]byte(tags), &o.Tags)
	if _, err := s.db.Exec(`DELETE FROM standing_orders WHERE id=?`, id); err != nil {
		return nil, err
	}
	return &o, nil
}

func (s *Store) UpdateStandingOrder(q protocol.StandingOrdersRequest) (protocol.StandingOrder, error) {
	orders, err := s.StandingOrders()
	if err != nil {
		return protocol.StandingOrder{}, err
	}
	for _, o := range orders {
		if o.ID != q.ID {
			continue
		}
		if q.Text != "" {
			o.Text, err = protocol.CleanStandingText(q.Text)
			if err != nil {
				return o, err
			}
		}
		if q.Tags != nil {
			o.Tags = q.Tags
		}
		if q.Pinned != nil {
			o.Pinned = *q.Pinned
		}
		o.UpdatedAt = time.Now().Unix()
		return s.SaveStandingOrder(protocol.StandingOrdersRequest{Restore: &o})
	}
	return protocol.StandingOrder{}, errors.New("no standing order " + q.ID)
}
func (s *Store) SearchStandingOrders(q protocol.StandingOrdersRequest) ([]protocol.StandingOrder, error) {
	rows, err := s.db.Query(`SELECT o.id,o.text,o.saved_at,o.source,o.tags,o.pinned,o.updated_at,snippet(standing_orders_fts,0,'[',']','...',24)
 FROM standing_orders_fts JOIN standing_orders o ON o.rowid=standing_orders_fts.rowid
 WHERE standing_orders_fts MATCH ? AND (?='' OR EXISTS(SELECT 1 FROM json_each(o.tags) WHERE value=?))
 AND (?='' OR o.source=?) AND o.saved_at>=? ORDER BY bm25(standing_orders_fts),o.id LIMIT 20`, q.Query, q.Tag, q.Tag, q.Source, q.Source, q.SavedSince)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.StandingOrder{}
	for rows.Next() {
		var o protocol.StandingOrder
		var tags string
		if err := rows.Scan(&o.ID, &o.Text, &o.SavedAt, &o.Source, &tags, &o.Pinned, &o.UpdatedAt, &o.Snippet); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(tags), &o.Tags)
		out = append(out, o)
	}
	return out, rows.Err()
}
func (s *Store) StandingOrderHistory(id string) ([]protocol.StandingOrder, error) {
	rows, err := s.db.Query(`SELECT order_id,text,tags,changed_at FROM standing_order_history WHERE order_id=? ORDER BY rowid`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.StandingOrder{}
	for rows.Next() {
		var o protocol.StandingOrder
		var tags string
		if err := rows.Scan(&o.ID, &o.Text, &tags, &o.UpdatedAt); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(tags), &o.Tags)
		out = append(out, o)
	}
	return out, rows.Err()
}
