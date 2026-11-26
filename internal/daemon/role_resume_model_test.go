package daemon

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// A Claude resume of a role session names the model the session was asked to
// run, or else the one it runs, so Claude never falls back to its own default.
// The wake fallback builds the same spec.
func TestClaudeRoleResumePassesItsModel(t *testing.T) {
	d := &Daemon{log: log.New(io.Discard, "", 0)}
	d.cfg.StateDir = t.TempDir()
	d.cfg.testGatewayCall = func(_ context.Context, typ string, _ any) (json.RawMessage, bool, error) {
		if typ == "chief.get" {
			raw, _ := json.Marshal(protocol.ChiefStatus{SessionID: "s-chief"})
			return raw, true, nil
		}
		return nil, true, errNotConnected
	}
	for _, tc := range []struct {
		name string
		a    protocol.AgentView
		want string
	}{
		{"merger asked", protocol.AgentView{SessionID: "s-merger", Type: "merger", NativeID: "n1", RequestedModel: "claude-sonnet-5-5", Model: "claude-fable-5-2"}, "claude-sonnet-5-5"},
		{"merger running", protocol.AgentView{SessionID: "s-merger", Type: "merger", NativeID: "n1", Model: "claude-opus-5-5"}, "claude-opus-5-5"},
		{"chief", protocol.AgentView{SessionID: "s-chief", NativeID: "n2", Model: "claude-sonnet-5-5"}, "claude-sonnet-5-5"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.a.Cwd = t.TempDir()
			spec, model := d.claudeResumeSpec(tc.a)
			if got := argValue(spec.ModelArgs, "--model"); got != tc.want || model != tc.want {
				t.Fatalf("resume model args %v, model %q, want %q", spec.ModelArgs, model, tc.want)
			}
		})
	}
}

// A start that named a model reports it on the session it registered, so the
// gateway keeps the ask for a later resume and for the session list.
func TestStartReportsItsRequestedModel(t *testing.T) {
	var sent []protocol.SessionInfo
	d := &Daemon{log: log.New(io.Discard, "", 0), sessions: map[string]*localSession{}}
	d.cfg.testGatewayCall = func(_ context.Context, typ string, params any) (json.RawMessage, bool, error) {
		if typ != "session.upsert" {
			return nil, false, nil
		}
		sent = append(sent, params.(protocol.SessionUpsert).Session)
		return json.RawMessage(`{"session_id":"s-merger"}`), true, nil
	}
	ls := &localSession{info: protocol.SessionInfo{Harness: "claude", NativeID: "n1", Model: "claude-fable-5-2"}}
	d.sessions[key("claude", "n1")] = ls
	d.noteRequestedModel("claude", "n1", "claude-sonnet-5-5")
	d.bg.Wait()
	if len(sent) != 1 || sent[0].RequestedModel != "claude-sonnet-5-5" {
		t.Fatalf("reports = %+v, want one with the requested model", sent)
	}
}
