package gateway

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// Historical migration constants from the removed Desk/Ask/Intake/Follow-ups
// subsystems. Shipped migrations are append-only (migrations.lock) and must
// stay byte-for-byte identical so existing databases replay cleanly before
// dropDeskTablesMigration drops the tables.

const chiefMigration = `-- Chief of staff (decision 0114): the sort history of an ask. prev_* keep the
-- ask as it was, so an undo can put it back.
CREATE TABLE IF NOT EXISTS ask_sorts (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 ask_id TEXT NOT NULL, at INTEGER NOT NULL, sorter TEXT NOT NULL,
 result TEXT NOT NULL, into_id TEXT NOT NULL DEFAULT '', to_id TEXT NOT NULL DEFAULT '',
 reason TEXT NOT NULL DEFAULT '', priority INTEGER NOT NULL DEFAULT 0, why_you TEXT NOT NULL DEFAULT '',
 prev_next TEXT NOT NULL DEFAULT '', prev_status TEXT NOT NULL DEFAULT '',
 prev_answer TEXT NOT NULL DEFAULT '', prev_answered_at INTEGER NOT NULL DEFAULT 0);
CREATE INDEX IF NOT EXISTS ask_sorts_ask ON ask_sorts(ask_id, id);
`

const chiefAdoptMigration = `
-- Chief of staff adoption (decision 0114): when an ask started waiting for the
-- chief. Zero means its created_at. An ask opened before the chief existed is
-- adopted now, so its 15 minutes start now, not when its asker wrote it.
-- route_fixed marks an ask whose creator named the next who, so adoption
-- leaves it there.
SELECT 1 /* chief ask adoption time */;
`

const chiefCorrectionsMigration = `-- The owner's marks on asks (decision 0114, part 2): didnt_need_me or
-- should_have_asked. area is the ask's project, ref the ask id.
CREATE TABLE IF NOT EXISTS chief_corrections (
 id INTEGER PRIMARY KEY AUTOINCREMENT, at INTEGER NOT NULL, mark TEXT NOT NULL,
 text TEXT NOT NULL, area TEXT NOT NULL DEFAULT '', ref TEXT NOT NULL DEFAULT '');
`

const ownerMarksMigration = `
-- Owner marks move to the judgment store (decision 0114, part 2): one memory.
INSERT INTO standing_orders(id,text,saved_at,source,tags,pinned,updated_at,kind,area,ref)
SELECT 'so_m_' || lower(hex(randomblob(8))), c.text, c.at / 1000000000, 'owner', '[]', 0, 0, 'correction', 'chief', c.ref
FROM chief_corrections c
WHERE NOT EXISTS (SELECT 1 FROM standing_orders o WHERE o.kind='correction' AND o.area='chief' AND o.ref=c.ref AND o.text=c.text AND o.saved_at=c.at / 1000000000)
ORDER BY c.id;
`

const frontDoorFlushMigration = `SELECT 1 /* Front door flush of open owner items (decision 0150) */;`

const frontDoorFlushKey = "front_door_flush_v1"

func (s *Store) flushOwnerAsksOnceTx(tx *sql.Tx) error {
	if _, done, err := markerGetTx(tx, frontDoorFlushKey); err != nil || done {
		return err
	}
	if ok, err := hasTable(tx, "inbox_items"); err != nil || !ok {
		return err
	}
	now := s.now().UnixNano()
	res, err := tx.Exec(`UPDATE inbox_items SET status='closed', answer='flushed_by_front_door', answered_at=?
		WHERE status='open' AND next_who IN ('','owner') AND about_kind<>'step' AND source_ref NOT LIKE 'permission:%'`, now)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	return markerSetTx(tx, frontDoorFlushKey, strconv.FormatInt(n, 10), now)
}

