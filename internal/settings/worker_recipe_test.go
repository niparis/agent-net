package settings_test

import (
	"testing"

	. "github.com/niparis/agent-net/internal/settings"
)

func TestWorkerRecipeDefinitions(t *testing.T) {
	expected := map[string]string{
		"workers.isolation_minimum": `"git-default"`, "workers.sandbox_level": `"sandboxed"`,
		"workers.max_age": `"4h"`, "workers.idle_timeout": `"60m"`, "workers.caller_gone_grace": `"10m"`, "workers.queue_timeout": `"2h"`,
		"workers.max_per_machine": `12`, "workers.cpu_target": `75`, "workers.cpu_hot": `95`, "workers.memory_reserve": `1610612736`,
		"workers.memory_reserve_min": `1610612736`, "workers.memory_reserve_share": `0.1`, "workers.cost_default_ram": `629145600`,
		"workers.cost_default_cores": `1`, "workers.cost_history_limit": `20`, "workers.start_gap": `"6s"`,
		"workers.auto_continue": `true`, "workers.auto_continue_max": `2`,
	}
	recipe := Standard()
	snap := Snapshot{}
	for key, want := range expected {
		def, ok := recipe.Definition(key)
		if !ok || !def.Live {
			t.Errorf("%s missing or not live", key)
			continue
		}
		got, err := snap.Resolve(key, Where{})
		if err != nil || string(got.Value) != want {
			t.Errorf("%s = %s, want %s (%v)", key, got.Value, want, err)
		}
	}
	for _, key := range []string{"workers.brief_preamble", "workers.brief_task", "workers.brief_no_delegation", "workers.brief_handback", "workers.brief_handback_partial", "workers.brief_blocked", "workers.brief_approval", "workers.brief_codex_tools", "workers.brief_codex_final", "workers.brief_codex_complete", "workers.brief_codex_blocked", "workers.brief_claude", "workers.continue_prompt_text", "workers.continue_intro", "workers.continue_intro_branch", "workers.continue_intro_folder", "workers.continue_intro_empty"} {
		def, ok := recipe.Definition(key)
		if !ok || !def.Live || snap.String(key, Where{}) == "" {
			t.Errorf("%s missing, empty or not live", key)
		}
	}
}
