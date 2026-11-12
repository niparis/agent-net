//go:build measure

package gateway

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/tui"
)

// pollHarness is a fake daemon in front of the real gateway handlers and a copy
// of a real gateway database. The real TUI model and the real operator client
// run against it, and real writes to the copy make the topics move. It counts
// the calls and the answer bytes of each method, which is what the owner's
// Mac sends through its daemon and the gateway link.
type pollHarness struct {
	t     *testing.T
	g     *Gateway
	owner string
	node  string

	mu    sync.Mutex
	calls map[string]int
	bytes map[string]int
}

func (h *pollHarness) count(method string, n int) {
	h.mu.Lock()
	h.calls[method]++
	h.bytes[method] += n
	h.mu.Unlock()
}

// frame maps a local operator method to the gateway frame the daemon sends.
var pollFrames = map[string]string{
	"list": "query.agents", "nodes": "query.nodes", "projects": "query.projects",
	"learned_list": "learned.list", "asks_list": "asks.list", "chief_get": "chief.get",
	"chief_sorts": "chief.sorts", "counts": "query.counts", "usage": "query.usage",
	"events": "query.events",
}

func (h *pollHarness) answer(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	nc := &nodeConn{id: h.node}
	switch method {
	case "info":
		return protocol.OperatorInfo{Connected: true, NodeID: h.node, Version: "v0.0.0-measure", OwnerSession: h.owner, Launch: true, Protocol: protocol.LocalProtocol}, nil
	case "proposal_list":
		// The daemon lists the proposals of every online machine. It asks the
		// gateway for the machines first.
		if _, err := h.g.handle(ctx, nc, "query.nodes", pollProposalNodesRequest()); err != nil {
			return nil, err
		}
		return []protocol.CTOProposal{}, nil
	case protocol.OpChangesOwnerFind:
		var a protocol.ChangesFindArgs
		_ = json.Unmarshal(raw, &a)
		res, err := h.g.handle(ctx, nc, "change.list", pollChangeRequest(a))
		if err != nil {
			return nil, err
		}
		page := res.(protocol.ChangeListResult)
		return pollChangesResult(page), nil
	case "learned_list":
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		if m == nil {
			m = map[string]any{}
		}
		m["owner"] = h.owner
		raw, _ = json.Marshal(m)
	}
	if frame, ok := pollFrames[method]; ok {
		return h.g.handle(ctx, nc, frame, raw)
	}
	return map[string]any{}, nil
}

func (h *pollHarness) serve(ln net.Listener, stop <-chan struct{}) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go h.serveConn(conn, stop)
	}
}

func (h *pollHarness) serveConn(conn net.Conn, stop <-chan struct{}) {
	defer conn.Close()
	var wmu sync.Mutex
	write := func(v any) {
		b, _ := json.Marshal(v)
		wmu.Lock()
		conn.Write(append(b, '\n'))
		wmu.Unlock()
	}
	r := bufio.NewReaderSize(conn, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			return
		}
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(line, &req) != nil {
			continue
		}
		if req.Method == "hello" {
			write(map[string]any{"id": req.ID, "result": map[string]any{}})
			continue
		}
		if req.Method == protocol.OpTopicsSubscribe {
			versions := h.g.topicVersions()
			write(map[string]any{"id": req.ID, "result": versions})
			go func() {
				ticker := time.NewTicker(250 * time.Millisecond)
				defer ticker.Stop()
				seen := versions
				for {
					select {
					case <-stop:
						return
					case <-ticker.C:
						next := h.g.topicVersions()
						for _, topic := range protocol.Topics {
							if next[topic] != seen[topic] {
								write(map[string]any{"method": protocol.FrameTopicChanged, "params": protocol.TopicEvent{Topic: topic, Version: next[topic]}})
							}
						}
						seen = next
					}
				}
			}()
			continue
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			res, err := h.answer(ctx, req.Method, req.Params)
			if err != nil {
				write(map[string]any{"id": req.ID, "error": err.Error()})
				h.count(req.Method, 0)
				return
			}
			b, _ := json.Marshal(res)
			if strings.Contains(string(b), `"not_modified":true`) {
				// Rows that were not sent again are counted apart.
				h.count(req.Method+" (not modified)", len(b))
			} else {
				h.count(req.Method, len(b))
			}
			wmu.Lock()
			out, _ := json.Marshal(map[string]any{"id": req.ID, "result": json.RawMessage(b)})
			conn.Write(append(out, '\n'))
			wmu.Unlock()
		}()
	}
}

