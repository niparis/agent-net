package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func openCodeRoleFixture(t *testing.T) (*workerNode, string) {
	t.Helper()
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	g := startGateway(t, t.TempDir(), "")
	n := startWorkerNode(t, g, shortSet, func(c *Config) { c.HarnessPaths = map[string]string{"opencode": os.Args[0]} })
	return n, state
}

// OpenCode stores these rules on the session, so they survive a wake and do
// not change any other session in the owner's shared service.
func TestOpenCodeRoleCreationHasUnattendedPermission(t *testing.T) {
	n, state := openCodeRoleFixture(t)
	for _, role := range []string{"chief", "merger", ""} {
		t.Run(role, func(t *testing.T) {
			dir := t.TempDir()
			var permissions []ctoPermission
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != "/api/session" {
					http.NotFound(w, r)
					return
				}
				var body struct {
					Permissions []ctoPermission `json:"permissions"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				permissions = body.Permissions
				id := "ses_" + role
				if role == "" {
					id += "ordinary"
				}
				helloOpenCode(t, n.node, id, dir)
				encoded, _ := json.Marshal(body.Permissions)
				fmt.Fprintf(w, `{"data":{"id":%q,"permissions":%s}}`, id, encoded)
			}))
			defer server.Close()
			writeOwnerOpenCodeService(t, state, server.URL)
			r := protocol.StartedSession{ID: "start_" + role, Harness: "opencode", Type: role, Path: dir}
			if _, err := n.d.startOwnerOpenCode(context.Background(), r, "", "", "", false, false, false); err != nil {
				t.Fatal(err)
			}
			if role == "" {
				if len(permissions) != 0 {
					t.Fatalf("ordinary session rules = %v", permissions)
				}
				return
			}
			if len(permissions) != 1 || permissions[0] != (ctoPermission{Action: "*", Resource: "*", Effect: "allow"}) {
				t.Fatalf("%s session rules = %v", role, permissions)
			}
		})
	}
}

func TestOpenCodeRoleCreationRequiresConfirmedPermissions(t *testing.T) {
	n, state := openCodeRoleFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/api/session" {
			fmt.Fprint(w, `{"data":{"id":"ses_ignored"}}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	writeOwnerOpenCodeService(t, state, server.URL)
	_, err := n.d.startOwnerOpenCode(context.Background(), protocol.StartedSession{ID: "start_chief", Type: "chief", Harness: "opencode", Path: t.TempDir()}, "", "", "", false, false, false)
	if err == nil || !strings.Contains(err.Error(), "did not confirm") {
		t.Fatalf("start error = %v, want the missing permission rule", err)
	}
}

func openCodeRoleResumeFixture(t *testing.T, n *workerNode, state, dir string) (string, func()) {
	t.Helper()
	var mu sync.Mutex
	var permissions []ctoPermission
	gets := 0
	patches := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/session/ses_wake":
			mu.Lock()
			gets++
			encoded, _ := json.Marshal(permissions)
			mu.Unlock()
			fmt.Fprintf(w, `{"data":{"id":"ses_wake","permissions":%s}}`, encoded)
		case "PATCH /api/session/ses_wake":
			var body struct {
				Permissions []ctoPermission `json:"permissions"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			mu.Lock()
			permissions = body.Permissions
			patches++
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		case "GET /api/location":
			helloOpenCode(t, n.node, "ses_wake", dir)
			fmt.Fprint(w, `{"data":{}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	writeOwnerOpenCodeService(t, state, server.URL)
	_, sid := helloOpenCode(t, n.node, "ses_wake", dir)
	return sid, func() {
		t.Helper()
		mu.Lock()
		defer mu.Unlock()
		if gets != 2 || patches != 1 || len(permissions) != 1 || permissions[0] != (ctoPermission{Action: "*", Resource: "*", Effect: "allow"}) {
			t.Fatalf("reads = %d, patches = %d, rules = %v", gets, patches, permissions)
		}
	}
}

func TestOpenCodeRoleWakeAddsUnattendedPermission(t *testing.T) {
	n, state := openCodeRoleFixture(t)
	dir := t.TempDir()
	sid, check := openCodeRoleResumeFixture(t, n, state, dir)
	a := protocol.AgentView{SessionID: sid, Type: "merger", Harness: "opencode", NativeID: "ses_wake", Cwd: dir}
	if err := n.d.wakeOpenCode(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	check()
}

func TestOpenCodeRoleResumeAddsUnattendedPermission(t *testing.T) {
	n, state := openCodeRoleFixture(t)
	dir := t.TempDir()
	sid, check := openCodeRoleResumeFixture(t, n, state, dir)
	a := protocol.AgentView{SessionID: sid, Type: "merger", NodeID: n.d.NodeID(), Harness: "opencode", NativeID: "ses_wake", Cwd: dir}
	if _, err := n.d.resumeAgent(context.Background(), a, "", ""); err != nil {
		t.Fatal(err)
	}
	check()
}
