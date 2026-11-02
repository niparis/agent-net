package gateway

import (
	"database/sql"
	"encoding/base64"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAppsCreateListRevoke(t *testing.T) {
	s, c := newTestStore(t)
	uris := []string{"https://example.test/callback", "http://localhost:8787/callback"}
	app, err := s.CreateApp("test bot", "bot", "client", "Test client", uris)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(app.ID, "app_") || app.Name != "test bot" || app.Kind != "bot" || app.ClientID != "client" || app.ClientName != "Test client" || !reflect.DeepEqual(app.RedirectURIs, uris) || !app.CreatedAt.Equal(c.t) || app.LastUsedAt != nil || app.RevokedAt != nil || app.Grants == nil || len(app.Grants) != 0 {
		t.Fatalf("app = %+v", app)
	}
	if _, err := s.CreateApp("test bot", "owner_app", "", "", nil); err == nil {
		t.Fatal("duplicate active name accepted")
	}
	if _, err := s.CreateApp("invalid", "agent", "", "", nil); err == nil {
		t.Fatal("invalid kind accepted")
	}
	access, err := s.IssueToken(app.ID, "access", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	refresh, err := s.IssueToken(app.ID, "refresh", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeApp(app.ID); err != nil {
		t.Fatal(err)
	}
	for _, token := range []struct{ raw, kind string }{{access, "access"}, {refresh, "refresh"}} {
		if _, err := s.AppForToken(token.raw, token.kind, c.t); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("revoked lookup: %v", err)
		}
	}
	if _, err := s.IssueToken(app.ID, "access", time.Hour); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("issued to revoked app: %v", err)
	}
	if _, _, err := s.RotateRefresh(refresh, c.t); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("rotated revoked app: %v", err)
	}
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM app_tokens WHERE app_id = ?", app.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("tokens remain: %d, %v", count, err)
	}
	replacement, err := s.CreateApp("test bot", "owner_app", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	apps, err := s.ListApps()
	if err != nil || len(apps) != 2 {
		t.Fatalf("apps = %+v, %v", apps, err)
	}
	for _, got := range apps {
		if got.ID == app.ID && (got.RevokedAt == nil || !got.RevokedAt.Equal(c.t)) {
			t.Fatalf("revocation = %+v", got)
		}
		if got.ID == replacement.ID && (got.Kind != "owner_app" || got.RedirectURIs == nil) {
			t.Fatalf("replacement = %+v", got)
		}
	}
}

