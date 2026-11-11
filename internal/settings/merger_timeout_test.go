package settings

import (
	"encoding/json"
	"testing"
	"time"
)

func TestMergerTestTimeoutSetting(t *testing.T) {
	def, ok := DefinitionFor("merger.test_timeout")
	if !ok {
		t.Fatal("merger.test_timeout is not in the recipe")
	}
	if def.Kind != "rule" || def.Type != "duration" || def.Stricter != "lower" {
		t.Fatalf("definition: %+v", def)
	}
	levels := map[string]bool{}
	for _, l := range def.Levels {
		levels[l] = true
	}
	if !levels["network"] || !levels["repo"] || len(def.Levels) != 2 {
		t.Fatalf("levels: %v, want network and repo", def.Levels)
	}
	e, err := Snapshot{Recipe: RecipeHash(), Maturity: map[string]string{}}.Resolve("merger.test_timeout", Where{})
	if err != nil {
		t.Fatal(err)
	}
	var text string
	if err := json.Unmarshal(e.Value, &text); err != nil {
		t.Fatal(err)
	}
	got, err := time.ParseDuration(text)
	if err != nil {
		t.Fatalf("%q: %v", text, err)
	}
	if got != 90*time.Minute {
		t.Fatalf("default %s, want 90m", got)
	}
}
