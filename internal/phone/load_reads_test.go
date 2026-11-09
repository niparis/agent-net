package phone

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/operator"
	"github.com/niparis/agent-net/internal/protocol"
)

type countingSource struct {
	*richOperator
	snapshotCalls int
	calls         map[string]int
	bytes         int
}

func (f *countingSource) Snapshot(ctx context.Context) (operator.Snapshot, error) {
	f.snapshotCalls++
	// Model the concrete operator's full Snapshot wire reads for the before
	// measurement. Each response uses the same fixture as granular reads.
	for _, method := range []string{protocol.OpInfo, protocol.OpAgents, protocol.OpNodes, "notification_settings", protocol.OpTimeZoneGet, protocol.OpWorkerProfiles, protocol.OpWorkerProvidersGet, protocol.OpProtectedProvidersGet, protocol.OpWorkerFloorsGet, protocol.OpScoresGet, "difficulty_get", protocol.OpWorkerRotationGet, "learned_list", protocol.OpGraphGet, protocol.OpProjects, protocol.OpWorkerLog} {
		_, _ = f.Call(ctx, method, nil)
	}
	return f.richOperator.Snapshot(ctx)
}
func (f *countingSource) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	f.calls[method]++
	raw, err := f.richOperator.Call(ctx, method, params)
	f.bytes += len(raw)
	return raw, err
}
func TestLoadReadsOnlyVisibleScreen(t *testing.T) {
	header := []string{protocol.OpInfo, protocol.OpCounts}
	for _, tc := range []struct {
		screen string
		reads  []string
	}{
		{"desk", []string{protocol.OpAsksList, protocol.OpAgents, protocol.OpChangesOwnerFind, protocol.OpChiefSorts, protocol.OpDeskLog}},
		{"sessions", []string{protocol.OpAgents, protocol.OpNodes, protocol.OpUsage, protocol.OpGraphView, protocol.OpConversations}},
		{"changes", []string{protocol.OpAgents, protocol.OpChangesOwnerFind}},
		{"messages", []string{protocol.OpAgents, protocol.OpConversations, protocol.OpConversationMessages}},
		{"graph", []string{protocol.OpAgents, protocol.OpGraphView, protocol.OpChiefGet, protocol.OpConversations}},
		{"workers", []string{protocol.OpAgents, protocol.OpNodes, protocol.OpWorkerProfiles, protocol.OpScoresGet, protocol.OpWorkerRotationGet, protocol.OpProtectedProvidersGet, protocol.OpUsage}},
	} {
		t.Run(tc.screen, func(t *testing.T) {
			f := &countingSource{richOperator: newRichOperator(), calls: map[string]int{}}
			a := &app{src: f}
			a.load(context.Background(), tc.screen, query{})
			firstBytes := f.bytes
			firstCalls := 0
			for _, n := range f.calls {
				firstCalls += n
			}
			t.Logf("first load: calls=%d bytes=%d snapshot=%d methods=%v", firstCalls, firstBytes, f.snapshotCalls, f.calls)
			if f.snapshotCalls != 0 {
				t.Errorf("full Snapshot called %d times", f.snapshotCalls)
			}
			var got []string
			for method := range f.calls {
				got = append(got, method)
			}
			want := append(append([]string{}, header...), tc.reads...)
			sort.Strings(got)
			sort.Strings(want)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("reads=%v want=%v", got, want)
			}
			before := map[string]int{}
			for k, v := range f.calls {
				before[k] = v
			}
			a.load(context.Background(), tc.screen, query{})
			if !reflect.DeepEqual(before, f.calls) {
				t.Errorf("second load reread: before=%v after=%v", before, f.calls)
			}
			t.Logf("second load: additional bytes=%d", f.bytes-firstBytes)
		})
	}
}

