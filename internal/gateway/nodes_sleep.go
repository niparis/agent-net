package gateway

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// silentAfter is how long a machine may stay offline without a sleep report
// before the gateway calls the outage possibly hung. One Desk item follows.
const silentAfter = 15 * time.Minute

// sleepTolerance is how far a sleep report may sit from the moment the link
// dropped and still explain that outage. A daemon's ticker and the link's own
// keepalive can differ by a few seconds.
const sleepTolerance = 2 * time.Minute

// NodeSilence is the row of one outage: when the link dropped, what the
// daemon said about sleeping, and whether the gateway marked it silent.
type NodeSilence struct {
	NodeID     string
	DroppedAt  time.Time
	AsleepFrom time.Time
	AsleepTo   time.Time // zero while the sleep is still ongoing
	SilentAt   time.Time
}

// Asleep reports whether a sleep report explains this outage.
func (n NodeSilence) Asleep() bool {
	return !n.AsleepFrom.IsZero() && (n.AsleepTo.IsZero() || !n.AsleepTo.Before(n.DroppedAt.Add(-sleepTolerance)))
}

// downState names why an offline machine is away: "asleep" when a sleep
// report covers the drop, "silent" when the gateway marked the gap. An empty
// state means the machine is online or the drop is newer than silentAfter.
func downState(dropped, asleepFrom, asleepTo, silentAt int64) (string, int64) {
	if dropped <= 0 {
		return "", 0
	}
	if asleepFrom > 0 && (asleepTo == 0 || asleepTo >= dropped-int64(sleepTolerance)) {
		return "asleep", asleepFrom / 1e9
	}
	if silentAt > 0 {
		return "silent", dropped / 1e9
	}
	return "", 0
}

// machineState is the one place a machine's state is decided, so the nodes
// list, agent_machine_get, agent_machines_find and the TUI agree. A live link
// makes a machine online, and it stays online while frames of any kind arrive
// on that link, including keepalive pings. Only a link that is open but has
// carried no frame for silentAfter reads "silent". With no link, the recorded
// outage names the state: asleep when a sleep report covers the drop, silent
// once the Desk item was raised, else offline. since is when the state began,
// in Unix seconds, and is 0 for a machine that is online.
func machineState(online bool, lastFrame, now time.Time, downState string, downSince int64) (string, int64) {
	if online {
		if !lastFrame.IsZero() && now.Sub(lastFrame) >= silentAfter {
			return "silent", lastFrame.Unix()
		}
		return "online", 0
	}
	if downState != "" {
		return downState, downSince
	}
	return "offline", 0
}

// NodeLinkDown records that a machine's link dropped at t. A drop older than
// the last frame the gateway saw from that machine is not an outage: the link
// came back after it, and the late cleanup of a replaced connection must not
// undo the return that already happened. A new outage starts with the rest: an
// older sleep span explains nothing about it. A sleep the daemon announced
// before it dropped (an ongoing span) is kept when it covers t.
func (s *Store) NodeLinkDown(nodeID string, t time.Time) error {
	s.linkMu.Lock()
	defer s.linkMu.Unlock()
	var seen int64
	switch err := s.db.QueryRow(`SELECT seen_at FROM nodes WHERE id=?`, nodeID).Scan(&seen); err {
	case nil:
		if seen > t.UnixNano() {
			return nil
		}
	case sql.ErrNoRows:
	default:
		return err
	}
	var asleepFrom, asleepTo int64
	err := s.db.QueryRow(`SELECT asleep_from,asleep_to FROM node_silence WHERE node_id=?`, nodeID).Scan(&asleepFrom, &asleepTo)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	keep := asleepFrom > 0
	if keep && asleepTo > 0 {
		keep = sleepExplains(t.UnixNano(), time.Unix(0, asleepFrom), time.Unix(0, asleepTo))
	}
	if keep {
		_, err := s.db.Exec(`UPDATE node_silence SET dropped_at=?,silent_at=0 WHERE node_id=?`, t.UnixNano(), nodeID)
		return err
	}
	_, err = s.db.Exec(`INSERT INTO node_silence(node_id,dropped_at,asleep_from,asleep_to,silent_at) VALUES(?,?,0,0,0)
ON CONFLICT(node_id) DO UPDATE SET dropped_at=excluded.dropped_at, asleep_from=0, asleep_to=0, silent_at=0`,
		nodeID, t.UnixNano())
	return err
}

