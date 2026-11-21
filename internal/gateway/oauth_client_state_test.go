package gateway

import (
	"database/sql"
	"errors"
	"net/url"
	"testing"
	"time"
)

// These tests describe the sign-in rules that must not change when the client
// record moves into the apps table (owner review of 2026-11-21). They talk to
// the store and the HTTP handler only, so they hold before and after.

// A client made by dynamic registration is not an app. It cannot start a
// session, get tokens or show in the owner's list until the owner approves it.
func TestRegisteredClientCannotGetTokensBeforeApproval(t *testing.T) {
	s, c, h := oauthFixture(t)
	client := oauthRegister(t, h)

	// The owner approves nothing. A made-up code and verifier get no tokens.
	if w := oauthToken(h, oauthExchange("not-a-real-code", client, oauthVerifier)); w.Code != 400 || oauthJSON(t, w)["error"] != "invalid_grant" {
		t.Fatalf("exchange before approval: %d %s", w.Code, w.Body.String())
	}
	if w := oauthToken(h, url.Values{"grant_type": {"refresh_token"}, "client_id": {client}, "refresh_token": {"not-a-token"}}); w.Code != 400 {
		t.Fatalf("refresh before approval: %d %s", w.Code, w.Body.String())
	}
	// The authorize page works, because it starts the approval. A request that
	// is still pending gives no code.
	_, id := oauthPage(t, oauthAuthorize(h, client, nil))
	if st := oauthStatus(t, h, id); st["status"] != "pending" || st["redirect"] != nil {
		t.Fatalf("pending request status: %v", st)
	}
	apps, err := s.ListApps()
	if err != nil || len(apps) != 0 {
		t.Fatalf("apps before approval: %+v %v", apps, err)
	}
	var tokens int
	if err := s.db.QueryRow(`SELECT count(*) FROM app_tokens`).Scan(&tokens); err != nil || tokens != 0 {
		t.Fatalf("tokens before approval: %d %v", tokens, err)
	}

	if err := s.ApprovePending(id, "bot", "approved bot", c.t); err != nil {
		t.Fatal(err)
	}
	apps, err = s.ListApps()
	if err != nil || len(apps) != 1 || apps[0].Name != "approved bot" || apps[0].ClientID != client || apps[0].ClientName != "Cursor" || apps[0].Kind != "bot" || len(apps[0].RedirectURIs) != 2 {
		t.Fatalf("apps after approval: %+v %v", apps, err)
	}
	code := oauthRedirectCode(t, oauthStatus(t, h, id))
	if w := oauthToken(h, oauthExchange(code, client, oauthVerifier)); w.Code != 200 {
		t.Fatalf("exchange after approval: %d %s", w.Code, w.Body.String())
	}
}

// A client nobody approves is forgotten after one hour. An approved client is
// kept, and the owner can approve it again after revoking the app.
func TestUnapprovedClientIsSweptAfterAnHour(t *testing.T) {
	s, c, h := oauthFixture(t)
	forgotten := oauthRegister(t, h)
	kept := oauthRegister(t, h)
	_, id := oauthPage(t, oauthAuthorize(h, kept, nil))
	if err := s.ApprovePending(id, "bot", "kept", c.t); err != nil {
		t.Fatal(err)
	}

	c.advance(59 * time.Minute)
	if err := s.CleanupOAuth(c.t); err != nil {
		t.Fatal(err)
	}
	if w := oauthAuthorize(h, forgotten, nil); w.Code != 200 {
		t.Fatalf("client swept before an hour: %d", w.Code)
	}
	c.advance(2 * time.Minute)
	if err := s.CleanupOAuth(c.t); err != nil {
		t.Fatal(err)
	}
	if w := oauthAuthorize(h, forgotten, nil); w.Code != 400 {
		t.Fatalf("client not swept after an hour: %d %s", w.Code, w.Body.String())
	}
	if w := oauthAuthorize(h, kept, nil); w.Code != 200 {
		t.Fatalf("approved client swept: %d %s", w.Code, w.Body.String())
	}

	// Revoking the app keeps the client, so it can ask again.
	apps, err := s.ListApps()
	if err != nil || len(apps) != 1 {
		t.Fatalf("apps: %+v %v", apps, err)
	}
	if err := s.RevokeApp(apps[0].ID); err != nil {
		t.Fatal(err)
	}
	c.advance(24 * time.Hour)
	if err := s.CleanupOAuth(c.t); err != nil {
		t.Fatal(err)
	}
	_, id = oauthPage(t, oauthAuthorize(h, kept, nil))
	if err := s.ApprovePending(id, "bot", "kept again", c.t); err != nil {
		t.Fatal(err)
	}
	apps, err = s.ListApps()
	if err != nil || len(apps) != 2 || apps[0].RevokedAt == nil || apps[1].RevokedAt != nil || apps[1].ClientID != kept {
		t.Fatalf("apps after second approval: %+v %v", apps, err)
	}
}

// chainRotations rotates a refresh token n times, one minute apart, and
// returns the last refresh token and the time of the last rotation.
func chainRotations(t *testing.T, s *Store, refresh string, start time.Time, n int) (string, time.Time) {
	t.Helper()
	now := start
	for i := 0; i < n; i++ {
		_, next, err := s.RotateRefresh(refresh, now)
		if err != nil {
			t.Fatalf("rotation %d at %v: %v", i, now, err)
		}
		refresh = next
		now = now.Add(time.Minute)
	}
	return refresh, now
}

// One token family may rotate 200 times in 24 hours. The 201st is refused and
// the family is revoked. Rotations from an earlier day do not count.
func TestRefreshRotationLimitIsPerFamilyAndPerDay(t *testing.T) {
	s, c := newTestStore(t)
	app, err := s.CreateApp("rotating", "bot", "client", "Client", nil)
	if err != nil {
		t.Fatal(err)
	}
	refresh, err := s.IssueToken(app.ID, "refresh", 90*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	// 150 rotations, then 50 more an hour later, make 200 in one day.
	refresh, _ = chainRotations(t, s, refresh, c.t, 150)
	later := c.t.Add(time.Hour)
	refresh, now := chainRotations(t, s, refresh, later, 50)
	if _, _, err := s.RotateRefresh(refresh, now); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("201st rotation in a day: %v", err)
	}
	var live int
	if err := s.db.QueryRow(`SELECT count(*) FROM app_tokens WHERE app_id=? AND used_at IS NULL`, app.ID).Scan(&live); err != nil || live != 0 {
		t.Fatalf("family not revoked at the limit: %d live tokens, %v", live, err)
	}

	// Another family of the same app is not touched by that count.
	other, err := s.IssueToken(app.ID, "refresh", 90*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RotateRefresh(other, now); err != nil {
		t.Fatalf("other family refused: %v", err)
	}

	// A day after the first rotations, 150 more fit in a fresh family chain.
	next, err := s.IssueToken(app.ID, "refresh", 90*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	next, now = chainRotations(t, s, next, c.t, 150)
	next, now = chainRotations(t, s, next, now.Add(25*time.Hour), 150)
	if _, _, err := s.RotateRefresh(next, now); err != nil {
		t.Fatalf("rotation after the window moved on: %v", err)
	}
}
