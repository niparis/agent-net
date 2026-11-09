package gateway

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// OpenAppConsent creates an owner ask from a verified live OAuth or phone
// request. The CTO supplies the code; it never approves the request itself.
func (s *Store) OpenAppConsent(code, kind, name string) (protocol.Ask, error) {
	p, err := s.PendingByUserCode(code, "tui", s.now())
	if err != nil {
		return protocol.Ask{}, err
	}
	if kind == "" {
		kind = p.DefaultKind
	}
	if kind != "bot" && kind != "owner_app" {
		return protocol.Ask{}, reject("invalid app kind")
	}
	if p.ClientID == "phone" && kind != "owner_app" {
		return protocol.Ask{}, reject("phone pairing requires owner_app")
	}
	if strings.TrimSpace(name) == "" {
		name = p.ClientName
	}
	data, err := json.Marshal(map[string]string{"id": p.ID, "kind": kind, "name": name})
	if err != nil {
		return protocol.Ask{}, err
	}
	label := "bot app"
	if kind == "owner_app" {
		label = "owner app (full access)"
	}
	text := askFirstLine(fmt.Sprintf("Approve %s (code %s): %q?", label, p.UserCode, name), protocol.MaxOwnerAskChars)
	tx, err := s.db.Begin()
	if err != nil {
		return protocol.Ask{}, err
	}
	defer tx.Rollback()
	var pending bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM oauth_pending WHERE id=? AND status='pending' AND expires_at>?)`, p.ID, s.now().UnixNano()).Scan(&pending); err != nil {
		return protocol.Ask{}, err
	}
	if !pending {
		return protocol.Ask{}, reject("app request expired or resolved")
	}
	var previous string
	err = tx.QueryRow(`SELECT detail FROM inbox_items WHERE source_ref=? AND status='open'`, "app-consent:"+p.ID).Scan(&previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return protocol.Ask{}, err
	}
	// A changed name or app kind is a new consent decision. A click on the old
	// row must never approve permissions the owner did not see.
	if err == nil && previous != string(data) {
		if err := s.closeAskRef(tx, "app-consent:"+p.ID, protocol.AskReasonReplaced); err != nil {
			return protocol.Ask{}, err
		}
	}

	item, err := s.createInboxItem(tx, protocol.InboxItem{Kind: protocol.AskKindApproval, SessionID: gatewayAsker, Node: "apps", FromKind: protocol.AskFromApps, AskerName: p.ClientName, SourceRef: "app-consent:" + p.ID, Text: text, Recommendation: fmt.Sprintf("App: %s. Client: %s; redirect: %s. y approves; n refuses.", name, p.ClientName, p.RedirectURI), Detail: string(data), Options: []string{"approve", "refuse"}})
	if err != nil {
		return protocol.Ask{}, err
	}
	if err := tx.Commit(); err != nil {
		return protocol.Ask{}, err
	}
	return protocol.Ask{ID: item.ID, Kind: item.Kind, Ref: item.SourceRef, FromKind: protocol.AskFromApps, Question: item.Text, Options: item.Options, State: protocol.AskStateOpen, Next: protocol.AskNextOwner}, nil
}

func (s *Store) answerAppConsent(id, answer, owner string, fromCTO bool) (bool, error) {
	var ref, detail, status string
	err := s.db.QueryRow(`SELECT source_ref,detail,status FROM inbox_items WHERE id=? AND from_kind='apps' AND source_ref LIKE 'app-consent:%'`, id).Scan(&ref, &detail, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	if status != "open" {
		return true, reject("app consent already resolved")
	}
	if answer != "approve" && answer != "refuse" {
		return false, nil
	}
	if answer == "approve" && fromCTO {
		return true, reject("app consent requires the owner's own answer")
	}
	var p struct{ ID, Kind, Name string }
	if json.Unmarshal([]byte(detail), &p) != nil || ref != "app-consent:"+p.ID {
		return true, reject("invalid app consent target")
	}
	if answer == "refuse" {
		err = s.RefusePending(p.ID)
	} else {
		err = s.ApprovePending(p.ID, p.Kind, p.Name, s.now(), owner)
	}
	if err != nil {
		return true, err
	}
	// The ask closes later as resolved; the owner's decision is kept now.
	items, err := scanInboxRows(s.db.Query(`SELECT `+inboxColumns+` FROM inbox_items WHERE id=?`, id))
	if err != nil || len(items) != 1 {
		return true, err
	}
	return true, s.recordOwnerAnswer(s.db, items[0], answer)
}

func (s *Store) reconcileAppConsentAsks() error {
	rows, err := s.db.Query(`SELECT i.source_ref FROM inbox_items i LEFT JOIN oauth_pending p ON i.source_ref='app-consent:'||p.id WHERE i.status='open' AND i.from_kind='apps' AND i.source_ref LIKE 'app-consent:%' AND (p.id IS NULL OR p.status!='pending' OR p.expires_at<=?)`, s.now().UnixNano())
	if err != nil {
		return err
	}
	var refs []string
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			rows.Close()
			return err
		}
		refs = append(refs, ref)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, ref := range refs {
		if err := s.closeAskRef(s.db, ref, protocol.AskReasonResolved); err != nil {
			return err
		}
	}
	return nil
}
