package daemon

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// decodeCTOArgs decodes one CTO write action's arguments strictly. A field the
// action does not have is refused by name, so a misspelled or stale field can
// never be dropped and leave a stored value at its zero value. required names
// the fields a setter must include: an explicit empty value still clears, a
// missing one does not.
func decodeCTOArgs(action string, raw json.RawMessage, dst any, required ...string) error {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("bad %s arguments: %v", action, err)
	}
	if len(required) == 0 {
		return nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return fmt.Errorf("bad %s arguments: %v", action, err)
	}
	for _, name := range required {
		if _, ok := fields[name]; !ok {
			return fmt.Errorf("%s needs %s", action, name)
		}
	}
	return nil
}
