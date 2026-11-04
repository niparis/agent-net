// Package settings holds the embedded recipe and immutable snapshot resolver.
package settings

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/niparis/agent-net/internal/markers"
)

//go:embed standard.yaml
var standardYAML []byte

func StandardYAML() []byte { return bytes.Clone(standardYAML) }
func RecipeHash() string   { return fmt.Sprintf("%x", sha256.Sum256(standardYAML)) }

type LedgerEntry struct {
	Row  int      `yaml:"row"`
	Keys []string `yaml:"keys"`
	Step int      `yaml:"step"`
	Code string   `yaml:"code"`
}
type Definition struct {
	Key         string               `yaml:"key"`
	Rows        []int                `yaml:"rows"`
	Description string               `yaml:"description"`
	Kind        string               `yaml:"kind"`
	Type        string               `yaml:"type"`
	Values      []string             `yaml:"values"`
	Stricter    string               `yaml:"stricter"`
	Min         yaml.Node            `yaml:"min"`
	Max         yaml.Node            `yaml:"max"`
	Levels      []string             `yaml:"levels"`
	Default     yaml.Node            `yaml:"default"`
	ByMaturity  map[string]yaml.Node `yaml:"by_maturity"`
	DefaultGo   string               `yaml:"default_go"`
	Slots       []string             `yaml:"slots"`
	Visibility  string               `yaml:"visibility"`
	Live        bool                 `yaml:"live"`
}
type Recipe struct {
	Format      int           `yaml:"format"`
	Definitions []Definition  `yaml:"settings"`
	Ledger      []LedgerEntry `yaml:"ledger"`
	index       map[string]int
}

var standard = func() *Recipe {
	r, err := Load(standardYAML)
	if err != nil {
		panic(err)
	}
	return r
}()

func DefinitionFor(key string) (Definition, bool) {
	d, ok := standard.Definition(key)
	d.Values = append([]string(nil), d.Values...)
	return d, ok
}

func Standard() *Recipe {
	r, err := Load(standardYAML)
	if err != nil {
		panic(err)
	}
	return r
}
func (r *Recipe) Definition(key string) (Definition, bool) {
	i, ok := r.index[key]
	if !ok {
		return Definition{}, false
	}
	return r.Definitions[i], true
}

func Load(data []byte) (*Recipe, error) {
	var r Recipe
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, err
	}
	if err := checkYAMLFieldTypes(&document); err != nil {
		return nil, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&r); err != nil {
		return nil, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("extra YAML document")
	}
	if r.Format != 1 {
		return nil, fmt.Errorf("unsupported format")
	}
	r.index = map[string]int{}
	for i := range r.Definitions {
		d := &r.Definitions[i]
		if d.Key == "" || d.Description == "" {
			return nil, fmt.Errorf("missing key or description")
		}
		if _, ok := r.index[d.Key]; ok {
			return nil, fmt.Errorf("duplicate key %s", d.Key)
		}
		r.index[d.Key] = i
		if d.Kind != "rule" && d.Kind != "advice" {
			return nil, fmt.Errorf("%s: invalid kind", d.Key)
		}
		if d.Visibility != "" && d.Visibility != "public" && d.Visibility != "owner" {
			return nil, fmt.Errorf("%s: invalid visibility", d.Key)
		}
		seen := map[string]bool{}
		for _, l := range d.Levels {
			if seen[l] || !contains([]string{"network", "repo", "session", "run"}, l) {
				return nil, fmt.Errorf("%s: invalid level", d.Key)
			}
			seen[l] = true
		}
		if !seen["network"] {
			return nil, fmt.Errorf("%s: network required", d.Key)
		}
		if d.Kind == "rule" && len(d.Levels) > 1 {
			switch d.Type {
			case "enum":
				if len(d.Values) < 2 {
					return nil, fmt.Errorf("%s: enum order required", d.Key)
				}
			case "bool":
				if d.Stricter != "true" && d.Stricter != "false" {
					return nil, fmt.Errorf("%s: stricter required", d.Key)
				}
			case "int", "float", "duration":
				if d.Stricter != "lower" && d.Stricter != "higher" {
					return nil, fmt.Errorf("%s: stricter required", d.Key)
				}
			case "set":
				if d.Stricter != "fewer" && d.Stricter != "more" {
					return nil, fmt.Errorf("%s: stricter required", d.Key)
				}
			case "text":
				if !d.OwnerScopedText() || seen["session"] || seen["run"] {
					return nil, fmt.Errorf("%s: unordered rule", d.Key)
				}
			default:
				return nil, fmt.Errorf("%s: unordered rule", d.Key)
			}
		}
		if d.Default.Kind == 0 && d.DefaultGo == "" {
			return nil, fmt.Errorf("%s: default required", d.Key)
		}
		if d.Default.Kind != 0 {
			if _, err := d.yamlValue(&d.Default); err != nil {
				return nil, fmt.Errorf("%s default: %w", d.Key, err)
			}
		}
		for m, v := range d.ByMaturity {
			if !contains([]string{"early", "growing", "mature", "critical"}, m) {
				return nil, fmt.Errorf("invalid maturity %s", m)
			}
			if _, err := d.yamlValue(&v); err != nil {
				return nil, fmt.Errorf("%s maturity: %w", d.Key, err)
			}
		}
	}
	seen := map[int]bool{}
	for _, l := range r.Ledger {
		if l.Row < 1 || l.Row > 149 || seen[l.Row] || (len(l.Keys) == 0) == (l.Code == "") {
			return nil, fmt.Errorf("invalid ledger row %d", l.Row)
		}
		seen[l.Row] = true
	}
	if len(seen) != 149 {
		return nil, fmt.Errorf("incomplete ledger")
	}
	return &r, nil
}

