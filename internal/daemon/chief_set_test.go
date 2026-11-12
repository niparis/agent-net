package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The owner's chief_set refuses an argument it does not have, names it, and
// leaves the chief in place. The same call with no argument is refused, and
// only an explicit clear unassigns the chief. Model fields reach the gateway.
func TestChiefSetOperatorDoesWhatItNamesOrRefuses(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNodeCfg(t, g, nodeOpts{cmdline: "/usr/bin/pi", interval: time.Hour}, nil)
	_, chief := helloPi(t, n, "chief-set", "/w/chief", true)
	cli := cliClient(t, n)
	if err := g.gw.Store().SetChief(protocol.ChiefSetRequest{SessionID: chief}, "owner"); err != nil {
		t.Fatal(err)
	}
	chiefNow := func() string {
		t.Helper()
		st, err := g.gw.Store().ChiefStatus()
		if err != nil {
			t.Fatal(err)
		}
		return st.SessionID
	}
	for _, c := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"foo": 1}, "foo"},
		{map[string]any{}, "chief_set needs"},
		{map[string]any{"harness": "claude", "model": "claude-nonesuch-9", "foo": 1}, "foo"},
		{map[string]any{"harness": "claude", "model": "claude-nonesuch-9"}, "claude-nonesuch-9"},
		{map[string]any{"clear": true, "session_id": chief}, "clear cannot be combined"},
	} {
		if _, err := cli.call("chief_set", c.args); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("chief_set %v: err = %v, want it to contain %q", c.args, err, c.want)
		}
		if got := chiefNow(); got != chief {
			t.Fatalf("chief = %q after a refused chief_set %v, want %q", got, c.args, chief)
		}
	}
	cli.mustCall("chief_set", map[string]any{"clear": true})
	if got := chiefNow(); got != "" {
		t.Fatalf("chief = %q after clear, want none", got)
	}
	cli.mustCall("chief_set", map[string]any{"session_id": chief})
	if got := chiefNow(); got != chief {
		t.Fatalf("chief = %q after naming it, want %q", got, chief)
	}
}
