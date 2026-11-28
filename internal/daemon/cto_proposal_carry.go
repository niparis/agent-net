package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Until decision 0199 the daemon stored the CTO's proposals in cto_proposals.
// An open one still waited for the owner, so the upgrade keeps it in
// cto_proposal_carry and drops the old tables. Once the link is up, the daemon
// sends each kept row to the chief of staff as a request from the CTO, and
// removes the row. Nothing the owner was asked disappears without a message.

// carryOpenCTOProposals copies the open proposals into cto_proposal_carry and
// drops the old tables, in one transaction. A daemon.db without them, new or
// already upgraded, is left as it is.
func carryOpenCTOProposals(db *sql.DB) error {
	var has int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='cto_proposals'`).Scan(&has); err != nil {
		return err
	}
	if has == 0 {
		_, err := db.Exec(`DROP TABLE IF EXISTS cto_proposal_messages`)
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT OR IGNORE INTO cto_proposal_carry(id,tool,action,args,reason,created_at)
		SELECT id,tool,action,args,reason,created_at FROM cto_proposals WHERE status='open'`); err != nil {
		return fmt.Errorf("keep open CTO proposals: %w", err)
	}
	if _, err := tx.Exec(`DROP TABLE cto_proposals; DROP TABLE IF EXISTS cto_proposal_messages`); err != nil {
		return err
	}
	return tx.Commit()
}

// carriedProposal is one open proposal kept from cto_proposals.
type carriedProposal struct {
	id                         int64
	tool, action, args, reason string
	createdAt                  int64
}

func (s *state) carriedProposals() ([]carriedProposal, error) {
	rows, err := s.db.Query(`SELECT id,tool,action,args,reason,created_at FROM cto_proposal_carry ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []carriedProposal
	for rows.Next() {
		var p carriedProposal
		if err := rows.Scan(&p.id, &p.tool, &p.action, &p.args, &p.reason, &p.createdAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// sendCarriedCTOProposals sends each kept proposal to the chief of staff, or
// to the owner's session when no chief is set. It comes from the CTO, which
// made the proposal, so the reply reaches the CTO. The idempotency key is made
// from this machine and the proposal ID: a send repeated after a crash, before
// the row was removed, gives the first message back. A row that cannot be
// sent stays for the next connect, and the log says why.
func (d *Daemon) sendCarriedCTOProposals(ctx context.Context) {
	items, err := d.st.carriedProposals()
	if err != nil {
		d.Logf("cto proposals carried: %v", err)
		return
	}
	if len(items) == 0 {
		return
	}
	cto := d.ctoSessionID()
	if cto == "" {
		d.Logf("cto proposals carried: %d wait for the CTO network session", len(items))
		return
	}
	var chief protocol.ChiefStatus
	raw, err := d.call(ctx, "chief.get", struct{}{})
	if err == nil {
		err = json.Unmarshal(raw, &chief)
	}
	if err != nil {
		d.Logf("cto proposals carried: chief: %v", err)
		return
	}
	to := chief.SessionID
	if to == "" {
		to = d.ownerSessionID()
	}
	if to == "" {
		d.Logf("cto proposals carried: %d wait: no chief and no owner session", len(items))
		return
	}
	for _, p := range items {
		_, err := d.ctoAttributedCall(ctx, "msg.send", protocol.MsgSend{
			FromCTO: true, Sender: cto, Recipient: to, Type: protocol.TypeRequest,
			Body: carriedProposalBody(p), IdempotencyKey: "cto_proposal_carry:" + d.cfg.NodeID + ":p_" + strconv.FormatInt(p.id, 10),
		})
		if err != nil {
			d.Logf("cto proposals carried: p_%d not sent, kept for the next connect: %v", p.id, err)
			return
		}
		if _, err := d.st.db.Exec(`DELETE FROM cto_proposal_carry WHERE id=?`, p.id); err != nil {
			d.Logf("cto proposals carried: p_%d: %v", p.id, err)
			return
		}
		d.Logf("cto proposals carried: p_%d sent to %s", p.id, to)
	}
}

// carriedProposalBody names what the proposal would do, on what, and why.
func carriedProposalBody(p carriedProposal) string {
	var b strings.Builder
	fmt.Fprintf(&b, "CTO proposal p_%d was waiting for the owner when agent-net removed stored CTO proposals (decision 0199). Nothing ran.\n", p.id)
	action := p.tool
	if p.action != "" {
		action += " " + p.action
	}
	fmt.Fprintf(&b, "Action: %s\n", action)
	fmt.Fprintf(&b, "Target: %s\n", strings.TrimSpace(p.args))
	fmt.Fprintf(&b, "Reason: %s\n", strings.TrimSpace(p.reason))
	if p.createdAt > 0 {
		fmt.Fprintf(&b, "Proposed: %s\n", time.Unix(0, p.createdAt).UTC().Format(time.RFC3339))
	}
	b.WriteString("Ask the owner. If he agrees, he tells the CTO in its chat, and the CTO makes the call again at once with his words quoted in owner_words. If he declines, nothing is left to do.")
	return b.String()
}
