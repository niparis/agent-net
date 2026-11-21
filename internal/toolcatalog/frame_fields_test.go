package toolcatalog

import (
	"encoding/json"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// nestedFrameArgs lists agent and worker arguments that have a frame field's
// name but never reach a flat frame. The local request nests its parameters,
// and the gateway frame carries these values under other names (message_type,
// pause_id), so the clash cannot happen. Any other such argument must be
// mapped in daemonFields.
var nestedFrameArgs = map[string][]string{
	"agent_message_send": {"type"},
	"agent_nudge_resume": {"id"},
}

// TestAgentArgumentsAvoidFrameFieldNames fails on an agent or worker argument
// named like a frame field (id, type, via) that is sent under its own name. The
// daemon would put it in a gateway frame, and the frame refuses it.
func TestAgentArgumentsAvoidFrameFieldNames(t *testing.T) {
	var offenders []string
	for _, tool := range Tools {
		if !slices.Contains(tool.Roles, RoleAgent) && !slices.Contains(tool.Roles, RoleWorker) {
			continue
		}
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
			t.Fatalf("%s: %v", tool.Name, err)
		}
		for name := range schema.Properties {
			if !slices.Contains(protocol.FrameFieldNames, name) || DaemonField(tool.Name, name) != name {
				continue
			}
			if slices.Contains(nestedFrameArgs[tool.Name], name) {
				continue
			}
			offenders = append(offenders, tool.Name+"."+name)
		}
	}
	sort.Strings(offenders)
	if len(offenders) > 0 {
		t.Errorf("arguments named like a frame field and not mapped in daemonFields: %s", strings.Join(offenders, ", "))
	}
}
