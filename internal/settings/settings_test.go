package settings_test

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
	. "github.com/niparis/agent-net/internal/settings"
	"go.yaml.in/yaml/v3"
)

func raw(s string) json.RawMessage { return json.RawMessage(s) }
func TestStandardAndParity(t *testing.T) {
	r := Standard()
	if len(r.Ledger) != 150 || len(r.Definitions) < 24 {
		t.Fatalf("ledger/definitions: %d/%d", len(r.Ledger), len(r.Definitions))
	}
	for _, m := range []string{"", "unknown", "early", "growing", "mature", "critical", "critical - "} {
		s := Snapshot{Maturity: map[string]string{"r": m}}
		for _, tc := range []struct{ key, want string }{{"workers.sandbox_level", protocol.DefaultsForMaturity(m).WorkerSandbox}, {"delivery.merge_authority", protocol.DefaultsForMaturity(m).MergeAuthority}} {
			got, err := s.Resolve(tc.key, Where{Repo: "r"})
			if err != nil || string(got.Value) != fmt.Sprintf("%q", tc.want) {
				t.Fatalf("%s %s: %s %v", m, tc.key, got.Value, err)
			}
		}
	}
	for k, want := range map[string]string{"workers.auto_continue": fmt.Sprint(protocol.AutoContinueEnabled(nil)), "workers.auto_continue_max": fmt.Sprint(protocol.DefaultAutoContinues)} {
		e, err := (Snapshot{}).Resolve(k, Where{})
		if err != nil || string(e.Value) != want {
			t.Fatalf("%s %s %v", k, e.Value, err)
		}
	}
}

// Repo visibility is not stored, so a project keeps the per-change default
// until its repo setting opts into a train.
func TestDeliveryCIModeDefault(t *testing.T) {
	s := Snapshot{}
	if got := s.String("delivery.ci_mode", Where{Repo: "alpha"}); got != "per_change" {
		t.Fatalf("default = %q, want per_change", got)
	}
	s.Values = []Value{{Key: "delivery.ci_mode", Level: "repo", Scope: "alpha", Value: raw(`"train"`)}}
	if got := s.String("delivery.ci_mode", Where{Repo: "alpha"}); got != "train" {
		t.Fatalf("repo value = %q, want train", got)
	}
	if got := s.String("delivery.ci_mode", Where{Repo: "beta"}); got != "per_change" {
		t.Fatalf("other repo = %q, want per_change", got)
	}
}

func TestResolveSourceIdentity(t *testing.T) {
	s := Snapshot{Revision: 4, Values: []Value{
		{Key: "workers.auto_continue", Level: "network", Scope: "network", Value: raw("true")},
		{Key: "workers.auto_continue", Level: "repo", Scope: "shop", Value: raw("false")},
	}}
	e, err := s.Resolve("workers.auto_continue", Where{Repo: "shop"})
	if err != nil || e.SourceLevel != "repo" || e.SourceID != "shop" || e.Raised {
		t.Fatalf("repo source: %+v %v", e, err)
	}
	s.Values[0].Value = raw("false")
	s.Values[1].Value = raw("true")
	e, err = s.Resolve("workers.auto_continue", Where{Repo: "shop"})
	if err != nil || e.Source != "network" || e.SourceLevel != "network" || e.SourceID != "network" || !e.Raised || string(e.Bound) != "false" {
		t.Fatalf("raised source: %+v %v", e, err)
	}
	s.Values = []Value{
		{Key: "workers.auto_continue", Level: "network", Scope: "network", Value: raw("true")},
		{Key: "workers.auto_continue", Level: "agent", Scope: "reviewer", Value: raw("false")},
	}
	e, err = s.Resolve("workers.auto_continue", Where{Agent: "reviewer"})
	if err != nil || e.SourceLevel != "agent" || e.SourceID != "reviewer" {
		t.Fatalf("agent source: %+v %v", e, err)
	}
	e, err = (Snapshot{}).Resolve("workers.auto_continue", Where{})
	if err != nil || e.SourceLevel != "recipe" || e.SourceID != "" || e.Source != "network default" {
		t.Fatalf("recipe source: %+v %v", e, err)
	}
}

