package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

func TestWorkerRecipeRuntime(t *testing.T) {
	snap := settings.Snapshot{Values: []settings.Value{
		{Key: "workers.auto_continue", Level: "repo", Scope: "shop", Value: json.RawMessage(`false`)},
		{Key: "workers.auto_continue_max", Level: "network", Scope: "network", Value: json.RawMessage(`1`)},
		{Key: "workers.max_age", Level: "repo", Scope: "shop", Value: json.RawMessage(`"30m"`)},
		{Key: "workers.cpu_hot", Level: "network", Scope: "network", Value: json.RawMessage(`80`)},
		{Key: "workers.cost_default_ram", Level: "network", Scope: "network", Value: json.RawMessage(`123456`)},
		{Key: "workers.cost_default_cores", Level: "network", Scope: "network", Value: json.RawMessage(`2`)},
	}}
	if autoContinueEnabledWith(snap, "shop", nil) {
		t.Error("repo auto continue ignored")
	}
	w := &worker{autoContinue: true, sessionID: "s"}
	if autoContinueEligibleWith(w, snap, "shop") {
		t.Error("disabled repo was continued")
	}
	if !autoContinueEligibleWith(w, snap, "other") {
		t.Error("enabled repo could not continue")
	}
	w.autoContinueCount = 1
	if autoContinueEligibleWith(w, snap, "other") {
		t.Error("continue maximum ignored")
	}
	w.autoContinue = false
	w.autoContinueCount = 0
	if autoContinueEligibleWith(w, snap, "other") {
		t.Error("run opted out but was continued")
	}
	d := &Daemon{}
	if got := d.workerSettingsWith(snap, "shop").MaxAge; got != 30*time.Minute {
		t.Errorf("repo max age = %s", got)
	}
	if c := defaultWorkerCostWith(snap); c.RAM != 123456 || c.Cores != 2 {
		t.Errorf("cost = %+v", c)
	}
	load := MachineLoad{Known: true, Cores: 4, CPU1: 85, CPU5: 10, Total: 16 << 30, Avail5: 8 << 30, Samples: 3}
	if got := admitWithHot((WorkerSettings{}).filled(), load, WorkerCost{}, nil, 0, time.Time{}, time.Now(), 80); got.OK || got.Room != protocol.RoomCPU {
		t.Errorf("hot cap = %+v", got)
	}
}

func TestWorkerRecipeIsolation(t *testing.T) {
	snap := settings.Snapshot{Values: []settings.Value{{Key: "workers.isolation_minimum", Level: "repo", Scope: "shop", Value: json.RawMessage(`"worktree-required"`)}}}
	if err := validateWorkerIsolation(snap, "shop", protocol.IsolationNone); err == nil {
		t.Fatal("required worktree allowed isolation none")
	}
	if err := validateWorkerIsolation(snap, "shop", protocol.IsolationWorktree); err != nil {
		t.Fatal(err)
	}
	if err := validateWorkerIsolation(snap, "elsewhere", protocol.IsolationNone); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerRecipeCostHistoryAndMemory(t *testing.T) {
	t.Parallel()
	snap := settings.Snapshot{Values: []settings.Value{
		{Key: "workers.memory_reserve_min", Level: "network", Scope: "network", Value: json.RawMessage(`100`)},
		{Key: "workers.memory_reserve_share", Level: "network", Scope: "network", Value: json.RawMessage(`0.25`)},
	}}
	got, err := snap.Resolve("workers.memory_reserve", settings.Where{Host: settings.Host{RAMBytes: 1000}})
	if err != nil || string(got.Value) != "250" {
		t.Fatalf("reserve: %s %v", got.Value, err)
	}
	st, err := openState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	for i := 0; i < 4; i++ {
		if err := st.addCost(workerCost{Harness: "test", Model: "m", EndedAt: time.Now()}, 2); err != nil {
			t.Fatal(err)
		}
	}
	costs, err := st.costs("test", "m")
	if err != nil || len(costs) != 2 {
		t.Fatalf("cost retention: %d %v", len(costs), err)
	}
}

func TestWorkerRecipeQueuedIsolationReleasesBase(t *testing.T) {
	d := &Daemon{}
	snap := settings.Snapshot{Values: []settings.Value{{Key: "workers.isolation_minimum", Level: "network", Scope: "network", Value: json.RawMessage(`"worktree-required"`)}}}
	d.settingsSnapshot.Store(&snap)
	d.reserveWorkerBase("feature")
	_, err := d.runWorkerStart(context.Background(), protocol.WorkerLaunch{Project: "shop", Isolation: protocol.IsolationNone, Base: "feature"})
	if err == nil {
		t.Fatal("queued start bypassed required worktree")
	}
	if d.startingBases["feature"] != 0 {
		t.Fatal("refused queued start kept its branch reservation")
	}
}

func TestWorkerRecipeAppliedToWorkerLaunch(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	result, err := g.gw.Store().WriteSettings([]protocol.SettingsChange{
		{Key: "workers.isolation_minimum", Level: "repo", Scope: "shop", Value: json.RawMessage(`"worktree-required"`)},
		{Key: "workers.brief_preamble", Level: "repo", Scope: "shop", Value: json.RawMessage(`"Project specific worker introduction."`)},
	}, "owner", "test")
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "worker recipe snapshot", func() bool { return w.d.SettingsSnapshot().Revision >= result.Revision })
	_, err = w.startDefault(protocol.WorkerStartRequest{Model: "fake/m1", Project: "shop", Harness: "opencode", Task: "test", Isolation: protocol.IsolationNone})
	if err == nil || !strings.Contains(err.Error(), "requires worktree") {
		t.Fatalf("isolation: %v", err)
	}
	res := w.mustStart(t, protocol.WorkerStartRequest{Model: "fake/m1", Project: "shop", Harness: "opencode", Task: "FAKE:handback", Isolation: protocol.IsolationWorktree})
	if res.Task == nil || !strings.HasPrefix(res.Task.Body, "[agent-net worker brief] Project specific worker introduction.") {
		t.Fatalf("brief: %+v", res.Task)
	}
}
