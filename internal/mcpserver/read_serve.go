package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/niparis/agent-net/internal/protocol"
)

// The four read tools that filter, page or fetch (agent_messages_find,
// agent_message_get, agent_workers_find, agent_mcp_servers_find) run in the
// daemon, once, so that every harness gets the same answer (principle 18).
// The MCP handlers only forward their input to the daemon method; a native
// plugin sends the same input to the same method.

// ServeRead answers one of those four daemon methods. raw is the tool input.
// who is the session bound to the connection; call reaches the other daemon
// methods the read needs, as that session.
func ServeRead(ctx context.Context, method string, raw json.RawMessage, who ReadCaller, call Caller) (any, error) {
	ctx = context.WithValue(ctx, readCallerKey{}, who)
	switch method {
	case protocol.MethodMessagesFind:
		var in roMessagesFindIn
		if err := decodeReadInput(raw, &in); err != nil {
			return nil, err
		}
		in.Detail = roDetailDefault(in.Detail)
		return roFindMessages(ctx, call, in)
	case protocol.MethodMessageGet:
		var in roMessageGetIn
		if err := decodeReadInput(raw, &in); err != nil {
			return nil, err
		}
		return roGetMessage(ctx, call, in)
	case protocol.MethodWorkersFind:
		var in roWorkersFindIn
		if err := decodeReadInput(raw, &in); err != nil {
			return nil, err
		}
		in.Detail = roDetailDefault(in.Detail)
		return roFindWorkers(ctx, call, in)
	case protocol.MethodMCPServersFind:
		var in roMCPFindIn
		if err := decodeReadInput(raw, &in); err != nil {
			return nil, err
		}
		in.Detail = roDetailDefault(in.Detail)
		return roFindMCPServers(ctx, call, in)
	}
	return nil, fmt.Errorf("unknown read method %q", method)
}

// roDetailDefault gives the detail a plugin leaves out the value the MCP
// schema fills in, so a cursor made through one path works through the other.
func roDetailDefault(detail string) string {
	if detail == "" {
		return "concise"
	}
	return detail
}

func decodeReadInput(raw json.RawMessage, in any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if err := json.Unmarshal(raw, in); err != nil {
		return fmt.Errorf("bad arguments: %v", err)
	}
	return nil
}

// roForward sends a tool's input to its daemon method and decodes the answer.
func roForward[Out any](ctx context.Context, call Caller, method string, in any) (Out, error) {
	var out Out
	raw, err := call(ctx, method, in)
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(raw, &out)
	return out, err
}
