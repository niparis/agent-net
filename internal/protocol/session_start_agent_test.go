package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSessionStartAgentOptional(t *testing.T) {
	for _, agent := range []string{"", "chosen"} {
		raw, err := json.Marshal(SessionStartRequest{Project: "repo", AgentID: agent})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "agent_id") != (agent != "") {
			t.Fatalf("wire: %s", raw)
		}
		var relay SessionOwnerRequest
		if err = json.Unmarshal(raw, &relay); err != nil || relay.AgentID != agent {
			t.Fatalf("relay lost Agent: %+v %v", relay, err)
		}
	}
}
