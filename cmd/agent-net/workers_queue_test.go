package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// A start that has no room says so, with the place in the queue and how to
// cancel it.
func TestStartCommandQueued(t *testing.T) {
	f := &fakeOp{result: protocol.WorkerStartResult{Status: "queued", QueueID: "q_ab12", Position: 2, Harness: "opencode",
		Project: "shop", Hostname: "research", QueueReason: "queued #2 on research: CPU 5-min average 82% (target 75%)",
		MachineNote: "picked research: most room for a worker"}}
	var out bytes.Buffer
	if err := runStart(context.Background(), f, &out, []string{"--project", "shop", "do", "it"}); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"queued #2 on research: CPU 5-min average 82% (target 75%)", "q_ab12", "A notice comes to you when it starts",
		"machine: picked research", "agent-net stop q_ab12"} {
		if !strings.Contains(out.String(), s) {
			t.Errorf("output lacks %q:\n%s", s, out.String())
		}
	}
	if strings.Contains(out.String(), "started worker") {
		t.Errorf("a queued start was reported as started:\n%s", out.String())
	}
}

// `agent-net workers` shows each machine's worker count and room.
func TestWorkersCommandShowsMachineRoom(t *testing.T) {
	f := &fakeOp{usage: protocol.UsageResult{NodeID: "n_1", Hostname: "mac",
		Local: &protocol.UsageReport{Workers: &protocol.WorkerLoad{Running: 2, Queued: 1, Room: protocol.RoomCPU}},
		Gateway: &protocol.UsageAnswer{Nodes: []protocol.UsageNode{
			{NodeID: "n_1", Hostname: "mac", Usage: protocol.UsageReport{Workers: &protocol.WorkerLoad{Running: 9, Room: protocol.RoomOK}}},
			{NodeID: "n_2", Hostname: "research", Usage: protocol.UsageReport{Workers: &protocol.WorkerLoad{Running: 3, Queued: 0, Room: protocol.RoomOK}}},
			{NodeID: "n_3", Hostname: "old"},
		}}}}
	var out bytes.Buffer
	if err := runWorkers(context.Background(), f, &out, nil); err != nil {
		t.Fatal(err)
	}
	want := "mac: workers 2 running, 1 queued \u00b7 room: cpu\nresearch: workers 3 running, 0 queued \u00b7 room: ok\n"
	if !strings.HasPrefix(out.String(), want) {
		t.Errorf("output:\n%s\nwant it to start with:\n%s", out.String(), want)
	}
	if strings.Contains(out.String(), "old:") || strings.Contains(out.String(), "running, 0 queued \u00b7 room: ok\nmac") {
		t.Errorf("a machine with no report or a duplicate is listed:\n%s", out.String())
	}
}
