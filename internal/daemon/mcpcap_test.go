package daemon

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestCapResult(t *testing.T) {
	small := []byte(`{"content":[{"type":"text","text":"ok"}]}`)
	if string(capResult(small, 1000)) != string(small) {
		t.Fatal("small result changed")
	}
	// Text is cut, a note is added, and the result stays under the cap.
	big, _ := json.Marshal(map[string]any{"content": []map[string]any{
		{"type": "text", "text": strings.Repeat("é", 5000)},
		{"type": "image", "data": strings.Repeat("A", 5000), "mimeType": "image/png"}},
		"structuredContent": map[string]string{"k": strings.Repeat("z", 5000)}})
	out := capResult(big, 4096)
	if len(out) > 4096 {
		t.Fatalf("%d bytes after cap", len(out))
	}
	var r struct {
		Content []map[string]any `json:"content"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatalf("capped result is not JSON: %v", err)
	}
	last := r.Content[len(r.Content)-1]["text"].(string)
	if !strings.Contains(last, "the result was cut") {
		t.Fatalf("no note: %v", r.Content)
	}
	if strings.Contains(string(out), "structuredContent") || strings.Contains(string(out), "AAAA") {
		t.Fatal("oversize parts kept")
	}
}

func TestRedactor(t *testing.T) {
	var r redactor
	r.add("Bearer abc-123-token")
	r.add("ab") // too short to be worth hiding
	for _, in := range []string{"Bearer abc-123-token", "token is abc-123-token here", `{"a":"abc-123-token"}`} {
		if got := r.apply(in); strings.Contains(got, "abc-123-token") {
			t.Errorf("%q -> %q", in, got)
		}
	}
	if got := r.apply("ab stays"); got != "ab stays" {
		t.Errorf("short value redacted: %q", got)
	}
	err := r.wrap(errors.New("Get https://x: header Bearer abc-123-token rejected"))
	if strings.Contains(err.Error(), "abc-123-token") {
		t.Errorf("wrap: %v", err)
	}
	var q redactor
	q.add(`quo"te-secret`)
	if got := string(q.applyBytes([]byte(`{"v":"quo\"te-secret"}`))); strings.Contains(got, "te-secret") {
		t.Errorf("json-escaped form kept: %s", got)
	}
}

func TestHeaderValue(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "tok")
	os.WriteFile(f, []byte("  tok-1234 \n"), 0o600)
	t.Setenv("MCP_TEST_HDR", "Token abc")
	cases := []struct {
		h    protocol.MCPHeader
		want string
	}{
		{protocol.MCPHeader{Name: "Authorization", File: f}, "Bearer tok-1234"},
		{protocol.MCPHeader{Name: "authorization", Env: "MCP_TEST_HDR"}, "Token abc"},
		{protocol.MCPHeader{Name: "X-Api-Key", File: f}, "tok-1234"},
	}
	for _, c := range cases {
		if got, err := headerValue(c.h); err != nil || got != c.want {
			t.Errorf("%+v -> %q, %v; want %q", c.h, got, err, c.want)
		}
	}
	// Errors name the source, never a value.
	_, err := headerValue(protocol.MCPHeader{Name: "Authorization", Env: "MCP_TEST_UNSET"})
	if err == nil || !strings.Contains(err.Error(), "MCP_TEST_UNSET") {
		t.Errorf("unset env: %v", err)
	}
	_, err = headerValue(protocol.MCPHeader{Name: "Authorization", File: filepath.Join(dir, "missing")})
	if err == nil || !strings.Contains(err.Error(), "cannot read the file") {
		t.Errorf("missing file: %v", err)
	}
}
