package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func fullOpenAPISpec(version string) string {
	paths := map[string]map[string]any{}
	for _, rt := range []struct{ path, method string }{
		{"/api/info", "get"}, {"/api/agent", "get"}, {"/api/model", "get"}, {"/api/location", "get"},
		{"/api/session", "post"}, {"/api/session/active", "get"}, {"/api/session/{sessionID}", "get"}, {"/api/session/{sessionID}", "delete"},
		{"/api/session/{sessionID}/message", "get"}, {"/api/session/{sessionID}/prompt", "post"}, {"/api/session/{sessionID}/agent", "post"},
		{"/api/session/{sessionID}/model", "post"}, {"/api/session/{sessionID}/compact", "post"}, {"/api/session/{sessionID}/interrupt", "post"},
	} {
		if paths[rt.path] == nil {
			paths[rt.path] = map[string]any{}
		}
		paths[rt.path][rt.method] = map[string]any{}
	}
	b, _ := json.Marshal(map[string]any{"info": map[string]any{"version": version}, "paths": paths})
	return string(b)
}

func TestOpenCodeCallersDiscoverRoutesViaAdapter(t *testing.T) {
	var discoveries atomic.Int32
	var lastQuery atomic.Value
	spec := fullOpenAPISpec("2.2.6")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /openapi.json":
			discoveries.Add(1)
			fmt.Fprint(w, spec)
		case "GET /api/agent":
			json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"name": "cto", "permissions": ctoPermissions()}}})
		case "GET /api/session/active":
			fmt.Fprint(w, `{"data":{"ses_1":{"type":"running"}}}`)
		case "GET /api/session/ses_1":
			fmt.Fprint(w, `{"data":{"id":"ses_1","outcome":"failed","cost":0.01,"tokens":{"input":100,"output":20},"model":{"providerID":"fake","id":"m1"}}}`)
		case "GET /api/session/ses_1/message":
			lastQuery.Store(r.URL.RawQuery)
			fmt.Fprint(w, `{"data":[{"id":"m_idle","type":"idle","outcome":"failed","error":{"type":"provider.rate","message":"rate limit"}},{"id":"m_1","type":"assistant","content":[{"type":"text","text":"assistant reply"}],"tokens":{"input":500,"cache":{"read":100,"write":50}}}],"cursor":{}}`)
		case "GET /api/model":
			fmt.Fprint(w, `{"data":[{"providerID":"fake","id":"m1","limit":{"context":128000}}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	t.Run("cto_tools", func(t *testing.T) {
		discoveries.Store(0)
		d := &Daemon{log: log.New(io.Discard, "", 0)}
		d.cto.base = server.URL
		if err := d.checkOpenCodeCTOAgent(context.Background(), []byte(`{"version":"2.2.6"}`)); err != nil {
			t.Fatal(err)
		}
		if discoveries.Load() == 0 {
			t.Fatal("checkOpenCodeCTOAgent did not discover routes through adapter")
		}
	})

	t.Run("worker_stats_context_and_failure", func(t *testing.T) {
		discoveries.Store(0)
		d := &Daemon{}
		w := &worker{harness: "opencode", nativeID: "ses_1", apiBase: server.URL}
		st := (opencodeLauncher{}).stats(context.Background(), d, w)
		if st.ContextTokens != 650 || st.ContextLimit != 128000 {
			t.Fatalf("stats: %+v", st)
		}
		if got, _ := lastQuery.Load().(string); got != "type=assistant&order=desc&limit=1" {
			t.Fatalf("context query = %q, want type=assistant&order=desc&limit=1", got)
		}
		msg, provider := (opencodeLauncher{}).failure(context.Background(), d, w)
		if msg == "" || !provider {
			t.Fatalf("failure = %q, %v", msg, provider)
		}
		if discoveries.Load() == 0 {
			t.Fatal("worker stats/context/failure did not discover routes through adapter")
		}
	})

	t.Run("idle_notice_last_assistant", func(t *testing.T) {
		discoveries.Store(0)
		got := openCodeLastAssistant(context.Background(), server.URL, "", "ses_1")
		if got != "assistant reply" {
			t.Fatalf("last assistant = %q", got)
		}
		if q, _ := lastQuery.Load().(string); q != "type=assistant&limit=1" {
			t.Fatalf("idle notice query = %q, want type=assistant&limit=1", q)
		}
		if discoveries.Load() == 0 {
			t.Fatal("openCodeLastAssistant did not discover routes through adapter")
		}
	})

	t.Run("owner_start_and_cleanup", func(t *testing.T) {
		state := t.TempDir()
		t.Setenv("XDG_STATE_HOME", state)
		// Verify owner service discovery fails cleanly when CreateSession is absent from spec.
		noCreate := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/openapi.json" {
				fmt.Fprint(w, `{"info":{"version":"2.2.6"},"paths":{}}`)
				return
			}
			t.Errorf("unexpected direct call %s %s when route is absent", r.Method, r.URL.Path)
			fmt.Fprint(w, `{"data":{"id":"ses_should_not_call"}}`)
		}))
		defer noCreate.Close()
		writeOwnerOpenCodeService(t, state, noCreate.URL)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		d := &Daemon{cfg: Config{Hostname: "box", HomeDir: t.TempDir()}, ctoCtx: ctx, log: log.New(io.Discard, "", 0)}
		_, err := d.launchOwnerOpenCode(ctx, protocol.StartedSession{ID: "st_1", Project: "shop", Path: t.TempDir()}, "", "", "", false, false)
		cancel()
		d.wg.Wait()
		if err == nil {
			t.Fatal("launchOwnerOpenCode succeeded despite missing create session route")
		}
	})
}
