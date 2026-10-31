package operator

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// fakeDaemon speaks the daemon's local protocol on a Unix socket.
type fakeDaemon struct {
	mu      sync.Mutex
	calls   []string
	params  map[string]json.RawMessage
	handler func(method string, params json.RawMessage) (any, string)
}

func startFake(t *testing.T, h func(method string, params json.RawMessage) (any, string)) (*fakeDaemon, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "op")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "d.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	d := &fakeDaemon{handler: h, params: map[string]json.RawMessage{}}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				var writeMu sync.Mutex
				sc := bufio.NewScanner(c)
				for sc.Scan() {
					var req struct {
						ID     json.RawMessage `json:"id"`
						Method string          `json:"method"`
						Params json.RawMessage `json:"params"`
					}
					if json.Unmarshal(sc.Bytes(), &req) != nil {
						return
					}
					d.mu.Lock()
					d.calls = append(d.calls, req.Method)
					d.params[req.Method] = req.Params
					d.mu.Unlock()
					go func() {
						res, e := h(req.Method, req.Params)
						resp := map[string]any{"id": req.ID}
						if e != "" {
							resp["error"] = e
						} else {
							resp["result"] = res
						}
						b, _ := json.Marshal(resp)
						writeMu.Lock()
						c.Write(append(b, '\n'))
						writeMu.Unlock()
					}()
				}
			}()
		}
	}()
	return d, path
}

func TestActionsUseTheOperatorMethodsAndTypes(t *testing.T) {
	d, path := startFake(t, func(method string, params json.RawMessage) (any, string) {
		switch method {
		case "hello":
			return map[string]any{}, ""
		case protocol.OpSend:
			return protocol.MessageSent{Message: &protocol.Message{ID: "m_1", From: "s_me", To: "s_x"}}, ""
		case protocol.OpWorkerStart:
			return protocol.WorkerStartResult{SessionID: "s_w", Project: "shop", Harness: "opencode"}, ""
		case protocol.OpWorkerStop:
			return protocol.WorkerStopResult{Session: "s_w", Stopped: true}, ""
		case protocol.OpWorkers:
			return protocol.WorkersResult{Workers: []protocol.WorkerView{{SessionID: "s_w", OwnerName: "you (on mac)"}}}, ""
		}
		return nil, "unknown method"
	})
	c := New(path)
	defer c.Close()
	ctx := context.Background()

	sent, err := c.SendAsOwner(ctx, "s_x", "hello")
	if err != nil || sent.Message == nil || sent.Message.ID != "m_1" {
		t.Fatalf("send: %+v %v", sent, err)
	}
	var sreq map[string]any
	json.Unmarshal(d.params[protocol.OpSend], &sreq)
	if sreq["to"] != "s_x" || sreq["body"] != "hello" || sreq["idempotency_key"] == "" {
		t.Errorf("send params: %v", sreq)
	}
	for _, bad := range []string{"sender", "sender_session_id", "from", "owner"} {
		if _, ok := sreq[bad]; ok {
			t.Errorf("the owner send carries a sender field %q", bad)
		}
	}

	res, err := c.StartWorker(ctx, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "a/b", Task: "t", Isolation: "worktree"})
	if err != nil || res.SessionID != "s_w" {
		t.Fatalf("start: %+v %v", res, err)
	}
	var wreq protocol.WorkerStartRequest
	json.Unmarshal(d.params[protocol.OpWorkerStart], &wreq)
	if wreq.Project != "shop" || wreq.Model != "a/b" || wreq.Isolation != "worktree" {
		t.Errorf("start params: %+v", wreq)
	}
	if err := c.StopWorker(ctx, "s_w"); err != nil {
		t.Fatal(err)
	}
	var stop protocol.WorkerStopRequest
	json.Unmarshal(d.params[protocol.OpWorkerStop], &stop)
	if stop.Session != "s_w" {
		t.Errorf("stop params: %+v", stop)
	}
	ws, err := c.Workers(ctx, protocol.WorkersQuery{})
	if err != nil || len(ws) != 1 || ws[0].OwnerName != "you (on mac)" {
		t.Fatalf("workers: %+v %v", ws, err)
	}
}

