package settings

import (
	"encoding/json"
	"testing"
)

func TestCompactionSettingsDefaults(t *testing.T) {
	s := Snapshot{}
	if got := s.Int("sessions.compaction_threshold_percent", Where{}); got != 80 {
		t.Fatalf("threshold default=%d, want 80", got)
	}
	if !s.Bool("sessions.idle_compaction", Where{}) || s.Int("sessions.idle_compaction_min_tokens", Where{}) != 200000 {
		t.Fatal("idle compaction secondary controls missing")
	}
	// Claude Code keeps its prompt cache for an hour; a 5-minute default
	// would compact every Claude session after 4.5 minutes idle.
	if got := s.Map("sessions.cache_window_seconds", Where{}); got["anthropic"] != 3600 || got["default"] != 300 {
		t.Fatalf("cache window default=%v, want anthropic 3600 and default 300", got)
	}
	for _, key := range []string{"sessions.compaction_threshold_percent", "sessions.cache_window_seconds", "sessions.idle_compaction", "sessions.idle_compaction_min_tokens"} {
		d, ok := DefinitionFor(key)
		if !ok || d.Kind != "advice" || !d.Live {
			t.Fatalf("%s must apply immediately for CTO", key)
		}
	}
	d, _ := DefinitionFor("sessions.compaction_threshold_percent")
	for _, level := range []string{"network", "team", "repo", "agent"} {
		found := false
		for _, l := range d.Levels {
			found = found || l == level
		}
		if !found {
			t.Fatalf("threshold has no %s level: %v", level, d.Levels)
		}
	}
	if _, ok := DefinitionFor("sessions.idle_compaction_after"); ok {
		t.Fatal("sessions.idle_compaction_after still exists")
	}
}

func TestCompactionThresholdTeamAndAgentOverride(t *testing.T) {
	s := Snapshot{
		Values: []Value{
			{Key: "sessions.compaction_threshold_percent", Level: "network", Scope: "network", Value: json.RawMessage("70")},
			{Key: "sessions.compaction_threshold_percent", Level: "team", Scope: "forge", Value: json.RawMessage("60")},
			{Key: "sessions.compaction_threshold_percent", Level: "repo", Scope: "shop", Value: json.RawMessage("55")},
			{Key: "sessions.compaction_threshold_percent", Level: "agent", Scope: "reviewer", Value: json.RawMessage("50")},
		},
		RepoAgents: map[string]string{"shop": "reviewer"},
		RepoTeams:  map[string]string{"shop": "forge"},
	}
	for _, tc := range []struct {
		name  string
		where Where
		want  int
	}{
		{"network", Where{}, 70},
		{"team", Where{Team: "forge"}, 60},
		{"repo", Where{Repo: "shop", Agent: "other"}, 55},
		{"agent", Where{Agent: "reviewer"}, 50},
		{"repo derives its team and agent", Where{Repo: "shop"}, 50},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := s.Int("sessions.compaction_threshold_percent", tc.where); got != tc.want {
				t.Fatalf("threshold=%d, want %d", got, tc.want)
			}
		})
	}
}

func TestCacheWindowMapCodec(t *testing.T) {
	d, _ := DefinitionFor("sessions.cache_window_seconds")
	if _, err := d.Codec(json.RawMessage(`{"anthropic":0}`)); err == nil {
		t.Fatal("accepted zero seconds")
	}
	if _, err := d.Codec(json.RawMessage(`{"anthropic":"300"}`)); err == nil {
		t.Fatal("accepted a string value")
	}
	v, err := d.Codec(json.RawMessage(`{"anthropic":600}`))
	if err != nil || string(v) != `{"anthropic":600}` {
		t.Fatalf("codec=%s %v", v, err)
	}
}
