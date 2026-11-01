package daemon

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestOpenCodeContextNewestAssistant(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/session/ses_test":
			fmt.Fprint(w, `{"data":{"model":{"providerID":"opencode-go","id":"deepseek-v4.1-flash"}}}`)
		case "/api/session/ses_test/message":
			if r.URL.Query().Get("type") == "assistant" && r.URL.Query().Get("order") == "desc" && r.URL.Query().Get("limit") == "1" {
				fmt.Fprint(w, `{"data":[{"id":"msg_new","type":"assistant","tokens":{"input":80000,"output":2000,"reasoning":0,"cache":{"read":3000,"write":1000}}}],"cursor":{}}`)
			} else {
				fmt.Fprint(w, `{"data":[{"id":"msg_old","type":"assistant","tokens":{"input":100,"output":0,"reasoning":0,"cache":{"read":0,"write":0}}},{"id":"msg_new","type":"assistant","tokens":{"input":80000,"output":2000,"reasoning":0,"cache":{"read":3000,"write":1000}}}],"cursor":{}}`)
			}
		case "/api/model":
			fmt.Fprint(w, `{"data":[{"providerID":"other","id":"deepseek-v4.1-flash","modelID":"deepseek-v4.1-flash","limit":{"context":1}},{"providerID":"opencode-go","id":"deepseek-v4.1-flash","modelID":"upstream-id","limit":{"context":200000}}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	got := opencodeSessionContext(context.Background(), server.URL, "", "ses_test")
	if got.Tokens != 84000 || got.Limit != 200000 || got.AssistantID != "msg_new" || got.Model != "opencode-go/deepseek-v4.1-flash" {
		t.Fatalf("context = %+v", got)
	}
}

func TestOpenCodeWorkerContextSurvivesIncompleteAndFailedReads(t *testing.T) {
	var phase atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := phase.Load()
		if p == 3 {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		switch r.URL.Path {
		case "/api/session/ses_test":
			fmt.Fprint(w, `{"data":{"model":{"providerID":"fake","id":"m1"},"tokens":{"input":100000,"output":1000}}}`)
		case "/api/session/ses_test/message":
			switch p {
			case 1: // An assistant message exists but its turn has no usage yet.
				fmt.Fprint(w, `{"data":[{"id":"msg_running","type":"assistant"}]}`)
			case 2: // A read can fail without the worker ending.
				fmt.Fprint(w, `{`)
			case 4: // Compaction reports a real, smaller context on a later step.
				fmt.Fprint(w, `{"data":[{"id":"msg_compacted","type":"assistant","tokens":{"input":12000}}]}`)
			default:
				fmt.Fprint(w, `{"data":[{"id":"msg_finished","type":"assistant","tokens":{"input":84000}}]}`)
			}
		case "/api/model":
			fmt.Fprint(w, `{"data":[{"providerID":"fake","id":"m1","limit":{"context":200000}}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	d := &Daemon{launchers: map[string]launcher{"opencode": opencodeLauncher{}}}
	w := &worker{harness: "opencode", nativeID: "ses_test", apiBase: server.URL}
	for i, want := range []int64{84000, 84000, 84000, 84000, 12000} {
		phase.Store(int32(i))
		st := d.workerStats(w)
		d.noteWorkerUsage(w, st)
		got := d.workerInfo(w)
		if st.ContextTokens != want || got.ContextTokens != want || got.ContextLimit != 200000 {
			t.Fatalf("phase %d: stats=%+v worker=%+v", i, st, got)
		}
	}
}
