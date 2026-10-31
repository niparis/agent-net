package daemon

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
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