// YAML normally coerces scalars into strings. Recipe metadata must have the
// declared YAML type as well as values having the declared setting type.
func checkYAMLFieldTypes(doc *yaml.Node) error {
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("recipe must be a mapping")
	}
	root := doc.Content[0]
	for i := 0; i+1 < len(root.Content); i += 2 {
		key, n := root.Content[i].Value, root.Content[i+1]
		if key != "settings" {
			continue
		}
		if n.Kind != yaml.SequenceNode {
			return fmt.Errorf("settings must be a sequence")
		}
		for _, item := range n.Content {
			if item.Kind != yaml.MappingNode {
				return fmt.Errorf("definition must be a mapping")
			}
			for j := 0; j+1 < len(item.Content); j += 2 {
				name, v := item.Content[j].Value, item.Content[j+1]
				switch name {
				case "key", "description", "kind", "type", "default_go", "visibility":
					if v.Tag != "!!str" {
						return fmt.Errorf("%s must be a string", name)
					}
				case "stricter":
					if v.Tag != "!!str" && v.Tag != "!!bool" {
						return fmt.Errorf("stricter must be a string or bool")
					}
				case "live":
					if v.Tag != "!!bool" {
						return fmt.Errorf("live must be a bool")
					}
				case "levels", "values", "slots":
					if v.Kind != yaml.SequenceNode {
						return fmt.Errorf("%s must be a sequence", name)
					}
					for _, entry := range v.Content {
						if entry.Tag != "!!str" {
							return fmt.Errorf("%s items must be strings", name)
						}
					}
				case "rows":
					if v.Kind != yaml.SequenceNode {
						return fmt.Errorf("rows must be a sequence")
					}
					for _, entry := range v.Content {
						if entry.Tag != "!!int" {
							return fmt.Errorf("row must be an integer")
						}
					}
				}
			}
		}
	}
	return nil
}
func contains(a []string, s string) bool {
	for _, x := range a {
		if x == s {
			return true
		}
	}
	return false
}

// OwnerScopedText is protected wording whose network and repo values are
// individually owner-controlled. Text has no automatic strictness ordering.
func (d Definition) OwnerScopedText() bool {
	return d.Kind == "rule" && d.Type == "text" && d.Stricter == "owner"
}

