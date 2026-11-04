package settings

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

// DeliveryAssignment is the graph edge that assigns a step to a node.
type DeliveryAssignment struct {
	Project string `json:"project"`
	Step    string `json:"step"`
	For     string `json:"for,omitempty"`
	AfterMerge bool `json:"after_merge,omitempty"`
	Node    string `json:"node"`
	Kind    string `json:"kind"`
}

func deliveryApproverShorthand(key string, raw json.RawMessage, repo string, assignments []DeliveryAssignment) json.RawMessage {
	var current string
	if json.Unmarshal(raw, &current) != nil {
		return raw
	}
	wantKind, none, delegate, owner := "approves", "full", "delegated", "ask"
	if key == "delivery.deploy_rule" {
		wantKind, none, delegate, owner = "approves_deploy", "brief", "ask", "owner"
	}
	for _, a := range assignments {
		if a.Project != repo || a.Kind != wantKind {
			continue
		}
		if current == owner {
			break
		}
		if current == delegate && a.Node != "/delivery/owner" {
			break
		}
		switch a.Node {
		case "/delivery/owner":
			current = owner
		case "/delivery/delegate":
			current = delegate
		case "/delivery/none":
			current = none
		default:
			current = "custom"
		}
		break
	}
	b, _ := json.Marshal(current)
	return b
}

// DeliveryCanonicalKey names the ordered pipeline component represented by a
// legacy delivery shorthand. Empty means the setting is not a shorthand.
func DeliveryCanonicalKey(key string) string {
	switch key {
	case "delivery.tests_before_merge":
		return "delivery.pipeline.merge.requires"
	case "delivery.merge_authority":
		return "delivery.pipeline.merge.approver"
	case "delivery.deploy_rule":
		return "delivery.pipeline.deploy.approver"
	}
	return ""
}

// DeliveryShorthandKey is the legacy view of a canonical component.
func DeliveryShorthandKey(key string) string {
	for _, alias := range []string{"delivery.tests_before_merge", "delivery.merge_authority", "delivery.deploy_rule"} {
		if DeliveryCanonicalKey(alias) == key {
			return alias
		}
	}
	return ""
}

// DeliveryCanonicalValue converts a shorthand value to its pipeline rule.
func DeliveryCanonicalValue(key string, raw json.RawMessage) (json.RawMessage, error) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("delivery shorthand must be a string")
	}
	var result any
	switch key {
	case "delivery.tests_before_merge":
		requires := []string{"accept", "review"}
		switch value {
		case "optional":
		case "suite":
			requires = append(requires, "test:suite")
		case "suite-new-tests":
			requires = append(requires, "test:suite", "test:tests_added")
		case "suite-new-tests-review":
			requires = append(requires, "test:suite", "test:tests_added", "review:findings_open=0")
		default:
			return nil, fmt.Errorf("invalid tests shorthand %q", value)
		}
		sort.Strings(requires)
		result = requires
	case "delivery.merge_authority":
		v := map[string]string{"full": "none", "delegated": "delegate", "ask": "owner"}[value]
		if v == "" {
			return nil, fmt.Errorf("invalid delivery shorthand %q", value)
		}
		result = v
	case "delivery.deploy_rule":
		v := map[string]string{"brief": "none", "ask": "delegate", "owner": "owner"}[value]
		if v == "" {
			return nil, fmt.Errorf("invalid delivery shorthand %q", value)
		}
		result = v
	default:
		return nil, fmt.Errorf("unknown delivery shorthand %q", key)
	}
	return json.Marshal(result)
}

// DeliveryShorthandValue reads a shorthand from one canonical pipeline rule.
// A combination the shorthand cannot express is reported as custom.
func DeliveryShorthandValue(key string, raw json.RawMessage) (json.RawMessage, error) {
	var choices []string
	switch key {
	case "delivery.tests_before_merge":
		choices = []string{"optional", "suite", "suite-new-tests", "suite-new-tests-review"}
	case "delivery.merge_authority":
		choices = []string{"full", "delegated", "ask"}
	case "delivery.deploy_rule":
		choices = []string{"brief", "ask", "owner"}
	default:
		return nil, fmt.Errorf("unknown delivery shorthand %q", key)
	}
	d, _ := DefinitionFor(DeliveryCanonicalKey(key))
	canonical, err := d.Codec(raw)
	if err != nil {
		return nil, err
	}
	for _, choice := range choices {
		b, _ := json.Marshal(choice)
		want, _ := DeliveryCanonicalValue(key, b)
		if bytes.Equal(canonical, want) {
			return b, nil
		}
	}
	return json.Marshal("custom")
}
