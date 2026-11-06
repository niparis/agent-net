package toolcatalog

import (
	"encoding/json"
	"strings"
)

// PruneOutput removes fields unknown to this catalog from closed output
// objects. It follows the catalog's local references, arrays and map values.
// Open objects (including arbitrary JSON settings and action data) keep their
// fields. Validation must still run afterwards: pruning does not check types
// or fill in required fields. Raw values preserve large integers exactly.
func PruneOutput(schema, value json.RawMessage) (json.RawMessage, error) {
	var root map[string]any
	if err := json.Unmarshal(schema, &root); err != nil {
		return nil, err
	}
	var prune func(map[string]any, json.RawMessage) (json.RawMessage, error)
	prune = func(s map[string]any, raw json.RawMessage) (json.RawMessage, error) {
		if ref, ok := s["$ref"].(string); ok && strings.HasPrefix(ref, "#/") {
			var target any = root
			for _, part := range strings.Split(ref[2:], "/") {
				obj, ok := target.(map[string]any)
				if !ok {
					break
				}
				target = obj[strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")]
			}
			if target, ok := target.(map[string]any); ok {
				return prune(target, raw)
			}
		}
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		switch value.(type) {
		case map[string]any:
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(raw, &fields); err != nil {
				return nil, err
			}
			properties, _ := s["properties"].(map[string]any)
			for key, field := range fields {
				property, declared := properties[key]
				child, _ := property.(map[string]any)
				if !declared {
					if closed, ok := s["additionalProperties"].(bool); ok && !closed {
						delete(fields, key)
						continue
					}
					child, _ = s["additionalProperties"].(map[string]any)
				}
				if child != nil {
					out, err := prune(child, field)
					if err != nil {
						return nil, err
					}
					fields[key] = out
				}
			}
			return json.Marshal(fields)
		case []any:
			if child, ok := s["items"].(map[string]any); ok {
				var items []json.RawMessage
				if err := json.Unmarshal(raw, &items); err != nil {
					return nil, err
				}
				for i, item := range items {
					out, err := prune(child, item)
					if err != nil {
						return nil, err
					}
					items[i] = out
				}
				return json.Marshal(items)
			}
		}
		return raw, nil
	}
	return prune(root, value)
}