const followUpsMigration = `
CREATE TABLE IF NOT EXISTS follow_ups (
 id TEXT PRIMARY KEY,
 event TEXT NOT NULL CHECK (event IN ('reply','change','conversation','session')),
 set_by TEXT NOT NULL, waiter TEXT NOT NULL,
 message_id TEXT NOT NULL DEFAULT '', ask_id TEXT NOT NULL DEFAULT '',
 conversation_id TEXT NOT NULL DEFAULT '', recipient TEXT NOT NULL DEFAULT '',
 change_id TEXT NOT NULL DEFAULT '', watch_session TEXT NOT NULL DEFAULT '',
 summary TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL, due_at INTEGER NOT NULL,
 status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','met','raised')));
CREATE INDEX IF NOT EXISTS follow_ups_due ON follow_ups(status, due_at);
`

const ctoIntakeMigration = `
-- CTO intake queue (decision 0152).
CREATE TABLE IF NOT EXISTS cto_intake (
 id TEXT PRIMARY KEY,
 kind TEXT NOT NULL,
 subject TEXT NOT NULL,
 severity INTEGER NOT NULL,
 text TEXT NOT NULL DEFAULT '',
 detail TEXT NOT NULL DEFAULT '',
 first_seen INTEGER NOT NULL,
 last_seen INTEGER NOT NULL,
 count INTEGER NOT NULL DEFAULT 1,
 state TEXT NOT NULL DEFAULT 'open',
 alert_ask TEXT NOT NULL DEFAULT '',
 stuck_ask TEXT NOT NULL DEFAULT '',
 lead TEXT NOT NULL DEFAULT '',
 closed_at INTEGER NOT NULL DEFAULT 0,
 close_reason TEXT NOT NULL DEFAULT '');
CREATE UNIQUE INDEX IF NOT EXISTS cto_intake_open ON cto_intake(kind,subject) WHERE state='open';
CREATE INDEX IF NOT EXISTS cto_intake_seen ON cto_intake(state,last_seen);
-- The merge queue detector: failed test runs in a row on a project's train tip.
CREATE TABLE IF NOT EXISTS merge_train_fails (
 project TEXT PRIMARY KEY,
 fails INTEGER NOT NULL DEFAULT 0,
 last_run TEXT NOT NULL DEFAULT '',
 updated_at INTEGER NOT NULL DEFAULT 0);
`

// dropDeskTablesMigration drops the Desk, Ask store, Desk log and Follow-ups
// tables and any triggers referencing them (decision 0196).
const dropDeskTablesMigration = `
-- Decision 0196: remove the Desk, ask store, sort history, desk log and follow-ups.
DROP TRIGGER IF EXISTS inbox_items_desk_insert;
DROP TRIGGER IF EXISTS inbox_items_desk_update;
DROP TRIGGER IF EXISTS inbox_items_joined_settle;
DROP TABLE IF EXISTS inbox_items;
DROP TABLE IF EXISTS ask_sorts;
DROP TABLE IF EXISTS desk_log;
DROP TABLE IF EXISTS follow_ups;
`

// dropDeskTablesMarker finds dropDeskTablesMigration in the list.
const dropDeskTablesMarker = "Decision 0196: remove the Desk"

type openInboxRow struct {
	id, kind, sessionID, node, text, recommendation string
	options, askerName, whyYou, detail              string
	aboutKind, aboutID                              string
}

