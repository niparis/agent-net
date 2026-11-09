package main

import (
	"bytes"
	"context"
	"encoding/json"
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
	if err := runStart(context.Background(), f, &out, []string{"--touches", "other", "--project", "shop", "do", "it"}); err != nil {
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

// Queued starts show in the table and in the JSON, and the room line prints
// for a machine that has only a queue.
func TestWorkersCommandShowsQueuedStarts(t *testing.T) {
	q := protocol.QueuedWorker{QueueID: "q_ab12", Position: 1, Hostname: "mac", Harness: "opencode", Model: "deepseek/x",
		Owner: "s_owner", Project: "shop", QueuedAt: 1700000000, Reason: "a worker needs about 1.2 GB"}
	f := &fakeOp{usage: protocol.UsageResult{NodeID: "n_1", Hostname: "mac",
		Local: &protocol.UsageReport{Workers: &protocol.WorkerLoad{Queued: 1, Room: protocol.RoomMem, Queue: []protocol.QueuedWorker{q}}}}}
	var out bytes.Buffer
	if err := runWorkers(context.Background(), f, &out, nil); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"mac: workers 0 running, 1 queued \u00b7 room: mem", "q_ab12", "shop", "opencode", "deepseek/x", "s_owner", "a worker needs about 1.2 GB"} {
		if !strings.Contains(out.String(), s) {
			t.Errorf("table lacks %q:\n%s", s, out.String())
		}
	}
	if strings.Contains(out.String(), "no workers") {
		t.Errorf("said no workers while a start is queued:\n%s", out.String())
	}
	out.Reset()
	if err := runWorkers(context.Background(), f, &out, []string{"-json"}); err != nil {
		t.Fatal(err)
	}
	var res protocol.WorkersResult
	if err := json.Unmarshal(out.Bytes(), &res); err != nil || len(res.Queued) != 1 || res.Queued[0].QueueID != "q_ab12" {
		t.Errorf("json: %v %s", err, out.String())
	}
}

// The Claude Code panel runs `agent-net workers -json -recent -owner-claude ID`
// for every session every three seconds. It reads the rows of that session
// only. The machines' queues are not the session's, and reading them costs a
// usage call to the gateway for the whole network, so this call does not make it.
func TestWorkersOfOneClaudeSessionDoNotReadMachineUsage(t *testing.T) {
	f := &fakeOp{workers: []protocol.WorkerView{{SessionID: "s_w1", Name: "w1", Hostname: "research", Status: "busy", Online: true}},
		usage: protocol.UsageResult{Local: &protocol.UsageReport{Workers: &protocol.WorkerLoad{Queue: []protocol.QueuedWorker{{QueueID: "q_1"}}}}}}
	var out bytes.Buffer
	if err := runWorkers(context.Background(), f, &out, []string{"-json", "-recent", "-owner-claude", "abc"}); err != nil {
		t.Fatal(err)
	}
	if f.usageCalls != 0 {
		t.Fatalf("the panel's call read machine usage %d times", f.usageCalls)
	}
	if f.workersQ.OwnerClaude != "abc" || !f.workersQ.Recent {
		t.Fatalf("query %+v", f.workersQ)
	}
	var res struct {
		Workers      []protocol.WorkerView   `json:"workers"`
		Queued       []protocol.QueuedWorker `json:"queued"`
		PanelVersion string                  `json:"panel_version"`
	}
	if err := json.Unmarshal(out.Bytes(), &res); err != nil || len(res.Workers) != 1 || res.PanelVersion == "" || len(res.Queued) != 0 {
		t.Fatalf("json %v: %s", err, out.String())
	}
	// Without a session the command still shows the queue of every machine.
	out.Reset()
	if err := runWorkers(context.Background(), f, &out, []string{"-json"}); err != nil {
		t.Fatal(err)
	}
	if f.usageCalls != 1 || !strings.Contains(out.String(), "q_1") {
		t.Fatalf("usage calls %d: %s", f.usageCalls, out.String())
	}
}
