package gateway

import (
	"database/sql"
	"encoding/json"
	"strconv"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

// DefaultInboxLimit is how many messages may wait at the gateway for one
// recipient unless messages.inbox_limit says otherwise. Zero turns the limit
// off (decision 0129).
const DefaultInboxLimit = 5

// waitingSQL matches the messages that count toward a recipient's inbox
// limit: peer messages that wait undelivered, with no batch carrier of their
// own. Messages in a batch are represented by the batch message, so counting
// both would count the same wait twice. Replies never count: an answer to the
// recipient's own request is always allowed through. Neither do agent-net's
// own notices, or messages between a worker and its caller in either
// direction (decision 0129, fix round). It takes the table alias as its first
// argument and the current time as its second.
func waitingSQL(alias string) string {
	return alias + `.batch_id='' AND ` + alias + `.state IN ('stored','dispatched') AND ` + alias + `.expires_at > ?
		AND ` + alias + `.type<>'reply' AND ` + alias + `.sender<>'` + agentNetSender + `' AND NOT ` + workerLinkSQL(alias)
}

// workerLinkSQL is true when one end of a message is a worker and the other is
// that worker's caller. Those messages carry task, notice and hand-back
// traffic and are not peer messages.
func workerLinkSQL(alias string) string {
	return `(EXISTS (SELECT 1 FROM sessions wsw WHERE wsw.id=` + alias + `.sender AND wsw.worker<>'' AND json_valid(wsw.worker)
			AND json_extract(wsw.worker,'$.owner')=` + alias + `.recipient)
		OR EXISTS (SELECT 1 FROM sessions wsr WHERE wsr.id=` + alias + `.recipient AND wsr.worker<>'' AND json_valid(wsr.worker)
			AND json_extract(wsr.worker,'$.owner')=` + alias + `.sender))`
}

// waitingCountTx counts the messages waiting at the gateway for a recipient.
func waitingCountTx(tx interface {
	QueryRow(string, ...any) *sql.Row
}, recipient string, now int64) (int, error) {
	var n int
	err := tx.QueryRow(`SELECT COUNT(*) FROM messages WHERE `+waitingSQL("messages")+` AND recipient=?`, now, recipient).Scan(&n)
	return n, err
}

// checkInboxLimit refuses a new message when the recipient already has the
// allowed number waiting. The owner, the CTO writing with the owner's voice,
// replies, agent-net's own notices and worker-to-caller traffic are exempt
// (decision 0129).
func checkInboxLimit(tx *sql.Tx, p protocol.MsgSend, ownerVoice bool, now int64) error {
	if ownerVoice || p.Type == protocol.TypeReply || p.Sender == agentNetSender {
		return nil
	}
	// WorkerTask is checked by workerTaskForSend; WorkerNotice and
	// WorkerIdleNotice are not authenticated by the gateway, so a peer could
	// set them. Real worker traffic is exempt through workerLinked below.
	if p.WorkerTask {
		return nil
	}
	sender, recipient := p.Sender, p.Recipient
	var harness string
	if err := tx.QueryRow(`SELECT harness FROM sessions WHERE id=?`, sender).Scan(&harness); err != nil {
		return err
	}
	if protocol.IsOwner(harness) {
		return nil
	}
	linked, err := workerLinked(tx, sender, recipient)
	if err != nil {
		return err
	}
	if linked {
		return nil
	}
	limit, err := inboxLimitTx(tx, recipient)
	if err != nil || limit <= 0 {
		return err
	}
	waiting, err := waitingCountTx(tx, recipient, now)
	if err != nil {
		return err
	}
	if waiting < limit {
		return nil
	}
	oldest, fromYou, err := oldestWaitingTx(tx, sender, recipient, now)
	if err != nil {
		return err
	}
	who := "from anyone"
	if fromYou {
		who = "from you"
	}
	at := "an unknown time"
	if !oldest.IsZero() {
		at = oldest.UTC().Format("2006-01-02 15:04 MST")
	}
	return reject("recipient has %d waiting, oldest %s at %s; withdraw one or wait", waiting, who, at)
}

// workerLinked says whether one end of the message is a worker and the other
// is that worker's caller.
func workerLinked(tx *sql.Tx, sender, recipient string) (bool, error) {
	for _, pair := range [][2]string{{sender, recipient}, {recipient, sender}} {
		var owner string
		err := tx.QueryRow(`SELECT COALESCE(CASE WHEN worker<>'' AND json_valid(worker)
			THEN json_extract(worker,'$.owner') END,'') FROM sessions WHERE id=?`, pair[0]).Scan(&owner)
		if err != nil {
			if err == sql.ErrNoRows {
				continue
			}
			return false, err
		}
		if owner != "" && owner == pair[1] {
			return true, nil
		}
	}
	return false, nil
}

// oldestWaitingTx returns the oldest waiting message to the recipient, and
// whether at least one of them came from the sender.
func oldestWaitingTx(tx *sql.Tx, sender, recipient string, now int64) (time.Time, bool, error) {
	var youAt, anyoneAt int64
	if err := tx.QueryRow(`SELECT COALESCE(MIN(created_at),0) FROM messages WHERE `+
		waitingSQL("messages")+` AND recipient=? AND sender=?`, now, recipient, sender).Scan(&youAt); err != nil {
		return time.Time{}, false, err
	}
	if err := tx.QueryRow(`SELECT COALESCE(MIN(created_at),0) FROM messages WHERE `+
		waitingSQL("messages")+` AND recipient=?`, now, recipient).Scan(&anyoneAt); err != nil {
		return time.Time{}, false, err
	}
	if youAt > 0 {
		return time.Unix(0, youAt).UTC(), true, nil
	}
	if anyoneAt > 0 {
		return time.Unix(0, anyoneAt).UTC(), false, nil
	}
	return time.Time{}, false, nil
}

// inboxLimitTx resolves messages.inbox_limit for one recipient. A missing or
// invalid value keeps the default.
func inboxLimitTx(tx *sql.Tx, recipient string) (int, error) {
	var project, agentID string
	err := tx.QueryRow(`SELECT COALESCE(NULLIF(repo_project,''),(SELECT project FROM profiles WHERE session_id=sessions.id),''),
		COALESCE(agent_id,'') FROM sessions WHERE id=?`, recipient).Scan(&project, &agentID)
	if err != nil {
		return 0, err
	}
	snap, err := settingsSnapshotTx(tx)
	if err != nil {
		return DefaultInboxLimit, err
	}
	e, err := snap.Resolve("messages.inbox_limit", settings.Where{Repo: project, Agent: agentID})
	if err != nil {
		return DefaultInboxLimit, nil
	}
	var n int
	if json.Unmarshal(e.Value, &n) != nil {
		return DefaultInboxLimit, nil
	}
	return n, nil
}

// enrichQueue fills the read-only queue fields of a message: its position in
// its recipient's gateway queue, how many messages wait there, and how long
// the recipient has been busy.
func (s *Store) enrichQueue(m protocol.Message) protocol.Message {
	now := s.now().UnixNano()
	waiting, err := waitingCountTx(s.db, m.To, now)
	if err != nil {
		return m
	}
	m.QueueWaiting = waiting
	if m.State == protocol.StateStored || m.State == protocol.StateDispatched {
		var uid int64
		_ = s.db.QueryRow(`SELECT rowid FROM messages WHERE id=?`, m.ID).Scan(&uid)
		var ahead int
		_ = s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE recipient=? AND `+
			waitingSQL("messages")+` AND (urgent > ? OR (urgent = ? AND (created_at > ? OR (created_at = ? AND rowid > ?))))`,
			m.To, now, boolInt(m.Urgent), boolInt(m.Urgent), m.CreatedAt.UnixNano(), m.CreatedAt.UnixNano(), uid).Scan(&ahead)
		m.QueuePosition = ahead + 1
	}
	var busySince int64
	if err := s.db.QueryRow(`SELECT COALESCE(busy_since,0) FROM sessions WHERE id=?`, m.To).Scan(&busySince); err == nil && busySince > 0 {
		m.BusyForSeconds = int64(time.Duration(now - busySince).Seconds())
	}
	return m
}

// queueStatusLine is the one-line receipt a sender gets: its position, how
// many wait, and the recipient's busy time.
func queueStatusLine(m protocol.Message) string {
	if m.Type == protocol.TypeReply || m.QueueWaiting == 0 || (m.State != protocol.StateStored && m.State != protocol.StateDispatched) {
		return ""
	}
	line := "queued behind " + strconv.Itoa(m.QueuePosition-1)
	if m.BusyForSeconds > 0 {
		line += ", session busy for " + durationMinutes(m.BusyForSeconds)
	}
	return line
}

func durationMinutes(seconds int64) string {
	if seconds < 60 {
		return strconv.FormatInt(seconds, 10) + "s"
	}
	return strconv.FormatInt(seconds/60, 10) + " min"
}
