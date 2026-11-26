package phone

import (
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// The session detail names the parent the gateway computed (decision 0193)
// and never works out a place of its own.
func TestSessionPlaceNamesTheGatewayParent(t *testing.T) {
	names := map[string]string{"s_lead": "proj lead"}
	for _, c := range []struct {
		ag   protocol.AgentView
		want string
	}{
		{protocol.AgentView{Parent: "s_lead"}, "under proj lead"},
		{protocol.AgentView{Parent: "s_lead", NoParent: "no lead"}, "no parent: no lead"},
		{protocol.AgentView{}, "answers to you"},
	} {
		if got := sessionPlace(c.ag, names); got != c.want {
			t.Errorf("sessionPlace(%+v) = %q, want %q", c.ag, got, c.want)
		}
	}
}
