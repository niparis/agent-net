package opencodeapi

import (
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
