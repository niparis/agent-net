package gateway

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"time"
)

const publicConsentStateKey = "apps_public_consent"

func (g *Gateway) openPublicConsent(link, question string) error {
	u, err := url.Parse(link)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return fmt.Errorf("invalid consent link")
	}
	if err := stateSet(g.store.db, publicConsentStateKey, link, g.store.now().UnixNano()); err != nil {
		return err
	}
	body := fmt.Sprintf("%s\nLink: %s\nInternet access resumes after approval.", question, link)
	if m, sent, err := g.store.sendToChief(g.store.db, agentNetSender, publicConsentStateKey, body); err == nil && sent {
		g.dispatch(context.Background(), m)
	}
	return nil
}

func (g *Gateway) clearPublicConsent() error {
	_, err := g.store.db.Exec(`DELETE FROM gateway_state WHERE key=?`, publicConsentStateKey)
	return err
}

func (g *Gateway) hasPublicConsent() bool {
	var link string
	err := g.store.db.QueryRow(`SELECT value FROM gateway_state WHERE key=?`, publicConsentStateKey).Scan(&link)
	return err == nil && link != "" && !errors.Is(err, sql.ErrNoRows)
}

// A successful login needs no second key press. Status refreshes and the
// gateway's regular tick retry the requested switch while consent is pending.
func (g *Gateway) retryPublicConsent(ctx context.Context) {
	if !g.hasPublicConsent() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	_, _ = g.publicOperation(ctx, "apps.public.on", nil)
}
