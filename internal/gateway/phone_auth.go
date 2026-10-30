package gateway

import (
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
)

const phoneOwnerKey = "phone_owner"

// SetPhoneOwner records the identity once. Repeating the same value is harmless.
func (s *Store) SetPhoneOwner(login string) error {
	if login == "" || strings.TrimSpace(login) != login || strings.ContainsAny(login, "\r\n") {
		return errors.New("invalid phone owner login")
	}
	_, err := s.db.Exec(`INSERT INTO owner_settings (key, value, updated_at) VALUES (?, ?, ?) ON CONFLICT (key) DO NOTHING`, phoneOwnerKey, login, s.now().UnixNano())
	if err != nil {
		return err
	}
	owner, err := s.PhoneOwner()
	if err != nil {
		return err
	}
	if owner != login {
		return fmt.Errorf("phone owner is already set")
	}
	return nil
}

func (s *Store) PhoneOwner() (string, error) {
	var login string
	err := s.db.QueryRow(`SELECT value FROM owner_settings WHERE key = ?`, phoneOwnerKey).Scan(&login)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return login, err
}

func phoneAuth(s *Store, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil || host != "127.0.0.1" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		owner, err := s.PhoneOwner()
		values := r.Header.Values("Tailscale-User-Login")
		if err != nil || owner == "" || len(values) != 1 || values[0] != owner {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
