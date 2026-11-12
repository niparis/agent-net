package gateway

import (
	"database/sql"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// deskRerouteMigration moves the open Desk items made before the front door
// (plan step (d), decisions 0150 and 0152). The SQL is only a marker: the work
// is rerouteOpenAsksTx, which reads the route table in routes.go, so the
// migration follows the code's routes and its locked hash does not change
// when the table does.
const deskRerouteMigration = `SELECT 1 /* Desk reroute of open items (decision 0152) */;`

// rerouteOpenAsksTx re-routes every open Desk item by the route table. It
// never changes an item's status, so nothing is closed or dropped, and a
// second run finds nothing to move.
//
//   - Sign-in and OAuth asks go to the owner, as a new one would.
//   - An ask the chief routes that waits on the owner goes to the chief
//     (owner chief, state verifying), unless the chief itself sent it to the
//     owner and it meets the owner-item rule of step (a): options where the
//     kind needs them. Before the front door, the 15-minute timer and a
//     missing chief also put asks there; those were never verified.
//   - Every open item gets an owner and a state.
func (s *Store) rerouteOpenAsksTx(tx *sql.Tx) error {
	rows, err := tx.Query(`SELECT i.id,i.kind,i.from_kind,i.source_ref,i.next_who,i.options,
		COALESCE((SELECT r.result FROM ask_sorts r WHERE r.ask_id=i.id ORDER BY r.id DESC LIMIT 1),'')
		FROM inbox_items i WHERE i.status='open'`)
	if err != nil {
		return err
	}
	var toOwner, toChief []string
	for rows.Next() {
		var item protocol.InboxItem
		var options, lastSort string
		if err := rows.Scan(&item.ID, &item.Kind, &item.FromKind, &item.SourceRef, &item.NextWho, &options, &lastSort); err != nil {
			rows.Close()
			return err
		}
		rt, ok := routeOf(routeAsk, askRouteKey(item))
		if !ok {
			// createInboxItem's default for a kind with no row.
			rt = route{Class: routeAsk, Kind: item.Kind, To: routeChief}
		}
		ownerBound := item.NextWho == "" || item.NextWho == protocol.AskNextOwner
		switch {
		case rt.Auto && rt.To == routeOwner:
			if item.NextWho != protocol.AskNextOwner {
				toOwner = append(toOwner, item.ID)
			}
		case rt.To == routeChief && ownerBound:
			hasOptions := options != "" && options != "[]" && options != "null"
			chiefSent := lastSort == protocol.AskSortOwner
			if !chiefSent || (protocol.KindNeedsOptions(item.Kind) && !hasOptions) {
				toChief = append(toChief, item.ID)
			}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	// route_fixed keeps chief adoption off a sign-in, as createInboxItem does
	// for a caller that names the next who.
	for _, id := range toOwner {
		if _, err := tx.Exec(`UPDATE inbox_items SET next_who=?, route_fixed=1, owner=?, state=? WHERE id=?`, protocol.AskNextOwner, protocol.DeskOwnerChief, protocol.DeskReady, id); err != nil {
			return err
		}
	}
	// chief_at starts the chief's wait now, not at the ask's creation, and
	// chief_sent_at=0 puts the ask in the chief's next batch.
	now := s.now().UnixNano()
	for _, id := range toChief {
		if _, err := tx.Exec(`UPDATE inbox_items SET next_who=?, chief_at=?, chief_sent_at=0, owner=?, state=? WHERE id=?`, protocol.AskNextChief, now, protocol.DeskOwnerChief, protocol.DeskVerifying, id); err != nil {
			return err
		}
	}
	// The step (a) rule for owner and state, read from the item's own row.
	row := strings.NewReplacer("NEW.", "inbox_items.")
	_, err = tx.Exec(`UPDATE inbox_items SET owner=` + row.Replace(deskOwnerSQL) + `, state=` + row.Replace(deskStateSQL) +
		` WHERE status='open' AND (owner='' OR state='')`)
	return err
}
