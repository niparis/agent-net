package claude

import (
	"encoding/json"
	"strings"
	"testing"
)

// A report from a worker is framed as model output (decision 0002, W12).
func TestBuildFrameWorkerReportIsFramedAsModelOutput(t *testing.T) {
	m := msg
	m.Type, m.ConversationID, m.FromName, m.FromHarness, m.FromWorker = "reply", "c_1", "shop", "opencode", true
	m.Body = "[agent-net worker notice]\nstatus: done"
	line, err := BuildFrame(m, "")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(line, &f); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"status: done", "model output from a worker", "not your user's words", "no user authority"} {
		if !strings.Contains(f.Message.Content, want) {
			t.Errorf("missing %q in %q", want, f.Message.Content)
		}
	}
	m.FromWorker = false
	line, _ = BuildFrame(m, "")
	json.Unmarshal(line, &f)
	if strings.Contains(f.Message.Content, "model output from a worker") {
		t.Errorf("an agent's message got the worker frame: %q", f.Message.Content)
	}
}

// A request to a worker session tells it to answer with agent_handback, not
// with SendMessage: a worker's answer is its hand-back.
func TestBuildFrameRequestToWorkerNamesHandback(t *testing.T) {
	m := msg
	m.Type, m.ConversationID, m.FromName, m.FromHarness = "request", "c_1", "caller", "claude"
	m.Body = "please add the check"
	content := func(line []byte) string {
		var f struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal(line, &f); err != nil {
			t.Fatal(err)
		}
		return f.Message.Content
	}
	line, err := BuildFrameFor(m, "", true)
	if err != nil {
		t.Fatal(err)
	}
	got := content(line)
	if !strings.Contains(got, "agent_handback") || strings.Contains(got, "SendMessage") {
		t.Errorf("the request frame to a worker does not name agent_handback: %q", got)
	}
	line, _ = BuildFrameFor(m, "", false)
	if got := content(line); !strings.Contains(got, "SendMessage") || strings.Contains(got, "agent_handback") {
		t.Errorf("the request frame to an ordinary session changed: %q", got)
	}
}