func (d Definition) yamlValue(n *yaml.Node) (json.RawMessage, error) {
	var v any
	switch d.Type {
	case "bool":
		if n.Tag != "!!bool" {
			return nil, fmt.Errorf("expected bool")
		}
		var b bool
		if err := n.Decode(&b); err != nil {
			return nil, err
		}
		v = b
	case "int":
		if n.Tag != "!!int" {
			return nil, fmt.Errorf("expected int")
		}
		var i int64
		if err := n.Decode(&i); err != nil {
			return nil, err
		}
		v = i
	case "float":
		if n.Tag != "!!float" && n.Tag != "!!int" {
			return nil, fmt.Errorf("expected float")
		}
		var f float64
		if err := n.Decode(&f); err != nil {
			return nil, err
		}
		v = f
	case "string", "text", "duration", "enum":
		if n.Tag != "!!str" {
			return nil, fmt.Errorf("expected string")
		}
		v = n.Value
	case "set":
		if n.Kind != yaml.SequenceNode {
			return nil, fmt.Errorf("expected set")
		}
		a := []string{}
		for _, x := range n.Content {
			if x.Tag != "!!str" {
				return nil, fmt.Errorf("expected string item")
			}
			a = append(a, x.Value)
		}
		v = a
	default:
		return nil, fmt.Errorf("unknown type %s", d.Type)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return d.Codec(b)
}

var slotRE = regexp.MustCompile(`\{\{([^{}]+)\}\}`)

func (d Definition) Codec(raw json.RawMessage) (json.RawMessage, error) {
	return d.codec(raw, false)
}

// CodecExpected canonicalizes a previously stored value for a conflict check.
// Old guidance containing markers may be named here so it can be replaced or
// removed, but Codec still refuses to store it as a new value.
func (d Definition) CodecExpected(raw json.RawMessage) (json.RawMessage, error) {
	return d.codec(raw, true)
}

func (d Definition) codec(raw json.RawMessage, expected bool) (json.RawMessage, error) {
	raw = bytes.TrimSpace(raw)
	if !json.Valid(raw) || string(raw) == "null" {
		return nil, fmt.Errorf("invalid JSON value")
	}
	var value any
	switch d.Type {
	case "bool":
		var v bool
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		value = v
	case "int":
		if len(raw) > 0 && raw[0] == '"' {
			return nil, fmt.Errorf("expected integer")
		}
		var n json.Number
		if err := json.Unmarshal(raw, &n); err != nil {
			return nil, err
		}
		i, err := strconv.ParseInt(string(n), 10, 64)
		if err != nil {
			return nil, err
		}
		if err = d.bounds(float64(i), i); err != nil {
			return nil, err
		}
		value = i
	case "float":
		var f float64
		if err := json.Unmarshal(raw, &f); err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, fmt.Errorf("invalid float")
		}
		if err := d.bounds(f, 0); err != nil {
			return nil, err
		}
		value = f
	case "duration":
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, err
		}
		v, err := time.ParseDuration(s)
		if err != nil {
			return nil, err
		}
		if err = d.bounds(float64(v), int64(v)); err != nil {
			return nil, err
		}
		value = s
	case "string", "text", "enum":
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, err
		}
		if d.Key == "delivery.worker_branch_prefix" && (!regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*/$`).MatchString(s) || strings.Contains(s, "..") || strings.Contains(s, "//") || strings.Contains(s, "/.") || strings.Contains(s, ".lock/")) {
			return nil, fmt.Errorf("invalid worker branch prefix")
		}
		if strings.HasPrefix(d.Key, "delivery.trailer_") && !regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]*$`).MatchString(s) {
			return nil, fmt.Errorf("invalid trailer name")
		}
		if d.Type == "enum" && !contains(d.Values, s) {
			return nil, fmt.Errorf("unknown enum value %q", s)
		}
		if !expected && (d.Type == "text" || (strings.HasPrefix(d.Key, "guidance.") && d.Type == "string")) {
			lower := strings.ToLower(s)
			// The existing worker-brief marker is quoted in the standard recipe;
			// it is a reference to a brief, not an agent-net notice.
			notices := s
			if d.Key == "guidance.worker_brief_precedence" {
				notices = strings.ReplaceAll(notices, "[agent-net worker brief]", "")
			}
			if strings.Contains(lower, "<!-- agent-net:begin -->") || strings.Contains(lower, "<!-- agent-net:end -->") || markers.Neutralize(notices) != notices {
				return nil, fmt.Errorf("text contains an agent-net marker")
			}
		}
		if !expected && d.Type == "text" && (strings.HasPrefix(d.Key, "delivery.worker_") || strings.HasPrefix(d.Key, "workers.brief_") || strings.HasPrefix(d.Key, "workers.continue_")) && strings.Contains(s, "\n\nTask:\n") {
			return nil, fmt.Errorf("worker text contains a task boundary")
		}
		if d.Type == "text" {
			found := map[string]bool{}
			for _, m := range slotRE.FindAllStringSubmatch(s, -1) {
				if !contains(d.Slots, m[1]) {
					return nil, fmt.Errorf("unknown slot %s", m[1])
				}
				found[m[1]] = true
			}
			for _, slot := range d.Slots {
				// Existing custom guidance may omit the worker reference;
				// worker brief text slots themselves remain mandatory.
				if d.Key == "guidance.worker_brief_precedence" && slot == "handback" {
					continue
				}
				if !found[slot] {
					return nil, fmt.Errorf("missing slot %s", slot)
				}
			}
		}
		value = s
	case "set":
		var elements []json.RawMessage
		if err := json.Unmarshal(raw, &elements); err != nil || elements == nil {
			return nil, fmt.Errorf("invalid set")
		}
		for _, element := range elements {
			if len(element) == 0 || element[0] != '"' {
				return nil, fmt.Errorf("set items must be strings")
			}
		}
		var a []string
		if err := json.Unmarshal(raw, &a); err != nil || a == nil {
			return nil, fmt.Errorf("invalid set")
		}
		seen := map[string]bool{}
		for _, x := range a {
			if seen[x] {
				return nil, fmt.Errorf("duplicate set item")
			}
			seen[x] = true
		}
		sort.Strings(a)
		value = a
	default:
		return nil, fmt.Errorf("unknown type %s", d.Type)
	}
	b, err := json.Marshal(value)
	return b, err
}
func (d Definition) bounds(f float64, i int64) error {
	for _, b := range []struct {
		n   *yaml.Node
		min bool
	}{{&d.Min, true}, {&d.Max, false}} {
		if b.n.Kind == 0 {
			continue
		}
		if d.Type == "int" || d.Type == "duration" {
			var bound int64
			if d.Type == "int" {
				if b.n.Tag != "!!int" {
					return fmt.Errorf("integer bound required")
				}
				if err := b.n.Decode(&bound); err != nil {
					return err
				}
			} else {
				if b.n.Tag != "!!str" {
					return fmt.Errorf("duration bound required")
				}
				duration, err := time.ParseDuration(b.n.Value)
				if err != nil {
					return err
				}
				bound = int64(duration)
			}
			if (b.min && i < bound) || (!b.min && i > bound) {
				return fmt.Errorf("value outside bounds")
			}
		} else {
			if b.n.Tag != "!!int" && b.n.Tag != "!!float" {
				return fmt.Errorf("numeric bound required")
			}
			v, err := strconv.ParseFloat(b.n.Value, 64)
			if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
				return fmt.Errorf("invalid bound")
			}
			if (b.min && f < v) || (!b.min && f > v) {
				return fmt.Errorf("value outside bounds")
			}
		}
	}
	return nil
}