// churn makes the writes of a busy network: session heartbeats and events, at
// the rates read from a real gateway's counters.
func (h *pollHarness) churn(stop <-chan struct{}, heartbeats, events float64) {
	rows, err := h.g.store.db.Query(`SELECT id FROM sessions WHERE online=1 AND worker=''`)
	if err != nil {
		h.t.Fatal(err)
	}
	var ids []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	if len(ids) == 0 {
		h.t.Fatal("no online sessions in the copy")
	}
	beat := time.NewTicker(time.Duration(float64(time.Second) / heartbeats))
	evt := time.NewTicker(time.Duration(float64(time.Second) / events))
	defer beat.Stop()
	defer evt.Stop()
	for i := 0; ; i++ {
		select {
		case <-stop:
			return
		case <-beat.C:
			now := time.Now().UnixNano()
			// A report moves last_seen. last_active moves only when a session
			// does something, which the events below stand for.
			h.g.store.db.Exec(`UPDATE sessions SET last_seen=? WHERE id=?`, now, ids[i%len(ids)])
		case <-evt.C:
			h.g.store.Event("msg.stored", h.node, "", "", "measure")
		}
	}
}

// pause waits for d in steps of a second, so no single sleep is long.
func pause(d time.Duration) {
	for end := time.Now().Add(d); time.Now().Before(end); {
		time.Sleep(min(time.Second, time.Until(end)))
	}
}

func cpuSeconds() float64 {
	var ru syscall.Rusage
	syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
	return float64(ru.Utime.Sec+ru.Stime.Sec) + float64(ru.Utime.Usec+ru.Stime.Usec)/1e6
}

