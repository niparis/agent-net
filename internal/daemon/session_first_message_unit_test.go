package daemon

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// Who a first message goes out from. Only the owner's own words go out as the
// owner; nothing falls back to the owner. This needs no gateway and no socket.
func TestFirstMessageSender(t *testing.T) {
	state := t.TempDir()
	d, err := New(Config{StateDir: state, SocketPath: filepath.Join(state, "unused.sock")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.st.close() })
	d.sessions[key(protocol.OwnerHarness, protocol.OwnerNativeID)] = &localSession{sessionID: "s_owner"}
	d.sessions[key("claude", "peer-native")] = &localSession{sessionID: "s_peer"}
	if _, err := d.st.db.Exec(`INSERT INTO cto_network(id,session_id) VALUES(1,'s_cto')`); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		m       startFirstMessage
		want    string
		wantErr string
	}{
		{"the owner wrote it", startFirstMessage{Text: "hi", ByOwner: true}, "s_owner", ""},
		{"the CTO wrote it", startFirstMessage{Text: "hi", From: "s_cto"}, "s_cto", ""},
		{"a session of this machine wrote it", startFirstMessage{Text: "hi", From: "s_peer"}, "s_peer", ""},
		{"nobody known wrote it", startFirstMessage{Text: "hi"}, "", "does not know who wrote it"},
		{"its writer is on another machine", startFirstMessage{Text: "hi", From: "s_far"}, "", "has no session on this machine"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := d.firstMessageSender(tc.m)
			if got != tc.want || (tc.wantErr == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("sender %q, error %v; want %q, error %q", got, err, tc.want, tc.wantErr)
			}
		})
	}
}
