package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// fakeOpenCodeService is the part of OpenCode 2.3 that matters here: the
// OpenAPI document that lists the permission reply route, and the route itself.
// The route and its body were read from the OpenAPI document of a real 2.3.0
// service (POST /api/session/{sessionID}/permission/{requestID}/reply with
// {"decision": "once" | "always" | "reject"}, answered 204).
type fakeOpenCodeService struct {
	srv     *httptest.Server
	mu      sync.Mutex
	replies []string // "session/request decision"
	spec    string
}

func newFakeOpenCodeService(t *testing.T, spec string) *fakeOpenCodeService {
	t.Helper()
	f := &fakeOpenCodeService{spec: spec}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, pass, ok := r.BasicAuth(); !ok || pass != "test-owner-password" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/openapi.json" {
			w.Write([]byte(f.spec))
			return
		}
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if r.Method == http.MethodPost && len(parts) == 6 && parts[0] == "api" && parts[1] == "session" && parts[3] == "permission" && parts[5] == "reply" {
			var body struct {
				Decision string `json:"decision"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			f.mu.Lock()
			f.replies = append(f.replies, parts[2]+"/"+parts[4]+" "+body.Decision)
			f.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeOpenCodeService) got() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.replies...)
}

const openCode23Spec = `{"paths":{"/api/session/{sessionID}/permission/{requestID}/reply":{"post":{}}}}`

// An OpenCode session whose role node holds merge over the repo: the plugin
// forwards a permission request, the daemon says allow and answers "once" on
// the session's own service. A command no scope covers, a command on the never
// list, and a service without the reply route are left alone: nothing is sent,
// so the request stays with the owner as before.
func TestOpenCodePermissionIsAnsweredOnceOnlyWhenAScopeAllowsIt(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{exe: "/opt/x/node_modules/@opencode/cli/bin/opencode.exe"})
	if _, err := n.d.call(t.Context(), "hello", map[string]any{"hostname": "fixture", "version": "v0.4.145", "graph_refs": true}); err != nil {
		t.Fatal(err)
	}
	waitGraphCutover(t, g)
	repo := newRepo(t, "shop")
	if _, err := g.gw.Store().AddProjects(n.d.cfg.NodeID, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: "shop", Path: repo}}}); err != nil {
		t.Fatal(err)
	}
	oc, ocID := helloOpenCode(t, n, "ses_1", repo)
	view, ok, err := g.gw.Store().Agent(ocID)
	if err != nil || !ok || view.AgentID == "" {
		t.Fatalf("the session has no role node: %+v %v", view, err)
	}
	if _, err := g.gw.Store().SetScope(protocol.ScopeSetRequest{Node: view.AgentID, Kind: protocol.ScopeMerge, Over: "repo:shop"}, "owner", false); err != nil {
		t.Fatal(err)
	}
	service := newFakeOpenCodeService(t, openCode23Spec)
	writeOwnerOpenCodeService(t, state, service.srv.URL)

	decide := func(command, request string) protocol.ToolDecideResult {
		t.Helper()
		raw, _ := json.Marshal(map[string]string{"command": command})
		var res protocol.ToolDecideResult
		out, err := oc.call(protocol.MethodToolDecide, protocol.ToolDecideRequest{Tool: "Bash", Input: raw, Folder: repo, RequestID: request, RequestSession: "ses_1"})
		if err != nil {
			t.Fatal(err)
		}
		json.Unmarshal(out, &res)
		return res
	}
	eventually(t, "allow after the scope reaches the daemon", func() bool { return decide("git push origin main", "per_1").Allow })
	if got := service.got(); len(got) == 0 || got[len(got)-1] != "ses_1/per_1 once" {
		t.Fatalf("replies %v, want ses_1/per_1 once", got)
	}
	before := len(service.got())
	for _, c := range []struct{ command, why string }{
		{"git tag v1.0.0", "a scope does not cover it"},
		{"git push --force origin main", "never list"},
		{"git push origin main && true", "compound"},
	} {
		if res := decide(c.command, "per_2"); res.Allow {
			t.Errorf("%s (%s) was allowed", c.command, c.why)
		}
	}
	if len(service.got()) != before {
		t.Fatalf("a request with no decision was answered: %v", service.got())
	}

	// An older service has no reply route: no decision, nothing sent.
	older := newFakeOpenCodeService(t, `{"paths":{}}`)
	writeOwnerOpenCodeService(t, state, older.srv.URL)
	if res := decide("git push origin main", "per_3"); res.Allow || len(older.got()) != 0 {
		t.Fatalf("older service: %+v, replies %v", res, older.got())
	}
}
