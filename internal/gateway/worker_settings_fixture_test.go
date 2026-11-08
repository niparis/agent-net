package gateway

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestWorkerSandboxBaseline(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.AddArchitect("shop", "owner"); err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, maturity := range []string{"early", "growing", "mature", "critical"} {
		if err := s.seedLegacyCharter("shop", "maturity", maturity, "owner"); err != nil {
			t.Fatal(err)
		}
		for _, override := range []string{"", protocol.WorkersSandboxed, protocol.WorkersNoNetwork, protocol.WorkersReadOnly} {
			if err := s.SetNodeSetting("shop", protocol.SettingWorkerSandbox, override, "owner"); err != nil {
				t.Fatal(err)
			}
			for _, policy := range []string{"", protocol.WorkersSandboxed, protocol.WorkersNoNetwork, protocol.WorkersReadOnly} {
				// Project policy and graph writes now address the same repo
				// setting; reproduce the old winner for each fixture case.
				winner := override
				if policy != "" {
					winner = policy
				}
				if err := s.SetNodeSetting("shop", protocol.SettingWorkerSandbox, winner, "owner"); err != nil {
					t.Fatal(err)
				}
				got, err := s.WorkerPermissions("n_test", "shop")
				if err != nil {
					t.Fatal(err)
				}
				out[maturity+"/"+override+"/"+policy] = got
			}
		}
	}
	got, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	const path = "testdata/worker-sandbox-baseline.json"
	if os.Getenv("UPDATE_WORKER_SANDBOX_FIXTURE") == "1" {
		if err := os.WriteFile(path, got, 0644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("worker sandbox differs from baseline fixture")
	}
}
