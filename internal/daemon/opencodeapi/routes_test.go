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
			got, err := r.Interrupt("a")
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
	_, err = r.Interrupt("a")
	if err == nil || !strings.Contains(err.Error(), "OpenCode 2.2.6 has no interrupt route") {
		t.Fatalf("unexpected error: %v", err)
	}
}
