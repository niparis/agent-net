package daemon

import (
	"bytes"
	"encoding/json"
)

// redactedEnvValue takes the place of an enter plan's env values in a reply an
// agent reads.
const redactedEnvValue = "[redacted]"

// envObjectMarker is the compact form json.Marshal gives an env map. A reply
// without it cannot hold an env map, so it is sent as it is, with one byte
// search added to the write.
var envObjectMarker = []byte(`"env":{`)

// writeReply sends a reply on the connection. The owner's command line and TUI
// (client "cli") get the reply as it is: they start the client with the env.
// Every other client is an agent, whose reply loses the env values but keeps
// the names, so the plan still shows what it sets.
func (lc *localConn) writeReply(m map[string]any) error {
	lc.mu.Lock()
	owner := lc.client == "cli"
	lc.mu.Unlock()
	if owner {
		return lc.writeJSON(m)
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	b, err = redactAgentReply(b)
	if err != nil {
		return err
	}
	return lc.writeLine(b)
}

// redactAgentReply returns the reply line with every value of an env map
// replaced. A line without an env map is returned unchanged.
func redactAgentReply(line []byte) ([]byte, error) {
	if !bytes.Contains(line, envObjectMarker) {
		return line, nil
	}
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if !redactEnvValues(v) {
		return line, nil
	}
	return json.Marshal(v)
}

// redactEnvValues walks a decoded reply and replaces the values of every
// object under an "env" key. It reports whether it replaced any.
func redactEnvValues(v any) bool {
	changed := false
	switch x := v.(type) {
	case map[string]any:
		for key, child := range x {
			if env, ok := child.(map[string]any); ok && key == "env" {
				for name := range env {
					env[name] = redactedEnvValue
					changed = true
				}
				continue
			}
			if redactEnvValues(child) {
				changed = true
			}
		}
	case []any:
		for _, child := range x {
			if redactEnvValues(child) {
				changed = true
			}
		}
	}
	return changed
}
