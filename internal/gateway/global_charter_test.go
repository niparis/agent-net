package gateway

import (
	"strings"
	"sync/atomic"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func setLevel(t *testing.T, s *Store, q protocol.CharterSetRequest) {
	t.Helper()
	if err := s.SetLevelCharter(q, "owner", false); err != nil {
		t.Fatal(err)
	}
}

// The owner sets a rule once at the global level. The charter joins rules
// across global, team and repo, so a repo with its own rules text no longer
// hides it, and the text a session reads every turn carries all three.
func TestContextTextCarriesGlobalAndTeamRulesThatTheRepoReplaced(t *testing.T) {
	r := newPlaceRig(t)
	setLevel(t, r.s, protocol.CharterSetRequest{Level: "global", Attr: "rules", Value: "report late defects with agent_worker_rate"})
	setLevel(t, r.s, protocol.CharterSetRequest{Level: "team", Node: "alpha", Attr: "rules", Value: "team taste: small commits"})
	setLevel(t, r.s, protocol.CharterSetRequest{Level: "repo", Node: "demo", Attr: "rules", Value: "repo house rule"})

	got, err := r.s.contextText(r.member)
	if err != nil {
		t.Fatal(err)
	}
	want := "rules: report late defects with agent_worker_rate; team taste: small commits; repo house rule"
	if !strings.Contains(got, want) {
		t.Fatalf("context lacks %q:\n%s", want, got)
	}
	// One line holds them: no block apart from the charter.
	if strings.Contains(got, "higher charter levels") || strings.Count(got, "report late defects") != 1 {
		t.Fatalf("a rule is said twice or in a separate block:\n%s", got)
	}
	c, err := r.s.EffectiveCharter("demo", "", "")
	if err != nil || !strings.Contains(c.Rules, "report late defects with agent_worker_rate") || !strings.Contains(c.Rules, "repo house rule") {
		t.Fatalf("effective charter: %+v %v", c, err)
	}
}

// A session in a folder with no node has no effective charter to carry the
// global level, so it gets the global level alone. This is the one case that
// needs the separate text.
func TestContextTextForASessionWithoutANodeStillHasTheGlobalLevel(t *testing.T) {
	s := teamStore(t)
	var node string
	if err := s.db.QueryRow(`SELECT node_id FROM sessions LIMIT 1`).Scan(&node); err != nil {
		t.Fatal(err)
	}
	loose, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "loose", Cwd: "/tmp/loose"})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.contextText(loose); err != nil || got != "" {
		t.Fatalf("no global level set, context %q %v", got, err)
	}
	setLevel(t, s, protocol.CharterSetRequest{Level: "global", Attr: "rules", Value: "report late defects"})
	setLevel(t, s, protocol.CharterSetRequest{Level: "global", Attr: "refuses", Value: "touching real setup"})
	got, err := s.contextText(loose)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"rules: report late defects", "refuses: touching real setup"} {
		if !strings.Contains(got, want) {
			t.Fatalf("context lacks %q:\n%s", want, got)
		}
	}
}

// charter.global is the global level alone, for a connection that has no
// session (a Codex MCP process before any thread calls it).
func TestGlobalCharterHasOnlyTheGlobalLevel(t *testing.T) {
	s := teamStore(t)
	if got, err := s.GlobalCharter(); err != nil || !got.Empty() {
		t.Fatalf("empty store: %+v %v", got, err)
	}
	setLevel(t, s, protocol.CharterSetRequest{Level: "global", Attr: "rules", Value: "report late defects"})
	setLevel(t, s, protocol.CharterSetRequest{Level: "global", Attr: "refuses", Value: "touching real setup"})
	setLevel(t, s, protocol.CharterSetRequest{Level: "repo", Node: "demo", Attr: "rules", Value: "repo house rule"})
	got, err := s.GlobalCharter()
	if err != nil || got.Rules != "report late defects" || got.Refuses != "touching real setup" || got.Escalate != "" {
		t.Fatalf("global level: %+v %v", got, err)
	}
}

// A charter or team write tells the daemons to drop what they cached, once the
// write is committed. A dry run and a failed write say nothing.
func TestCharterWritesNotifyTheDaemons(t *testing.T) {
	r := newPlaceRig(t)
	var n atomic.Int64
	r.s.charterHook = func() { n.Add(1) }
	was := n.Load()
	grew := func(what string) {
		t.Helper()
		if n.Load() <= was {
			t.Fatalf("%s did not notify", what)
		}
		was = n.Load()
	}
	setLevel(t, r.s, protocol.CharterSetRequest{Level: "global", Attr: "rules", Value: "report late defects"})
	grew("global rule")
	setLevel(t, r.s, protocol.CharterSetRequest{Level: "team", Node: "alpha", Attr: "rules", Value: "team taste"})
	grew("team rule")
	setLevel(t, r.s, protocol.CharterSetRequest{Level: "repo", Node: "demo", Attr: "rules", Value: "repo house rule"})
	grew("repo rule")
	if err := r.s.SetCharter("demo", "accepts", "bugs", "owner"); err != nil {
		t.Fatal(err)
	}
	grew("SetCharter")

	// Dry runs and refused writes change nothing, so they say nothing.
	if err := r.s.SetLevelCharter(protocol.CharterSetRequest{Level: "global", Attr: "rules", Value: "another"}, "owner", true); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetLevelCharter(protocol.CharterSetRequest{Level: "global", Attr: "rules", Value: "denied"}, "nobody", false); err == nil {
		t.Fatal("a stranger changed the global level")
	}
	if n.Load() != was {
		t.Fatalf("notified %d times for a dry run or a refused write", n.Load()-was)
	}
}