// StricterValue returns the stricter combination. Sets use intersection or union.
func (d Definition) StricterValue(a, b json.RawMessage) (json.RawMessage, error) {
	a, err := d.Codec(a)
	if err != nil {
		return nil, err
	}
	b, err = d.Codec(b)
	if err != nil {
		return nil, err
	}
	switch d.Type {
	case "bool":
		if string(a) == d.Stricter {
			return a, nil
		}
		return b, nil
	case "enum":
		var x, y string
		json.Unmarshal(a, &x)
		json.Unmarshal(b, &y)
		for _, v := range d.Values {
			if v == x {
				return b, nil
			}
			if v == y {
				return a, nil
			}
		}
	case "int", "duration":
		var x, y int64
		if d.Type == "int" {
			json.Unmarshal(a, &x)
			json.Unmarshal(b, &y)
		} else {
			var s, t string
			json.Unmarshal(a, &s)
			json.Unmarshal(b, &t)
			xx, _ := time.ParseDuration(s)
			yy, _ := time.ParseDuration(t)
			x = int64(xx)
			y = int64(yy)
		}
		if (d.Stricter == "lower" && x <= y) || (d.Stricter == "higher" && x >= y) {
			return a, nil
		}
		return b, nil
	case "float":
		var x, y float64
		json.Unmarshal(a, &x)
		json.Unmarshal(b, &y)
		if (d.Stricter == "lower" && x <= y) || (d.Stricter == "higher" && x >= y) {
			return a, nil
		}
		return b, nil
	case "set":
		var x, y []string
		json.Unmarshal(a, &x)
		json.Unmarshal(b, &y)
		z := []string{}
		for _, s := range x {
			if d.Stricter == "more" || contains(y, s) {
				z = append(z, s)
			}
		}
		if d.Stricter == "more" {
			for _, s := range y {
				if !contains(z, s) {
					z = append(z, s)
				}
			}
		}
		sort.Strings(z)
		return json.Marshal(z)
	}
	return nil, fmt.Errorf("type %s has no ordering", d.Type)
}