// Serve the old snapshot fixture through the granular wire methods. This
// preserves the existing page assertions without a production Snapshot call.
func snapshotFixtureRead(ctx context.Context, f interface {
	Snapshot(context.Context) (operator.Snapshot, error)
}, method string) (json.RawMessage, bool) {
	switch method {
	case protocol.OpInfo, protocol.OpCounts, protocol.OpAgents, protocol.OpNodes, protocol.OpWorkerProfiles, protocol.OpScoresGet, protocol.OpWorkerRotationGet, protocol.OpProtectedProvidersGet, protocol.OpWorkerLog:
	default:
		return nil, false
	}
	snap, _ := f.Snapshot(ctx)
	var v any
	switch method {
	case protocol.OpInfo:
		v = protocol.OperatorInfo{Connected: snap.Connected, NodeID: snap.NodeID, OwnerSession: snap.OwnerSession}
	case protocol.OpCounts:
		counts := protocol.SessionCounts{}
		if cf, ok := f.(*consoleOperator); ok {
			for _, ask := range cf.asks {
				if ask.State != protocol.AskWithSomeone && askRank(ask) != 9 {
					counts.ConsoleAsks++
				}
			}
		}
		if rf, ok := f.(*richOperator); ok {
			for _, ask := range rf.asks {
				if ask.State != protocol.AskWithSomeone && askRank(ask) != 9 {
					counts.ConsoleAsks++
				}
			}
		}
		for _, n := range snap.Nodes {
			if n.Online {
				counts.MachinesOnline++
			}
		}
		for _, a := range snap.Agents {
			if !protocol.IsOwner(a.Harness) {
				counts.ConsoleSessions++
			}
		}
		v = counts
	case protocol.OpAgents:
		v = protocol.AgentsResult{Agents: snap.Agents}
	case protocol.OpNodes:
		v = protocol.NodesInfo{Nodes: snap.Nodes}
	case protocol.OpWorkerProfiles:
		v = snap.WorkerProfiles
	case protocol.OpScoresGet:
		v = snap.Scores
	case protocol.OpWorkerRotationGet:
		v = snap.WorkerRotation
	case protocol.OpProtectedProvidersGet:
		v = snap.ProtectedProviders
	case protocol.OpWorkerLog:
		v = snap.WorkerLog
	}
	raw, _ := json.Marshal(v)
	return raw, true
}

func TestScreenReadsShareBaseAndExpire(t *testing.T) {
	f := &countingSource{richOperator: newRichOperator(), calls: map[string]int{}}
	a := &app{src: f}
	a.load(context.Background(), "desk", query{})
	a.load(context.Background(), "messages", query{})
	for _, method := range []string{protocol.OpInfo, protocol.OpAgents, protocol.OpCounts} {
		if f.calls[method] != 1 {
			t.Errorf("%s read %d times across screens", method, f.calls[method])
		}
	}
	a.cacheMu.Lock()
	for _, entry := range a.cache {
		entry.until = time.Time{}
	}
	a.cacheMu.Unlock()
	a.load(context.Background(), "messages", query{})
	if f.calls[protocol.OpAgents] != 2 {
		t.Errorf("expired agents read %d times", f.calls[protocol.OpAgents])
	}
	a.clearReads()
	a.load(context.Background(), "messages", query{})
	if f.calls[protocol.OpAgents] != 3 {
		t.Errorf("invalidated agents read %d times", f.calls[protocol.OpAgents])
	}
}

func TestWorkersUseLiveSessionsWithoutHistory(t *testing.T) {
	snap := operator.Snapshot{
		Agents: []protocol.AgentView{
			{SessionID: "s_live", Harness: "codex", Online: true, Status: "busy", Profile: protocol.Profile{Name: "live worker"}, Worker: &protocol.WorkerInfo{Task: "live task", Tokens: 1234}},
			{SessionID: "s_offline", Harness: "codex", Worker: &protocol.WorkerInfo{Task: "old task"}},
			{SessionID: "s_ended", Harness: "codex", Online: true, Status: "ended", Worker: &protocol.WorkerInfo{Task: "ended task"}},
		},
		WorkerLog: []protocol.WorkerLogEntry{{SessionID: "s_old", Status: "running", Title: "stale log"}},
	}
	rows := runningWorkers(snap)
	if len(rows) != 1 || rows[0].Name != "live worker" || rows[0].Task != "live task" {
		t.Fatalf("running rows=%+v", rows)
	}
}

func TestRunningWorkersStayNewestFirst(t *testing.T) {
	snap := operator.Snapshot{Agents: []protocol.AgentView{
		{SessionID: "s_older", Harness: "codex", Online: true, Profile: protocol.Profile{Name: "older"}, Worker: &protocol.WorkerInfo{StartedAt: 1}},
		{SessionID: "s_newer", Harness: "codex", Online: true, Profile: protocol.Profile{Name: "newer"}, Worker: &protocol.WorkerInfo{StartedAt: 2}},
	}}
	rows := runningWorkers(snap)
	if len(rows) != 2 || rows[0].Name != "newer" {
		t.Fatalf("running order=%+v", rows)
	}
}
