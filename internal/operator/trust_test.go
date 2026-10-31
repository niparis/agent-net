package operator

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestSetPeerTrustUsesTheOperatorMethod(t *testing.T) {
	d, path := startFake(t, func(method string, params json.RawMessage) (any, string) {
		switch method {
		case "hello":
			return map[string]any{}, ""
		case protocol.OpSessionTrust:
			return protocol.SessionTrustResult{}, ""
		}
		return nil, "unknown method"
	})
	c := New(path)
	defer c.Close()
	if err := c.SetPeerTrust(context.Background(), "s_x", true); err != nil {
		t.Fatal(err)
	}
	var got protocol.SessionTrustRequest
	json.Unmarshal(d.params[protocol.OpSessionTrust], &got)
	if got.SessionID != "s_x" || !got.On {
		t.Errorf("params: %+v", got)
	}
}

func TestSetPeerTrustOnAnOldDaemonSaysSo(t *testing.T) {
	_, path := startFake(t, func(method string, params json.RawMessage) (any, string) {
		if method == "hello" {
			return map[string]any{}, ""
		}
		return nil, `method "` + method + `" is not available to cli clients`
	})
	c := New(path)
	defer c.Close()
	if err := c.SetPeerTrust(context.Background(), "s_x", true); !errors.Is(err, ErrTooOld) {
		t.Errorf("error: %v", err)
	}
}

func TestProjectDisplayOperator(t *testing.T) {
	d, path := startFake(t, func(method string, params json.RawMessage) (any, string) {
		if method == "hello" {
			return map[string]any{}, ""
		}
		if method == "project_display_set" || method == "project_display_get" {
			return map[string]string{"project": "agent-net", "name": "Agent Network"}, ""
		}
		return nil, "unknown method"
	})
	c := New(path)
	defer c.Close()
	if _, err := c.SetProjectDisplayName(context.Background(), "agent-net", "Agent Network"); err != nil {
		t.Fatal(err)
	}
	out, err := c.ProjectDisplayName(context.Background(), "agent-net")
	if err != nil || out.Name != "Agent Network" {
		t.Fatalf("%+v %v", out, err)
	}
	var p map[string]string
	if err = json.Unmarshal(d.params["project_display_set"], &p); err != nil || p["project"] != "agent-net" || p["name"] != "Agent Network" {
		t.Fatalf("params %v %v", p, err)
	}
}
