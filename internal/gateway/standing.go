package gateway

import (
	"errors"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// StandingOrders lists the owner's standing orders, oldest first.
func (s *Store) StandingOrders() ([]protocol.StandingOrder, error) {
	rows, err := s.db.Query(`SELECT id,text,saved_at,source FROM standing_orders ORDER BY saved_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.StandingOrder{}
	for rows.Next() {
		var o protocol.StandingOrder
		if err := rows.Scan(&o.ID, &o.Text, &o.SavedAt, &o.Source); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// SaveStandingOrder adds an order. Restore puts back a removed one with its
// old ID, time and source.
func (s *Store) SaveStandingOrder(q protocol.StandingOrdersRequest) (protocol.StandingOrder, error) {
	o := protocol.StandingOrder{ID: protocol.NewID("so"), SavedAt: time.Now().Unix(), Source: q.Source}
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
	_, err = s.db.Exec(`INSERT INTO standing_orders(id,text,saved_at,source) VALUES(?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET text=excluded.text,saved_at=excluded.saved_at,source=excluded.source`,
		o.ID, o.Text, o.SavedAt, o.Source)
	return o, err
}

// RemoveStandingOrder deletes one order and returns it.
func (s *Store) RemoveStandingOrder(id string) (*protocol.StandingOrder, error) {
	var o protocol.StandingOrder
	err := s.db.QueryRow(`SELECT id,text,saved_at,source FROM standing_orders WHERE id=?`, id).Scan(&o.ID, &o.Text, &o.SavedAt, &o.Source)
	if err != nil {
		return nil, errors.New("no standing order " + id)
	}
	if _, err := s.db.Exec(`DELETE FROM standing_orders WHERE id=?`, id); err != nil {
		return nil, err
	}
	return &o, nil
}
