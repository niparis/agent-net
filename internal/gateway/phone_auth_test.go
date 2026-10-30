package gateway

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestPhoneOwnerAndAuthentication(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SetPhoneOwner("owner@example.test"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPhoneOwner("other@example.test"); err == nil {
		t.Fatal("owner changed")
	}
	h := phoneAuth(s, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	for _, tt := range []struct {
		remote, login string
		want          int
	}{
		{"127.0.0.1:1234", "", 403}, {"127.0.0.1:1234", "other@example.test", 403}, {"192.0.2.1:1234", "owner@example.test", 403}, {"127.0.0.1:1234", "owner@example.test", 204},
	} {
		r := httptest.NewRequest("GET", "http://localhost/", nil)
		r.RemoteAddr = tt.remote
		r.Header.Set("Tailscale-User-Login", tt.login)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tt.want {
			t.Errorf("%s %q: got %d, want %d", tt.remote, tt.login, w.Code, tt.want)
		}
	}
}
