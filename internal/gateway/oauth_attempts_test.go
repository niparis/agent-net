package gateway

import (
	"database/sql"
	"errors"
	"testing"
	"time"
)

// registerFrom asks for one client from a source at a time. It returns the
// limit error, or nil when the registration is allowed.
func registerFrom(t *testing.T, s *Store, source string, now time.Time) error {
	t.Helper()
	_, err := s.registerOAuthClient(oauthClient{RedirectURIs: []string{oauthCallback}, GrantTypes: []string{"authorization_code"}, ResponseTypes: []string{"code"}}, now, source)
	return err
}

// Five code misses from one source in ten minutes block that source. The
// tenth minute is the first moment the misses no longer count.
func TestOAuthCodeMissWindowIsExactlyTenMinutes(t *testing.T) {
	s, c, _ := oauthFixture(t)
	start := c.t
	for range 4 {
		if _, err := s.PendingByUserCode("WRONGX", "one", start); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("miss: %v", err)
		}
	}
	if _, err := s.PendingByUserCode("WRONGX", "one", start); !errors.Is(err, ErrOAuthTooManyTries) {
		t.Fatalf("fifth miss: %v", err)
	}
	if _, err := s.PendingByUserCode("WRONGX", "one", start.Add(10*time.Minute-time.Nanosecond)); !errors.Is(err, ErrOAuthTooManyTries) {
		t.Fatalf("one nanosecond before the window ends: %v", err)
	}
	if _, err := s.PendingByUserCode("WRONGX", "one", start.Add(10*time.Minute)); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("window over: %v", err)
	}
}

// Five registrations from one source in an hour block that source. The hour is
// exact: a registration at the hour's end is allowed.
func TestOAuthRegistrationWindowIsExactlyOneHour(t *testing.T) {
	s, c, _ := oauthFixture(t)
	start := c.t
	for range 5 {
		if err := registerFrom(t, s, "192.0.2.1", start); err != nil {
			t.Fatal(err)
		}
	}
	if err := registerFrom(t, s, "192.0.2.1", start); err != errOAuthLimit {
		t.Fatalf("sixth registration: %v", err)
	}
	if err := registerFrom(t, s, "192.0.2.1", start.Add(time.Hour-time.Nanosecond)); err != errOAuthLimit {
		t.Fatalf("one nanosecond before the hour ends: %v", err)
	}
	if err := registerFrom(t, s, "192.0.2.1", start.Add(time.Hour)); err != nil {
		t.Fatalf("hour over: %v", err)
	}
}

// Code misses and registrations share a table, but each counts only its own
// kind. A source that has used up one limit keeps the other.
func TestOAuthCodeMissesAndRegistrationsCountSeparately(t *testing.T) {
	s, c, _ := oauthFixture(t)
	now := c.t
	for range 5 {
		if err := registerFrom(t, s, "192.0.2.1", now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.PendingByUserCode("WRONGX", "192.0.2.1", now); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("registrations counted as code misses: %v", err)
	}
	for range 5 {
		s.PendingByUserCode("WRONGX", "192.0.2.2", now)
	}
	if _, err := s.PendingByUserCode("WRONGX", "192.0.2.2", now); !errors.Is(err, ErrOAuthTooManyTries) {
		t.Fatalf("code misses did not reach the limit: %v", err)
	}
	if err := registerFrom(t, s, "192.0.2.2", now); err != nil {
		t.Fatalf("code misses counted as registrations: %v", err)
	}
}

// Each purge clears only its own kind. A code-miss check purges misses older
// than ten minutes and must keep the registrations, which last an hour.
func TestOAuthPurgesClearOnlyTheirOwnKind(t *testing.T) {
	s, c, _ := oauthFixture(t)
	start := c.t
	for range 5 {
		if err := registerFrom(t, s, "192.0.2.1", start); err != nil {
			t.Fatal(err)
		}
	}
	for range 5 {
		s.PendingByUserCode("WRONGX", "one", start)
	}
	later := start.Add(11 * time.Minute)
	if _, err := s.PendingByUserCode("WRONGX", "two", later); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("other source: %v", err)
	}
	if err := registerFrom(t, s, "192.0.2.1", later); err != errOAuthLimit {
		t.Fatalf("registrations of eleven minutes ago must still count: %v", err)
	}
	if err := s.CleanupOAuth(later); err != nil {
		t.Fatal(err)
	}
	if err := registerFrom(t, s, "192.0.2.1", later); err != errOAuthLimit {
		t.Fatalf("cleanup removed registrations younger than an hour: %v", err)
	}
	if _, err := s.PendingByUserCode("WRONGX", "one", later); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("code misses older than ten minutes still count: %v", err)
	}
}