// NodeLinkUp records that a machine returned. It closes the open ask about
// the outage, if one was raised, and forgets the outage row.
func (s *Store) NodeLinkUp(nodeID string) error {
	s.linkMu.Lock()
	defer s.linkMu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var row NodeSilence
	var dropped, asleepFrom, asleepTo, silentAt int64
	err = tx.QueryRow(`SELECT dropped_at,asleep_from,asleep_to,silent_at FROM node_silence WHERE node_id=?`, nodeID).
		Scan(&dropped, &asleepFrom, &asleepTo, &silentAt)
	if err == sql.ErrNoRows {
		return tx.Commit()
	}
	if err != nil {
		return err
	}
	row = NodeSilence{NodeID: nodeID, DroppedAt: time.Unix(0, dropped)}
	if asleepFrom > 0 {
		row.AsleepFrom = time.Unix(0, asleepFrom)
	}
	if asleepTo > 0 {
		row.AsleepTo = time.Unix(0, asleepTo)
	}
	if silentAt > 0 {
		row.SilentAt = time.Unix(0, silentAt)
	}
	if !row.SilentAt.IsZero() {
		if _, err := s.closeAsks(tx, `about_kind=? AND about_id=?`, []any{protocol.AskAboutMachine, nodeID}, protocol.AskReasonReturned); err != nil {
			return err
		}
		if err := s.closeIntake(tx, protocol.IntakeMachineSilent, nodeID, intakeReturned, s.now()); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM node_silence WHERE node_id=?`, nodeID); err != nil {
		return err
	}
	return tx.Commit()
}

// NodeSlept records a sleep span a daemon reported. It returns true when the
// span explains the machine's open outage; the outage then reads "asleep" and
// any ask it raised is closed.
func (s *Store) NodeSlept(nodeID string, from, to time.Time) (bool, error) {
	if from.IsZero() {
		return false, nil
	}
	s.linkMu.Lock()
	defer s.linkMu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var dropped, asleepFrom, asleepTo, silentAt int64
	err = tx.QueryRow(`SELECT dropped_at,asleep_from,asleep_to,silent_at FROM node_silence WHERE node_id=?`, nodeID).
		Scan(&dropped, &asleepFrom, &asleepTo, &silentAt)
	if err == sql.ErrNoRows {
		if to.IsZero() {
			// An announced sleep that is still ongoing. The drop that follows
			// it will keep the span (NodeLinkDown).
			_, err = tx.Exec(`INSERT INTO node_silence(node_id,dropped_at,asleep_from,asleep_to,silent_at) VALUES(?,0,?,0,0)`,
				nodeID, from.UnixNano())
			if err != nil {
				return false, err
			}
		}
		return false, tx.Commit()
	}
	if err != nil {
		return false, err
	}
	if !sleepExplains(dropped, from, to) {
		return false, tx.Commit()
	}
	var toNanos int64
	if !to.IsZero() {
		toNanos = to.UnixNano()
	}
	if _, err := tx.Exec(`UPDATE node_silence SET asleep_from=?,asleep_to=?,silent_at=0 WHERE node_id=?`,
		from.UnixNano(), toNanos, nodeID); err != nil {
		return false, err
	}
	if silentAt > 0 {
		if _, err := s.closeAsks(tx, `about_kind=? AND about_id=?`, []any{protocol.AskAboutMachine, nodeID}, protocol.AskReasonSlept); err != nil {
			return false, err
		}
		if err := s.closeIntake(tx, protocol.IntakeMachineSilent, nodeID, intakeSlept, s.now()); err != nil {
			return false, err
		}
	}
	return true, tx.Commit()
}

// sleepExplains says whether a sleep span covers a link drop. The tolerance
// lets a report made a few seconds before or after the drop count.
func sleepExplains(dropped int64, from, to time.Time) bool {
	if dropped <= 0 || from.IsZero() {
		return false
	}
	tolerance := int64(sleepTolerance)
	if from.UnixNano() > dropped+tolerance {
		return false
	}
	if to.IsZero() {
		// The sleep is still ongoing, and it began at or before the drop.
		return true
	}
	return to.UnixNano() >= dropped-tolerance
}

// markSilentMachines raises the Desk item of every outage that stayed
// unexplained for silentAfter. The maintenance loop calls it once a minute.
func (g *Gateway) markSilentMachines(now time.Time) {
	marked, err := g.store.MarkSilentNodes(now)
	if err != nil {
		g.logLine("error", "where", "machine silence", "err", err)
		return
	}
	for _, m := range marked {
		host := g.store.hostname(m.NodeID)
		g.logLine("node.silent", "node", m.NodeID, "host", host, "since", m.DroppedAt.UTC().Format(time.RFC3339))
		g.store.Event("node.silent", m.NodeID, "", "", m.DroppedAt.UTC().Format(time.RFC3339))
	}
}

// MarkSilentNodes marks every outage older than silentAfter that no sleep
// report explains, and raises one Desk item for each. It returns the rows it
// marked. Calling it again changes nothing until the machine drops again.
func (s *Store) MarkSilentNodes(now time.Time) ([]NodeSilence, error) {
	cutoff := now.Add(-silentAfter).UnixNano()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT sl.node_id,sl.dropped_at,sl.asleep_from,sl.asleep_to,sl.silent_at,
 COALESCE(NULLIF(n.display_name,''),NULLIF(n.hostname,''),sl.node_id)
FROM node_silence sl LEFT JOIN nodes n ON n.id=sl.node_id
WHERE sl.dropped_at>0 AND sl.silent_at=0 AND sl.dropped_at<=?`, cutoff)
	if err != nil {
		return nil, err
	}
	type candidate struct {
		row  NodeSilence
		host string
	}
	var list []candidate
	for rows.Next() {
		var c candidate
		var dropped, asleepFrom, asleepTo, silentAt int64
		if err := rows.Scan(&c.row.NodeID, &dropped, &asleepFrom, &asleepTo, &silentAt, &c.host); err != nil {
			rows.Close()
			return nil, err
		}
		c.row.DroppedAt = time.Unix(0, dropped)
		if asleepFrom > 0 {
			c.row.AsleepFrom = time.Unix(0, asleepFrom)
		}
		if asleepTo > 0 {
			c.row.AsleepTo = time.Unix(0, asleepTo)
		}
		if silentAt > 0 {
			c.row.SilentAt = time.Unix(0, silentAt)
		}
		if c.row.Asleep() {
			continue
		}
		list = append(list, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	var marked []NodeSilence
	for _, c := range list {
		res, err := tx.Exec(`UPDATE node_silence SET silent_at=? WHERE node_id=? AND silent_at=0`, now.UnixNano(), c.row.NodeID)
		if err != nil {
			return nil, err
		}
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			if err != nil {
				return nil, err
			}
			continue
		}
		c.row.SilentAt = now
		// A silent machine goes to the CTO intake queue and wakes the CTO
		// through the chief (decision 0152), never to the owner.
		at := c.row.DroppedAt.UTC().Format("2006-01-02 15:04 MST")
		if _, err := s.recordIntake(tx, intakeEvent{
			Kind: protocol.IntakeMachineSilent, Subject: c.row.NodeID,
			AboutKind: protocol.AskAboutMachine, AboutID: c.row.NodeID,
			Text:   fmt.Sprintf("%s went silent at %s without sleeping; it may be hung", c.host, at),
			Detail: "Last link drop: " + c.row.DroppedAt.UTC().Format(time.RFC3339),
		}, now); err != nil {
			return nil, err
		}
		marked = append(marked, c.row)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return marked, nil
}