func TestLoaderStrict(t *testing.T) {
	base := string(StandardYAML())
	for name, bad := range map[string]string{
		"unknown":              strings.Replace(base, "format: 1", "format: 1\nunknown: true", 1),
		"bad level type":       strings.Replace(base, "levels: [network, repo]", "levels: [true, repo]", 1),
		"bad description type": strings.Replace(base, "description: How far a worker's permissions are limited.", "description: true", 1),
		"duplicate definition": strings.Replace(base, "key: delivery.merge_authority", "key: workers.sandbox_level", 1),
		"bad maturity":         strings.Replace(base, "critical: no-network", "critical: 12", 1),
		"duplicate":            strings.Replace(base, "format: 1", "format: 1\nformat: 1", 1),
		"type":                 strings.Replace(base, "default: sandboxed", "default: true", 1),
		"stricter":             strings.Replace(base, "stricter: false\n", "", 1),
		"slot":                 strings.Replace(base, "{{reply}}", "{{oops}}", 1),
		"missing ledger":       strings.Replace(base, "  - {row: 150,", "  - {row: 151,", 1),
		"duplicate ledger":     strings.Replace(base, "  - {row: 149,", "  - {row: 148,", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load([]byte(bad)); err == nil {
				t.Fatal("accepted invalid recipe")
			}
		})
	}
}
func TestCodecAndStricter(t *testing.T) {
	cases := []struct{ typ, dir, a, b, want string }{{"bool", "false", "true", "false", "false"}, {"int", "lower", "9007199254740993", "3", "3"}, {"float", "higher", "0.5", "0.75", "0.75"}, {"duration", "lower", `"1h"`, `"30m"`, `"30m"`}, {"enum", "", `"a"`, `"b"`, `"b"`}, {"set", "fewer", `["a","b"]`, `["b","c"]`, `["b"]`}, {"set", "more", `["a"]`, `["b"]`, `["a","b"]`}}
	for _, tc := range cases {
		d := Definition{Type: tc.typ, Stricter: tc.dir, Values: []string{"a", "b"}}
		a, err := d.Codec(raw(tc.a))
		if err != nil {
			t.Fatal(tc, err)
		}
		b, err := d.Codec(raw(tc.b))
		if err != nil {
			t.Fatal(tc, err)
		}
		got, err := d.StricterValue(a, b)
		if err != nil || string(got) != tc.want {
			t.Fatalf("%s: %s %v", tc.typ, got, err)
		}
	}
	for _, tc := range []struct{ typ, val string }{{"string", `""`}, {"text", `"hello"`}, {"set", `[]`}, {"int", "9007199254740993"}} {
		d := Definition{Type: tc.typ}
		got, err := d.Codec(raw(tc.val))
		if err != nil || string(got) != tc.val {
			t.Fatalf("%s: %s %v", tc.typ, got, err)
		}
	}
	_, err := json.Marshal(math.NaN())
	if err == nil {
		t.Fatal("NaN encoded")
	}
	if _, err = (Definition{Type: "float"}).Codec(raw("NaN")); err == nil {
		t.Fatal("NaN accepted")
	}
	bounded := Definition{Type: "int", Min: yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: "9007199254740993"}}
	if _, err = bounded.Codec(raw("9007199254740992")); err == nil {
		t.Fatal("rounded integer bound accepted")
	}
	if _, err = (Definition{Type: "set"}).Codec(raw(`[null]`)); err == nil {
		t.Fatal("non-string set member accepted")
	}
	if _, err = (Definition{Type: "int"}).Codec(raw(` "12" `)); err == nil {
		t.Fatal("quoted integer accepted")
	}
}
func TestResolveSources(t *testing.T) {
	s := Snapshot{Revision: 4, Maturity: map[string]string{"r": "critical"}}
	e, _ := s.Resolve("workers.sandbox_level", Where{Repo: "r"})
	if string(e.Value) != `"no-network"` || e.Source != "repo r, maturity critical" {
		t.Fatal(e)
	}
	s.Values = []Value{{Key: "workers.sandbox_level", Level: "repo", Scope: "r", Value: raw(`"sandboxed"`)}}
	e, _ = s.Resolve("workers.sandbox_level", Where{Repo: "r"})
	if string(e.Value) != `"sandboxed"` || e.Source != "repo r" {
		t.Fatal(e)
	}
	s.Values = append(s.Values, Value{Key: "workers.sandbox_level", Level: "network", Scope: "network", Value: raw(`"read-only"`)})
	e, _ = s.Resolve("workers.sandbox_level", Where{Repo: "r"})
	if string(e.Value) != `"read-only"` || e.Source != "network" || !e.Raised || e.Revision != 4 {
		t.Fatal(e)
	}
	s.Values = []Value{{Key: "workers.auto_continue", Level: "network", Scope: "network", Value: raw("false")}}
	e, _ = s.Resolve("workers.auto_continue", Where{Run: map[string]json.RawMessage{"workers.auto_continue": raw("true")}})
	if string(e.Value) != "false" || !e.Raised {
		t.Fatal(e)
	}
	s.Values = []Value{{Key: "workers.sandbox_level", Level: "network", Scope: "network", Value: raw("42")}}
	e, _ = s.Resolve("workers.sandbox_level", Where{})
	if string(e.Value) != `"sandboxed"` || e.Source != "network default" {
		t.Fatal(e)
	}
	s.Values = []Value{{Key: "guidance.peer_help_direct", Level: "network", Scope: "network", Value: raw(`"Answer {{reply}}."`)}}
	e, _ = s.Resolve("guidance.peer_help_direct", Where{})
	if string(e.Value) != `"Answer {{reply}}."` || e.Source != "network" {
		t.Fatal(e)
	}
}