func TestRenameAppAndBotSession(t *testing.T) {
	s, _ := newTestStore(t)
	a, err := s.CreateApp("old bot", "bot", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.touchBot(a); err != nil {
		t.Fatal(err)
	}
	if err = s.RenameApp(a.ID, "new bot"); err != nil {
		t.Fatal(err)
	}
	var name string
	if err = s.db.QueryRow(`SELECT name FROM sessions WHERE harness='bot' AND native_id=?`, a.ID).Scan(&name); err != nil || name != "new bot" {
		t.Fatalf("session name %q: %v", name, err)
	}
	b, err := s.CreateApp("other", "bot", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RenameApp(a.ID, b.Name); err == nil || err.Error() != "name in use" {
		t.Fatalf("duplicate rename: %v", err)
	}
}

func TestAppTokensHashExpiryAndCleanup(t *testing.T) {
	s, c := newTestStore(t)
	app, err := s.CreateApp("bot", "bot", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := s.IssueToken(app.ID, "access", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	bytes, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(bytes) != 32 {
		t.Fatalf("token is not 256-bit URL-safe base64: %v", err)
	}
	var hash, kind string
	var created, expires int64
	if err := s.db.QueryRow("SELECT hash, type, created_at, expires_at FROM app_tokens").Scan(&hash, &kind, &created, &expires); err != nil {
		t.Fatal(err)
	}
	if hash != hashHex(raw) || hash == raw || kind != "access" || created != c.t.UnixNano() || expires != c.t.Add(time.Hour).UnixNano() {
		t.Fatal("wrong stored token metadata")
	}
	now := c.t.Add(time.Minute)
	got, err := s.AppForToken(raw, "access", now)
	if err != nil || got.LastUsedAt == nil || !got.LastUsedAt.Equal(now) {
		t.Fatalf("lookup = %+v, %v", got, err)
	}
	apps, err := s.ListApps()
	if err != nil || apps[0].LastUsedAt == nil || !apps[0].LastUsedAt.Equal(now) {
		t.Fatalf("last use not stored: %+v, %v", apps, err)
	}
	for _, test := range []struct {
		raw, kind string
		now       time.Time
	}{{raw, "refresh", now}, {"unknown", "access", now}, {raw, "access", c.t.Add(time.Hour)}} {
		if _, err := s.AppForToken(test.raw, test.kind, test.now); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("invalid lookup succeeded: %v", err)
		}
	}
	for _, test := range []struct {
		id, kind string
		ttl      time.Duration
	}{{app.ID, "device", time.Hour}, {app.ID, "access", 0}, {app.ID, "refresh", -time.Second}, {"missing", "access", time.Hour}} {
		if _, err := s.IssueToken(test.id, test.kind, test.ttl); err == nil {
			t.Fatalf("invalid issue accepted: %+v", test)
		}
	}
	live, err := s.IssueToken(app.ID, "refresh", 2*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteExpiredTokens(c.t.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM app_tokens").Scan(&count); err != nil || count != 1 {
		t.Fatalf("cleanup count = %d, %v", count, err)
	}
	if _, err := s.AppForToken(live, "refresh", c.t.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
}

func TestAppTokenRefreshRotation(t *testing.T) {
	s, c := newTestStore(t)
	app, err := s.CreateApp("bot", "bot", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	old, err := s.IssueToken(app.ID, "refresh", 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	now := c.t.Add(time.Minute)
	access, refresh, err := s.RotateRefresh(old, now)
	if err != nil {
		t.Fatal(err)
	}
	if access == refresh || refresh == old {
		t.Fatal("rotation reused a token")
	}
	for _, pair := range []struct {
		raw, kind string
		ttl       time.Duration
	}{{access, "access", time.Hour}, {refresh, "refresh", 90 * 24 * time.Hour}} {
		if _, err := s.AppForToken(pair.raw, pair.kind, now); err != nil {
			t.Fatal(err)
		}
		if _, err := s.AppForToken(pair.raw, pair.kind, now.Add(pair.ttl)); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("expiry: %v", err)
		}
	}
	if _, _, err := s.RotateRefresh(old, now); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("reused refresh accepted: %v", err)
	}
	if _, _, err := s.RotateRefresh(access, now); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("access accepted as refresh: %v", err)
	}
	if _, _, err := s.RotateRefresh(refresh, now.Add(90*24*time.Hour)); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expired refresh accepted: %v", err)
	}
	if _, err := s.AppForToken(old, "refresh", now); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("old refresh remains: %v", err)
	}
}

func TestAppTokenConcurrentRefresh(t *testing.T) {
	s, c := newTestStore(t)
	app, err := s.CreateApp("bot", "bot", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := s.IssueToken(app.ID, "refresh", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() { _, _, err := s.RotateRefresh(raw, c.t); results <- err })
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else if !errors.Is(err, sql.ErrNoRows) {
			t.Fatal(err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful rotations = %d", successes)
	}
}

// Re-running migrations preserves app tokens and existing node data.
func TestAppMigration(t *testing.T) {
	s, c := newTestStore(t)
	node, token, err := s.CreateNode("test-host", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := s.NodeByToken(token); err != nil || !ok || got != node {
		t.Fatalf("existing node lost: %q, %v, %v", got, ok, err)
	}
	app, err := s.CreateApp("bot", "bot", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := s.IssueToken(app.ID, "access", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(); err != nil {
		t.Fatal(err)
	}
	if got, err := s.AppForToken(raw, "access", c.t); err != nil || got.ID != app.ID {
		t.Fatalf("app lost on replay: %+v, %v", got, err)
	}
	// Even a token row left behind cannot bypass a revoked app.
	if _, err := s.db.Exec("UPDATE apps SET revoked_at = ? WHERE id = ?", c.t.UnixNano(), app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppForToken(raw, "access", c.t); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("revoked app accepted: %v", err)
	}
}