type Value struct {
	Key   string          `json:"key"`
	Level string          `json:"level"`
	Scope string          `json:"scope"`
	Item  string          `json:"item"`
	Value json.RawMessage `json:"value"`
}
type Snapshot struct {
	DeliveryCutover      bool              `json:"delivery_cutover,omitempty"`
	WorkerSandboxCutover bool              `json:"worker_sandbox_cutover,omitempty"`
	Revision             int64             `json:"revision"`
	Recipe               string            `json:"recipe"`
	Values               []Value           `json:"values"`
	Maturity             map[string]string `json:"maturity"`
}
type Host struct{ RAMBytes int64 }
type Where struct {
	Repo         string
	Session, Run map[string]json.RawMessage
	Host         Host
}
type Effective struct {
	Value    json.RawMessage
	Source   string
	Revision int64
	Raised   bool
}

func (s Snapshot) Clone() Snapshot {
	b, _ := json.Marshal(s)
	var c Snapshot
	json.Unmarshal(b, &c)
	return c
}
func (s Snapshot) Resolve(key string, w Where) (Effective, error) {
	d, ok := standard.Definition(key)
	if !ok {
		return Effective{}, fmt.Errorf("unknown setting %s", key)
	}
	var v json.RawMessage
	var err error
	if d.Default.Kind != 0 {
		v, err = d.yamlValue(&d.Default)
	} else if d.DefaultGo == "memory_reserve" {
		var minimum int64
		var share float64
		if e, eerr := s.Resolve("workers.memory_reserve_min", Where{}); eerr == nil {
			_ = json.Unmarshal(e.Value, &minimum)
		}
		if e, eerr := s.Resolve("workers.memory_reserve_share", Where{}); eerr == nil {
			_ = json.Unmarshal(e.Value, &share)
		}
		n := int64(float64(w.Host.RAMBytes) * share)
		if n < minimum {
			n = minimum
		}
		v, _ = json.Marshal(n)
	} else {
		err = fmt.Errorf("unknown default function %s", d.DefaultGo)
	}
	if err != nil {
		return Effective{}, err
	}
	e := Effective{Value: v, Source: "network default", Revision: s.Revision}
	networkSet := false
	apply := func(raw json.RawMessage, source string) {
		candidate, err := d.Codec(raw)
		if err != nil {
			log.Printf("settings: ignoring invalid %s at %s: %v", key, source, err)
			return
		}
		if d.Kind == "rule" && !d.OwnerScopedText() && (networkSet || source == "session" || source == "run") {
			bound, err := d.StricterValue(e.Value, candidate)
			if err == nil {
				if !bytes.Equal(bound, candidate) {
					e.Raised = true
				}
				if bytes.Equal(bound, e.Value) && !bytes.Equal(bound, candidate) {
					return
				}
				candidate = bound
			}
		}
		e.Value = candidate
		e.Source = source
	}
	find := func(level, scope string) json.RawMessage {
		for _, x := range s.Values {
			if x.Key == key && x.Level == level && x.Scope == scope && x.Item == "" {
				return x.Value
			}
		}
		return nil
	}
	// The network value replaces the file default, even if it is looser.
	if v := find("network", "network"); v != nil {
		if vv, err := d.Codec(v); err == nil {
			e.Value = vv
			e.Source = "network"
			networkSet = true
		} else {
			log.Printf("settings: ignoring invalid %s at network: %v", key, err)
		}
	}
	if w.Repo != "" && contains(d.Levels, "repo") {
		v := find("repo", w.Repo)
		if v != nil {
			apply(v, "repo "+w.Repo)
		} else if m := strings.TrimSpace(s.Maturity[w.Repo]); m != "" {
			level, explanation, hasExplanation := strings.Cut(m, " - ")
			m = level
			if hasExplanation && strings.TrimSpace(explanation) == "" {
				m = ""
			}
			if n, ok := d.ByMaturity[m]; ok {
				if v, err := d.yamlValue(&n); err == nil {
					apply(v, "repo "+w.Repo+", maturity "+m)
				}
			}
		}
	}
	if contains(d.Levels, "session") {
		if v := w.Session[key]; v != nil {
			apply(v, "session")
		}
	}
	if contains(d.Levels, "run") {
		if v := w.Run[key]; v != nil {
			apply(v, "run")
		}
	}
	return e, nil
}