// TestMeasureTUIPolling runs the real TUI against the real gateway handlers on
// a copy of a real database and reports calls per minute and bytes per method.
// It is a measurement, not a check. It runs only when AGENT_NET_MEASURE_DB
// names the copy:
//
//	AGENT_NET_MEASURE_DB=/path/to/gw.db AGENT_NET_MEASURE_SECONDS=60 \
//	  go test -tags measure ./internal/gateway -run TestMeasureTUIPolling$ -v -count=1 -timeout 10m
//
// AGENT_NET_MEASURE_TAB is the key of the tab ("1" Sessions, "7" Desk). The
// churn is AGENT_NET_MEASURE_HEARTBEATS and AGENT_NET_MEASURE_EVENTS per second.
func TestMeasureTUIPolling(t *testing.T) {
	src := os.Getenv("AGENT_NET_MEASURE_DB")
	if src == "" {
		t.Skip("AGENT_NET_MEASURE_DB is not set")
	}
	envFloat := func(name string, def float64) float64 {
		if v, err := strconv.ParseFloat(os.Getenv(name), 64); err == nil && v > 0 {
			return v
		}
		return def
	}
	seconds := envFloat("AGENT_NET_MEASURE_SECONDS", 60)
	tabKey := os.Getenv("AGENT_NET_MEASURE_TAB")
	if tabKey == "" {
		tabKey = "7"
	}
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gateway.db"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	g, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { g.store.Close() })
	// New marks every session offline. Mark the ones seen in the last hour
	// online again, as the live gateway has them.
	g.store.db.Exec(`UPDATE sessions SET online=1 WHERE last_seen > ? AND harness NOT IN ('cto','owner','bot')`, time.Now().Add(-time.Hour).UnixNano())
	h := &pollHarness{t: t, g: g, calls: map[string]int{}, bytes: map[string]int{}}
	_ = g.store.db.QueryRow(`SELECT value FROM owner_settings WHERE key='owner_node'`).Scan(&h.node)
	_ = g.store.db.QueryRow(`SELECT id FROM sessions WHERE harness='owner' AND node_id=? LIMIT 1`, h.node).Scan(&h.owner)

	sock := filepath.Join(dir, "d.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	go h.serve(ln, stop)
	go h.churn(stop, envFloat("AGENT_NET_MEASURE_HEARTBEATS", 3), envFloat("AGENT_NET_MEASURE_EVENTS", 0.3))

	model := tui.New(tui.NewDaemonSource(sock))
	p := tea.NewProgram(model, tea.WithInput(nil), tea.WithOutput(io.Discard), tea.WithWindowSize(220, 60))
	done := make(chan struct{})
	go func() { p.Run(); close(done) }()
	pause(time.Second)
	p.Send(tea.KeyPressMsg{Code: rune(tabKey[0]), Text: tabKey})
	// Let the first full load finish, then measure the steady state.
	pause(8 * time.Second)
	h.mu.Lock()
	h.calls, h.bytes = map[string]int{}, map[string]int{}
	h.mu.Unlock()
	cpu0, start := cpuSeconds(), time.Now()
	pause(time.Duration(seconds * float64(time.Second)))
	elapsed, cpu := time.Since(start), cpuSeconds()-cpu0
	h.mu.Lock()
	calls, bytes := h.calls, h.bytes
	h.mu.Unlock()
	p.Quit()
	close(stop)
	ln.Close()
	<-done

	var methods []string
	totalCalls, totalBytes := 0, 0
	for m := range calls {
		methods = append(methods, m)
		totalCalls += calls[m]
		totalBytes += bytes[m]
	}
	sort.Slice(methods, func(i, j int) bool { return bytes[methods[i]] > bytes[methods[j]] })
	scale := 600 / elapsed.Seconds()
	var b strings.Builder
	fmt.Fprintf(&b, "\ntab %s, %.0f s measured, heartbeats %.1f/s, events %.1f/s\n", tabKey, elapsed.Seconds(),
		envFloat("AGENT_NET_MEASURE_HEARTBEATS", 3), envFloat("AGENT_NET_MEASURE_EVENTS", 0.3))
	fmt.Fprintf(&b, "%-26s %10s %14s %12s\n", "method", "calls/min", "bytes/10min", "bytes/call")
	for _, m := range methods {
		if calls[m] == 0 {
			continue
		}
		fmt.Fprintf(&b, "%-26s %10.1f %14.0f %12d\n", m, float64(calls[m])*60/elapsed.Seconds(), float64(bytes[m])*scale, bytes[m]/calls[m])
	}
	fmt.Fprintf(&b, "%-26s %10.1f %14.0f\n", "TOTAL", float64(totalCalls)*60/elapsed.Seconds(), float64(totalBytes)*scale)
	fmt.Fprintf(&b, "process CPU (TUI, daemon stand-in, gateway handlers, churn): %.0f%% of one core\n", cpu/elapsed.Seconds()*100)
	t.Log(b.String())
}

// pollProposalNodesRequest is the machine list the daemon asks for when it lists
// the network's proposals (see Daemon.networkProposals).
func pollProposalNodesRequest() []byte {
	b, _ := json.Marshal(protocol.QueryNodes{Brief: true})
	return b
}

func pollChangeRequest(a protocol.ChangesFindArgs) []byte {
	b, _ := json.Marshal(protocol.ChangeListRequest{Project: a.Project, State: a.State, Limit: a.Limit, After: a.Cursor, IfVersion: a.IfVersion, IfETag: a.IfETag, WantETag: a.WantETag, Lite: a.Lite})
	return b
}

func pollChangesResult(page protocol.ChangeListResult) protocol.ChangesFindResult {
	return protocol.ChangesFindResult{Items: page.Changes, Total: len(page.Changes), NextCursor: page.Next, NotModified: page.NotModified, ETag: page.ETag}
}
