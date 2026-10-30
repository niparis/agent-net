package gateway

import (
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/niparis/agent-net/internal/protocol"
)

// SetInventory replaces a successful observation. A failed scan retains the
// last successful items as stale rather than treating a failure as deletion.
func (s *Store) SetInventory(nodeID string, r protocol.InventoryReport) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if r.State == protocol.InventoryError {
		var old string
		err = tx.QueryRow(`SELECT report FROM inventory_reports WHERE node_id=?`, nodeID).Scan(&old)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			var previous protocol.InventoryReport
			if err = json.Unmarshal([]byte(old), &previous); err != nil {
				return err
			}
			for i := range previous.Items {
				previous.Items[i].State = protocol.InventoryStale
				previous.Items[i].Reason = "latest scan failed"
			}
			previous.State = protocol.InventoryStale
			previous.Reason = "latest scan failed"
			previous.ScannedAt = r.ScannedAt
			r = previous
		}
	}
	r.Machine = nodeID
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO inventory_reports(node_id,report) VALUES(?,?) ON CONFLICT(node_id) DO UPDATE SET report=excluded.report`, nodeID, string(b)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Inventories() ([]protocol.InventoryReport, error) {
	rows, err := s.db.Query(`SELECT i.node_id,i.report,COALESCE(n.hostname,'') FROM inventory_reports i LEFT JOIN nodes n ON n.id=i.node_id ORDER BY i.node_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	reports := []protocol.InventoryReport{}
	for rows.Next() {
		var id, raw, host string
		if err = rows.Scan(&id, &raw, &host); err != nil {
			return nil, err
		}
		var r protocol.InventoryReport
		if err = json.Unmarshal([]byte(raw), &r); err != nil {
			return nil, err
		}
		r.Machine = host
		if r.Machine == "" {
			r.Machine = id
		}
		reports = append(reports, r)
	}
	return reports, rows.Err()
}

// inventoryPage splits reports by item and limits encoded response size.
func inventoryPage(reports []protocol.InventoryReport, offset int) (protocol.InventoryPage, error) {
	const maxBytes = 240 << 10
	if offset < 0 {
		return protocol.InventoryPage{}, errors.New("invalid inventory offset")
	}
	page := protocol.InventoryPage{Reports: []protocol.InventoryReport{}}
	index := 0
	for _, report := range reports {
		items := report.Items
		if len(items) == 0 {
			items = []protocol.InventoryItem{{}}
		}
		for _, item := range items {
			if index < offset {
				index++
				continue
			}
			one := report
			one.Items = nil
			if len(report.Items) > 0 {
				one.Items = []protocol.InventoryItem{item}
			}
			page.Reports = append(page.Reports, one)
			b, err := json.Marshal(page)
			if err != nil {
				return page, err
			}
			if len(b) > maxBytes {
				page.Reports = page.Reports[:len(page.Reports)-1]
				if len(page.Reports) == 0 {
					return page, errors.New("inventory item exceeds response limit")
				}
				page.Next = index
				return page, nil
			}
			index++
		}
	}
	return page, nil
}
