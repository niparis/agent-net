package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestCTOOnboardingSharedAndOwnerOnly(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n1 := startNodeCfg(t, g, nodeOpts{cmdline: "/usr/bin/pi", interval: time.Hour}, nil)
	agent, _ := helloPi(t, n1, "onboarding-agent", "/w/onboarding", true)
	owner := cliClient(t, n1)
	owner.mustCall("onboarding_set", map[string]any{"step": "see", "state": "done"})
	n2 := startNodeCfg(t, g, nodeOpts{cmdline: "/usr/bin/pi", interval: time.Hour}, nil)
	var got struct {
		Steps map[string]string `json:"steps"`
	}
	if err := json.Unmarshal(cliClient(t, n2).mustCall("onboarding_get", nil), &got); err != nil || got.Steps["see"] != "done" {
		t.Fatalf("shared progress: %+v %v", got, err)
	}
	for _, method := range []string{"onboarding_get", "onboarding_set", "onboarding_restart"} {
		if _, err := agent.call(method, map[string]any{}); err == nil || !strings.Contains(err.Error(), "only available to the owner") {
			t.Fatalf("%s gate: %v", method, err)
		}
	}
	value, err := n1.d.runCTOToolRaw(context.Background(), "settings_change", json.RawMessage(`{"area":"onboarding"}`))
	if err != nil {
		t.Fatal(err)
	}
	assertCTOImmediate(t, value)
	got.Steps = nil
	if err := json.Unmarshal(owner.mustCall("onboarding_get", nil), &got); err != nil || len(got.Steps) != 0 {
		t.Fatalf("restart: %+v %v", got, err)
	}
}