// keepOpenInboxItems runs in dropDeskTablesMigration's transaction, before the
// tables are dropped. Each still-open row in inbox_items becomes one idempotent
// message to the chief of staff (or a notification to the CTO when there is no
// chief), so nothing waiting for the owner silently disappears on upgrade.
func (s *Store) keepOpenInboxItems(tx *sql.Tx) error {
	if ok, err := hasTable(tx, "inbox_items"); err != nil || !ok {
		return err
	}
	rows, err := tx.Query(`SELECT id,kind,session_id,node,text,recommendation,options,asker_name,why_you,detail,about_kind,about_id
		FROM inbox_items WHERE status='open' ORDER BY created_at,id`)
	if err != nil {
		return err
	}
	var items []openInboxRow
	for rows.Next() {
		var r openInboxRow
		if err := rows.Scan(&r.id, &r.kind, &r.sessionID, &r.node, &r.text, &r.recommendation,
			&r.options, &r.askerName, &r.whyYou, &r.detail, &r.aboutKind, &r.aboutID); err != nil {
			rows.Close()
			return err
		}
		items = append(items, r)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(items) == 0 {
		return nil
	}
	to, err := chiefSession(tx)
	if err != nil {
		return err
	}
	viaChief := to != ""
	if !viaChief {
		if err := tx.QueryRow(`SELECT value FROM gateway_state WHERE key='cto_session'`).Scan(&to); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	if to == "" {
		if s.logf != nil {
			s.logf("desk.open_items_dropped", "count", len(items), "reason", "no chief and no CTO")
		}
		return nil
	}
	now := s.now().UTC()
	for _, item := range items {
		key := ownerItemKeyPrefix + "desk_migrate:" + item.id
		var sent int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM messages WHERE idem_key=?`, key).Scan(&sent); err != nil {
			return err
		}
		if sent > 0 {
			continue
		}
		from, typ := agentNetSender, protocol.TypeNotification
		if viaChief && item.sessionID != "" && item.sessionID != agentNetSender && item.sessionID != to {
			var known int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id=?`, item.sessionID).Scan(&known); err != nil {
				return err
			}
			if known > 0 {
				from, typ = item.sessionID, protocol.TypeRequest
			}
		}
		body := migratedInboxBody(tx, item)
		if len(body) > protocol.MaxBodyBytes {
			body = strings.ToValidUTF8(body[:protocol.MaxBodyBytes], "")
		}
		if _, err := tx.Exec(`INSERT INTO messages(id,conversation_id,parent_id,sender,recipient,type,body,hop,idem_key,created_at,expires_at,state)
			VALUES(?,?,'',?,?,?,?,0,?,?,?,?)`, protocol.NewID("m"), protocol.NewID("c"), from, to, typ, body,
			key, now.UnixNano(), now.Add(protocol.DefaultTTL).UnixNano(), protocol.StateStored); err != nil {
			return err
		}
		if s.logf != nil {
			s.logf("desk.open_item_migrated", "item", item.id, "kind", item.kind, "to", to, "type", typ)
		}
	}
	return nil
}

func migratedInboxBody(q askDB, item openInboxRow) string {
	who := strings.TrimSpace(item.askerName)
	if label := sessionLabel(q, item.sessionID); label != "" && label != who {
		if who != "" {
			who = who + " (" + label + ")"
		} else {
			who = label
		}
	}
	if who == "" {
		who = agentNetSender
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Open ask %s (%s) was waiting when agent-net removed the Desk.\n", item.id, item.kind)
	fmt.Fprintf(&b, "From: %s\n", who)
	if item.aboutKind != "" && item.aboutID != "" {
		fmt.Fprintf(&b, "About: %s %s\n", item.aboutKind, item.aboutID)
	} else if item.node != "" {
		fmt.Fprintf(&b, "Node: %s\n", item.node)
	}
	if text := strings.TrimSpace(item.text); text != "" {
		b.WriteString(text)
		b.WriteString("\n")
	}
	if detail := strings.TrimSpace(item.detail); detail != "" {
		fmt.Fprintf(&b, "Detail: %s\n", detail)
	}
	if why := strings.TrimSpace(item.whyYou); why != "" {
		fmt.Fprintf(&b, "Why only the owner: %s\n", why)
	}
	if rec := strings.TrimSpace(item.recommendation); rec != "" {
		fmt.Fprintf(&b, "Recommendation: %s\n", rec)
	}
	var opts []string
	if json.Unmarshal([]byte(item.options), &opts) == nil && len(opts) > 0 {
		fmt.Fprintf(&b, "Options: %s\n", strings.Join(opts, " / "))
	}
	return strings.TrimSpace(b.String())
}