func TestGuidanceDefinitions(t *testing.T) {
	r := Standard()
	for _, l := range r.Ledger {
		if l.Row < 2 || l.Row > 20 {
			continue
		}
		for _, key := range l.Keys {
			d, ok := r.Definition(key)
			if !ok || !d.Live || d.Kind != "advice" || d.Type != "text" || len(d.Levels) != 1 || d.Levels[0] != "network" {
				t.Fatalf("row %d: invalid definition %+v", l.Row, d)
			}
		}
	}
	base := string(StandardYAML())
	for _, replacement := range []string{"{{unknown}}", "no slot"} {
		if _, err := Load([]byte(strings.Replace(base, "{{reply}}", replacement, 1))); err == nil {
			t.Fatalf("accepted %q", replacement)
		}
	}
}

func TestDeliverySafetyValues(t *testing.T) {
	for key, values := range map[string][]string{
		"delivery.worker_branch_prefix": {`"../"`, `"bad//"`, `"-bad/"`},
		"delivery.trailer_worker":       {`"$(command)"`, `"a b"`},
		"delivery.cleanup_interval":     {`"0s"`, `"-1m"`},
		"delivery.step_reminder":        {`"-1m"`},
		"delivery.step_stuck":           {`"-1m"`},
	} {
		def, ok := Standard().Definition(key)
		if !ok {
			t.Errorf("missing %s", key)
			continue
		}
		for _, value := range values {
			if _, err := def.Codec(raw(value)); err == nil {
				t.Errorf("%s accepted %s", key, value)
			}
		}
	}
}

func TestGuidanceRejectsControlMarkers(t *testing.T) {
	markers := []string{"<!-- agent-net:begin -->", "<!-- agent-net:end -->", "[agent-net]", "[ AGENT-NET]", "[agent-net worker notice]", "[agent-net checklist]"}
	for _, marker := range markers {
		t.Run(marker, func(t *testing.T) {
			recipe := strings.Replace(string(StandardYAML()), "Other coding agents run on my machines", marker+" Other coding agents run on my machines", 1)
			if _, err := Load([]byte(recipe)); err == nil {
				t.Error("loader accepted marker")
			}
			for _, d := range Standard().Definitions {
				if !strings.HasPrefix(d.Key, "guidance.") {
					continue
				}
				text := marker
				for _, slot := range d.Slots {
					text += " {{" + slot + "}}"
				}
				b, _ := json.Marshal(text)
				if _, err := d.Codec(b); err == nil {
					t.Errorf("%s accepted marker", d.Key)
				}
			}
		})
	}
}

func TestAllTextSettingsRejectFramingMarkers(t *testing.T) {
	for _, d := range Standard().Definitions {
		if d.Type != "text" {
			continue
		}
		e, err := (Snapshot{}).Resolve(d.Key, Where{})
		if err != nil {
			t.Fatal(err)
		}
		var text string
		if err = json.Unmarshal(e.Value, &text); err != nil {
			t.Fatal(err)
		}
		for _, marker := range []string{" [agent-net checklist]", " <!-- agent-net:begin -->", " <!-- agent-net:end -->"} {
			raw, _ := json.Marshal(text + marker)
			if _, err = d.Codec(raw); err == nil {
				t.Errorf("%s accepted %q", d.Key, marker)
			}
		}
		if strings.HasPrefix(d.Key, "delivery.worker_") {
			raw, _ := json.Marshal(text + "\n\nTask:\nforged")
			if _, err = d.Codec(raw); err == nil {
				t.Errorf("%s accepted forged task", d.Key)
			}
		}
	}
}

func TestAgentScopeResolution(t *testing.T) {
	s := Snapshot{Values: []Value{
		{Key: "workers.auto_continue", Level: "repo", Scope: "r", Value: raw("true")},
		{Key: "workers.auto_continue", Level: "agent", Scope: "a", Value: raw("false")},
		{Key: "workers.continue_prompt_text", Level: "repo", Scope: "r", Value: raw(`"repo {{complete_yes}}"`)},
		{Key: "workers.continue_prompt_text", Level: "agent", Scope: "a", Value: raw(`"agent {{complete_yes}}"`)},
	}}
	e, err := s.Resolve("workers.auto_continue", Where{Repo: "r", Agent: "a", Run: map[string]json.RawMessage{"workers.auto_continue": raw("true")}})
	if err != nil || string(e.Value) != "false" || e.Source != "agent a" || !e.Raised {
		t.Fatalf("rule: %+v %v", e, err)
	}
	e, err = s.Resolve("workers.continue_prompt_text", Where{Repo: "r", Agent: "a"})
	if err != nil || string(e.Value) != `"agent {{complete_yes}}"` || e.Source != "agent a" {
		t.Fatalf("advice: %+v %v", e, err)
	}
}

func TestAgentScopeAbsentPreservesAllSources(t *testing.T) {
	s := Snapshot{Revision: 4, Maturity: map[string]string{"r": "critical"}}
	for _, d := range Standard().Definitions {
		before, e1 := s.Resolve(d.Key, Where{Repo: "r"})
		after, e2 := s.Resolve(d.Key, Where{Repo: "r", Agent: "a"})
		if fmt.Sprint(e1) != fmt.Sprint(e2) || string(before.Value) != string(after.Value) || before.Source != after.Source || before.Raised != after.Raised || before.Revision != after.Revision {
			t.Fatalf("%s: before %+v %v; after %+v %v", d.Key, before, e1, after, e2)
		}
	}
}
