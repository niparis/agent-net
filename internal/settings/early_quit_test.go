package settings_test

import (
	"encoding/json"
	"testing"
	"time"

	. "github.com/niparis/agent-net/internal/settings"
)

func TestWorkerEarlyQuitThreshold(t *testing.T) {
	def, ok := Standard().Definition("workers.early_quit_threshold")
	if !ok || def.Kind != "advice" {
		t.Fatalf("definition: %+v", def)
	}
	snap := Snapshot{Values: []Value{{Key: def.Key, Level: "network", Scope: "network", Value: json.RawMessage(`"60s"`)}, {Key: def.Key, Level: "repo", Scope: "demo", Value: json.RawMessage(`"30s"`)}}}
	if got := (Snapshot{}).Duration(def.Key, Where{}); got != 120*time.Second {
		t.Fatalf("default %v", got)
	}
	if got := snap.Duration(def.Key, Where{}); got != 60*time.Second {
		t.Fatalf("network %v", got)
	}
	if got := snap.Duration(def.Key, Where{Repo: "demo"}); got != 30*time.Second {
		t.Fatalf("repo %v", got)
	}
}