// An older daemon refuses the new calls. The operator says so in words that
// tell the person what to do.
func TestOldDaemonRefusesTheNewCalls(t *testing.T) {
	_, path := startFake(t, func(method string, params json.RawMessage) (any, string) {
		if method == "hello" {
			return map[string]any{}, ""
		}
		return nil, `method "` + method + `" is not available to cli clients`
	})
	c := New(path)
	defer c.Close()
	ctx := context.Background()
	_, e1 := c.SendAsOwner(ctx, "s", "b")
	_, e2 := c.StartWorker(ctx, protocol.WorkerStartRequest{})
	e3 := c.StopWorker(ctx, "s")
	_, e4 := c.Workers(ctx, protocol.WorkersQuery{})
	for i, err := range []error{e1, e2, e3, e4} {
		if !errors.Is(err, ErrTooOld) || !strings.Contains(err.Error(), "older than this command") {
			t.Errorf("call %d: %v", i, err)
		}
	}
	// A refusal that is not about age is passed on as it is.
	_, path2 := startFake(t, func(method string, params json.RawMessage) (any, string) {
		if method == "hello" {
			return map[string]any{}, ""
		}
		return nil, "machine mac is offline"
	})
	c2 := New(path2)
	defer c2.Close()
	if _, err := c2.StartWorker(ctx, protocol.WorkerStartRequest{}); err == nil || errors.Is(err, ErrTooOld) || err.Error() != "machine mac is offline" {
		t.Errorf("error: %v", err)
	}
}

func TestSnapshotIncludesOwnersAndWorkers(t *testing.T) {
	_, path := startFake(t, func(method string, params json.RawMessage) (any, string) {
		switch method {
		case "hello":
			return map[string]any{}, ""
		case protocol.OpInfo:
			return protocol.OperatorInfo{Connected: true, NodeID: "n_1", Hostname: "mac", OwnerSession: "s_o", Launch: true, Version: "v1"}, ""
		case protocol.OpNodes:
			return protocol.NodesInfo{}, ""
		case protocol.OpAgents:
			var q protocol.QueryAgents
			json.Unmarshal(params, &q)
			if !q.IncludeOwners {
				t.Error("the snapshot must ask for the owner sessions, to name them in the feed")
			}
			return protocol.AgentsResult{Agents: []protocol.AgentView{{SessionID: "s_w", Online: true, Worker: &protocol.WorkerInfo{Owner: "s_o"}}}}, ""
		case protocol.OpProjects:
			return protocol.ProjectsResult{}, ""
		}
		return nil, "unknown method"
	})
	c := New(path)
	defer c.Close()
	snap, err := c.Snapshot(context.Background())
	if err != nil || snap.OwnerSession != "s_o" || snap.Hostname != "mac" || !snap.Launch || len(snap.Workers()) != 1 {
		t.Fatalf("snapshot: %+v %v", snap, err)
	}
}

func TestSnapshotSlowBestEffort(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	_, path := startFake(t, func(method string, _ json.RawMessage) (any, string) {
		switch method {
		case protocol.OpInfo:
			return protocol.OperatorInfo{Connected: true}, ""
		case protocol.OpWorkerProfiles, protocol.OpScoresGet, "learned_list":
			return []any{}, ""
		case protocol.OpNodes:
			<-release
			return protocol.NodesInfo{}, ""
		case protocol.OpAgents:
			return protocol.AgentsResult{Agents: []protocol.AgentView{{}}}, ""
		default:
			return map[string]any{}, ""
		}
	})
	c := New(path)
	defer c.Close()
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	snap, err := c.Snapshot(ctx)
	if err != nil || len(snap.Agents) != 1 || time.Since(start) > 4*time.Second || len(snap.FailedCalls) != 1 || snap.FailedCalls[0] != protocol.OpNodes {
		t.Fatalf("snapshot after %s: %+v, %v", time.Since(start), snap, err)
	}
}

func TestGraphTalkClient(t *testing.T) {
	_, path := startFake(t, func(method string, _ json.RawMessage) (any, string) {
		if method == "hello" {
			return map[string]any{}, ""
		}
		if method == "graph_talk" {
			return protocol.GraphTalkResult{Counts: []protocol.GraphTalkCount{{From: "alpha", To: "beta", Count: 3}}, LastID: 7}, ""
		}
		return nil, "unexpected method"
	})
	c := New(path)
	defer c.Close()
	got, err := c.GraphTalk(context.Background())
	if err != nil || len(got.Counts) != 1 || got.Counts[0].Count != 3 || got.LastID != 7 {
		t.Fatalf("graph talk: %+v %v", got, err)
	}
}
