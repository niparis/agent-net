package gateway

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

const publicConsentRef = "apps-public-consent"

func (g *Gateway) openPublicConsent(link, question string) error {
	u, err := url.Parse(link)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return fmt.Errorf("invalid consent link")
	}
	item, err := g.store.createInboxItem(g.store.db, protocol.InboxItem{Kind: protocol.AskKindQuestion, SessionID: gatewayAsker, Node: "apps", FromKind: protocol.AskFromApps, AskerName: "Tailscale", SourceRef: publicConsentRef, Text: question, Recommendation: "y opens " + link + ". Internet access resumes after approval.", Detail: link, Options: []string{"open link", "cancel"}})
	if err != nil {
		return err
	}
	_, err = g.store.db.Exec(`UPDATE inbox_items SET text=?,detail=? WHERE id=? AND status='open'`, question, link, item.ID)
	return err
}

func (g *Gateway) hasPublicConsent() bool {
	var open bool
	err := g.store.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM inbox_items WHERE source_ref=? AND status='open')`, publicConsentRef).Scan(&open)
	return err == nil && open
}

// A successful login needs no second key press. Status refreshes and the
// gateway's regular tick retry the requested switch while its ask is open.
func (g *Gateway) retryPublicConsent(ctx context.Context) {
	if !g.hasPublicConsent() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	_, _ = g.publicOperation(ctx, "apps.public.on", nil)
}
