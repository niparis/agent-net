package opencodeapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDetectInterruptRoutes(t *testing.T) {
	tests := []struct{ name, spec, want string }{
		{"new", `{"info":{"version":"2.2.6"},"paths":{"/api/session/{sessionID}/interrupt":{"post":{}}}}`, "/api/session/a/interrupt"},
		{"old", `{"info":{"version":"2.2.5"},"paths":{"/api/session/{sessionID}/abort":{"post":{}}}}`, "/api/session/a/abort"},
		{"no spec", "", "/api/session/a/abort"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := Detect(strings.NewReader(tt.spec), "2.2.6")
			if err != nil {
				t.Fatal(err)
			}
			got, err := r.Path(Interrupt, "a")
			if err != nil || got != tt.want {
				t.Fatalf("got %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

func TestMissingInterrupt(t *testing.T) {
	r, err := Detect(strings.NewReader(`{"info":{"version":"2.2.6"},"paths":{}}`), "2.2.6")
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.Path(Interrupt, "a")
	if err == nil || !strings.Contains(err.Error(), "OpenCode 2.2.6 has no interrupt route") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOperations(t *testing.T) {
	r, err := Detect(strings.NewReader(`{"paths":{"/api/info":{"get":{}},"/api/session/{sessionID}/message":{"get":{}},"/api/session/{sessionID}":{"delete":{}}}}`), "2.2.6")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		op       Operation
		id, want string
	}{{Info, "", "/api/info"}, {Messages, "a b", "/api/session/a%20b/message"}, {DeleteSession, "x", "/api/session/x"}} {
		got, err := r.Path(tc.op, tc.id)
		if err != nil || got != tc.want {
			t.Errorf("%s: %q %v", tc.op, got, err)
		}
	}
	if _, err := r.Path(Prompt, "x"); err == nil || !strings.Contains(err.Error(), "OpenCode 2.2.6 has no prompt route") {
		t.Fatalf("missing prompt: %v", err)
	}
}

func TestDiscover(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		body, want string
	}{
		{"new", 200, `{"paths":{"/api/session/{sessionID}/interrupt":{"post":{}}}}`, "/api/session/s/interrupt"},
		{"old", 200, `{"paths":{"/api/session/{sessionID}/abort":{"post":{}}}}`, "/api/session/s/abort"},
		{"absent", 404, "", "/api/session/s/abort"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path != "/openapi.json" {
					t.Errorf("path %s", req.URL.Path)
				}
				u, p, ok := req.BasicAuth()
				if !ok || u != "opencode" || p != "secret" {
					t.Error("missing authentication")
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			routes, err := Discover(context.Background(), server.Client(), server.URL, "secret", "2.2.6")
			if err != nil {
				t.Fatal(err)
			}
			got, err := routes.Path(Interrupt, "s")
			if err != nil || got != tc.want {
				t.Fatalf("%s: %q %v", tc.name, got, err)
			}
		})
	}
}

func TestCacheServiceIdentity(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; fmt.Fprint(w, `{"paths":{}}`) }))
	defer server.Close()
	var cache Cache
	for _, identity := range []string{"start-1", "start-1", "start-2"} {
		if _, err := cache.Discover(context.Background(), server.Client(), server.URL, "", "2.2.6", identity); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatalf("got %d discoveries, want 2", calls)
	}
}

func TestMissingOperations(t *testing.T) {
	r, err := Detect(strings.NewReader(`{"paths":{}}`), "2.2.6")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, op := range r.Missing() {
		if op == Interrupt {
			found = true
		}
	}
	if !found {
		t.Fatal("missing interrupt absent from inventory")
	}
}

// The permission reply route exists only when the service lists it (2.3), and
// its request ID is escaped.
func TestPermissionReplyRoute(t *testing.T) {
	r, err := Detect(strings.NewReader(`{"paths":{"/api/session/{sessionID}/permission/{requestID}/reply":{"post":{}}}}`), "2.3.0")
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.PermissionReplyPath("ses_1", "per 1")
	if want := "/api/session/ses_1/permission/per%201/reply"; err != nil || got != want {
		t.Fatalf("got %q, %v; want %q", got, err, want)
	}
	for name, spec := range map[string]string{"older service": `{"paths":{}}`, "no document": ""} {
		r, err := Detect(strings.NewReader(spec), "2.2.6")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.PermissionReplyPath("ses_1", "per_1"); err == nil || !strings.Contains(err.Error(), "has no reply to permission request route") {
			t.Errorf("%s: %v", name, err)
		}
	}
}
