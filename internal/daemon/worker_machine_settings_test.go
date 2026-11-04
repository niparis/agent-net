package daemon

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

func TestWorkerMachineSettingsPrecedence(t *testing.T) {
	d := &Daemon{cfg: Config{Hostname: "test-machine", Workers: WorkerSettings{Max: 8, Idle: 12 * time.Minute, StartGap: -1}}}
	snap := settings.Snapshot{Values: []settings.Value{
		{Key: "workers.max_per_machine", Level: "network", Scope: "network", Value: json.RawMessage(`2`)},
		{Key: "workers.idle_timeout", Level: "network", Scope: "network", Value: json.RawMessage(`"5m"`)},
		{Key: "workers.max_age", Level: "network", Scope: "network", Value: json.RawMessage(`"3h"`)},
		{Key: "workers.start_gap", Level: "network", Scope: "network", Value: json.RawMessage(`"9s"`)},
	}}
	got := d.workerSettingsWith(snap, "project")
	if got.Max != 8 || got.Idle != 12*time.Minute || got.MaxAge != 3*time.Hour || got.StartGap >= 0 {
		t.Fatalf("effective: %+v", got)
	}
	rows, err := d.workerSettingsViews(snap, nil, protocol.SettingsReadRequest{Key: "workers.max_per_machine"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || string(rows[0].Value) != "8" || rows[0].Source != "node.json on test-machine" {
		t.Fatalf("view: %+v", rows)
	}
}

func TestWorkerMachineSettingsAppliedToAdmission(t *testing.T) {
	st, err := openState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	d := &Daemon{st: st, workers: map[string]*worker{"one": {}, "two": {}}, cfg: Config{LoadProbe: func() MachineLoad { return MachineLoad{} }}}
	snap := settings.Snapshot{Values: []settings.Value{{Key: "workers.max_per_machine", Level: "network", Scope: "network", Value: json.RawMessage(`2`)}}}
	d.settingsSnapshot.Store(&snap)
	if got := d.admission("", ""); got.OK || got.Room != protocol.RoomCap {
		t.Fatalf("network cap not applied: %+v", got)
	}
	d.cfg.Workers.Max = 3
	if got := d.admission("", ""); !got.OK {
		t.Fatalf("node.json cap not applied: %+v", got)
	}
}

func TestWorkerMachineSettingsReadPaths(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNodeCfg(t, g, nodeOpts{cmdline: "/usr/bin/pi"}, func(c *Config) { c.Hostname = "test-machine"; c.Workers.Max = 8 })
	cli := cliClient(t, n)
	cli.mustCall("settings_write", map[string]any{"changes": []any{map[string]any{"key": "workers.max_per_machine", "level": "network", "scope": "network", "value": 2}}, "reason": "test"})
	q := map[string]any{"key": "workers.max_per_machine"}
	agent, _ := helloPi(t, n, "settings-machine-reader", "/work/fixture", true)
	for _, item := range []struct {
		method string
		client *localClient
	}{{"settings_read", agent}, {"settings_owner_read", cli}} {
		raw := item.client.mustCall(item.method, q)
		var rows []protocol.SettingView
		if err := json.Unmarshal(raw, &rows); err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || string(rows[0].Value) != "8" || rows[0].Source != "node.json on test-machine" {
			t.Fatalf("%s: %s", item.method, raw)
		}
	}
	out, err := n.d.readCTOTool(context.Background(), "agent_settings_read", json.RawMessage(`{"key":"workers.max_per_machine"}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(out)
	var read struct {
		Settings []protocol.SettingView `json:"settings"`
	}
	if err := json.Unmarshal(raw, &read); err != nil || len(read.Settings) != 1 || read.Settings[0].Source != "node.json on test-machine" {
		t.Fatalf("CTO: %s %v", raw, err)
	}
}
