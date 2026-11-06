package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func (g *Gateway) relayMachineLog(ctx context.Context, raw []byte) (json.RawMessage, error) {
	var q protocol.MachineLogRequest
	if json.Unmarshal(raw, &q) != nil {
		return nil, reject("bad machine log request")
	}
	q, _, err := protocol.NormalizeMachineLog(q, time.Now())
	if err != nil {
		return nil, reject("%s", err.Error())
	}
	nodes, err := g.store.Nodes()
	if err != nil {
		return nil, err
	}
	var id string
	for _, n := range nodes {
		if strings.EqualFold(n.Hostname, q.Machine) || strings.EqualFold(n.NodeID, q.Machine) {
			id = n.NodeID
			break
		}
	}
	if id == "" {
		return nil, reject("unknown machine; use agent_machines_find")
	}
	target := g.conn(id)
	if target == nil {
		return nil, reject("that machine is offline")
	}
	data, err := target.request(ctx, "node.machine_log", q, relaySessionTextMax)
	if err != nil {
		return nil, reject("%s", err.Error())
	}
	var res protocol.MachineLogResult
	if len(data) > protocol.MachineLogMaxBytes || json.Unmarshal(data, &res) != nil || len(res.Lines) > q.Limit || res.Log != q.Log {
		return nil, reject("invalid machine log response")
	}
	return data, nil
}
