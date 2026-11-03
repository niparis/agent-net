package settings

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
	"go.yaml.in/yaml/v3"
)

func raw(s string) json.RawMessage { return json.RawMessage(s) }
func TestStandardAndParity(t *testing.T) {
	r := Standard()
	if len(r.Ledger) != 149 || len(r.Definitions) != 20 {
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
		"missing ledger":       strings.Replace(base, "  - {row: 149,", "  - {row: 150,", 1),
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
