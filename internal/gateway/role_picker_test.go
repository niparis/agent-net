package gateway

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// roleGateway is a gateway with one machine that has the harnesses a role list
// can name. It is the shared setup of the role tests.
func roleGateway(t *testing.T) (*Gateway, string) {
	t.Helper()
	g, node := scopeGateway(t)
	st := g.store
	if err := st.SetNodeHello(node, "role-box", "1"); err != nil {
		t.Fatal(err)
	}
	launch := true
	if err := st.SetNodeLaunch(node, &launch); err != nil {
		t.Fatal(err)
	}
	harnesses := []protocol.HarnessInfo{
		{Name: "claude", SignedIn: "yes", Models: []string{"claude-opus-5-5"}},
		{Name: "codex", SignedIn: "yes", Models: []string{"gpt-6.1-sol"}},
		{Name: "opencode", SignedIn: "yes", Models: []string{"deepseek/deepseek-v4.1-flash"}},
	}
	if _, err := st.SetNodeHarnesses(node, harnesses); err != nil {
		t.Fatal(err)
	}
	return g, node
}

// setRoleList writes one role's ordered list as the owner.
func setRoleList(t *testing.T, g *Gateway, key string, models ...protocol.RoleModel) {
	t.Helper()
	value, _ := json.Marshal(protocol.FormatRoleModels(models))
	if _, err := g.store.WriteSettings([]SettingsChange{{Key: key, Level: "network", Scope: "network", Value: value}}, "owner", "test"); err != nil {
		t.Fatal(err)
	}
}

// limitProvider makes the node's live session report that the provider behind
// harness/model has reached its limit, which is a locked check.
func limitProvider(t *testing.T, g *Gateway, node, harness, model string) {
	t.Helper()
	session, err := g.store.UpsertSession(node, protocol.SessionInfo{Harness: harness, NativeID: "limited-" + harness, Model: model})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.store.db.Exec(`UPDATE sessions SET limited_until=?,limited_reason='usage limit' WHERE id=?`,
		time.Now().Add(time.Hour).Unix(), session); err != nil {
		t.Fatal(err)
	}
}

// A role start asks the one gate, and a provider a live session reported
// limited is skipped with its reason (decision 0162).
func TestRoleStartSkipsAProviderALiveSessionReportedLimited(t *testing.T) {
	g, node := roleGateway(t)
	setRoleList(t, g, protocol.RoleModelsKey(protocol.RoleChief),
		protocol.RoleModel{Harness: "claude", Provider: "anthropic", Model: "claude-opus-5-5"},
		protocol.RoleModel{Harness: "codex", Provider: "openai", Model: "gpt-6.1-sol"})
	limitProvider(t, g, node, "claude", "claude-opus-5-5")

	got, err := g.pickRole(node, protocol.RolePickRequest{Role: protocol.RoleChief})
	if err != nil {
		t.Fatal(err)
	}
	if got.Harness != "codex" || got.Model != "gpt-6.1-sol" || got.Provider != "openai" {
		t.Fatalf("picked %+v, want the codex entry", got)
	}
	if !strings.Contains(got.Note, "limited until") || strings.Contains(got.Note, "\n") {
		t.Fatalf("note %q does not name the locked limit reason", got.Note)
	}
}

// A role resumes on the model it already runs and does not move to its list.
func TestRoleResumeKeepsTheCurrentModel(t *testing.T) {
	g, node := roleGateway(t)
	current := protocol.RoleModel{Harness: "claude", Provider: "anthropic", Model: "claude-opus-5-5"}
	setRoleList(t, g, protocol.RoleModelsKey(protocol.RoleCTO),
		current,
		protocol.RoleModel{Harness: "codex", Provider: "openai", Model: "gpt-6.1-sol"})

	got, err := g.pickRole(node, protocol.RolePickRequest{Role: protocol.RoleCTO, Resume: true, Current: current})
	if err != nil {
		t.Fatal(err)
	}
	if got.RoleModelOf() != current || got.NewConversation {
		t.Fatalf("resume moved off the current model: %+v", got)
	}
}

// A role resumes on its current model. When a locked check refuses that model,
// it falls to the next entry of its list with a new conversation; the role
// does not fail to start because of quota.
func TestRoleResumeFallsToTheListOnALockedCheck(t *testing.T) {
	g, node := roleGateway(t)
	current := protocol.RoleModel{Harness: "claude", Provider: "anthropic", Model: "claude-opus-5-5"}
	next := protocol.RoleModel{Harness: "codex", Provider: "openai", Model: "gpt-6.1-sol"}
	setRoleList(t, g, protocol.RoleModelsKey(protocol.RoleMerger), current, next)
	limitProvider(t, g, node, "claude", "claude-opus-5-5")

	got, err := g.pickRole(node, protocol.RolePickRequest{Role: protocol.RoleMerger, Resume: true, Current: current})
	if err != nil {
		t.Fatal(err)
	}
	if got.RoleModelOf() != next || !got.NewConversation {
		t.Fatalf("locked check did not fall to the next entry with a new conversation: %+v", got)
	}
}

// When no entry can run, the role start is refused with one line: the TUI
// shows a reason, never a silent failure.
func TestRoleRefusalIsOneLine(t *testing.T) {
	g, node := roleGateway(t)
	current := protocol.RoleModel{Harness: "claude", Provider: "anthropic", Model: "claude-opus-5-5"}
	setRoleList(t, g, protocol.RoleModelsKey(protocol.RoleChief), current)
	limitProvider(t, g, node, "claude", "claude-opus-5-5")

	got, err := g.pickRole(node, protocol.RolePickRequest{Role: protocol.RoleChief})
	if err != nil {
		t.Fatal(err)
	}
	if got.Refused == "" || strings.Contains(got.Refused, "\n") || !strings.Contains(got.Refused, "limited until") {
		t.Fatalf("refusal %q is not one reason line", got.Refused)
	}
}

// The merger's list is a setting, and its default is a one-entry list instead
// of a model hard-coded in Go.
func TestMergerRoleListComesFromItsSetting(t *testing.T) {
	g, node := roleGateway(t)
	defaults, err := g.roleModels(protocol.RolePickRequest{Role: protocol.RoleMerger})
	if err != nil || len(defaults) != 1 {
		t.Fatalf("merger default list: %+v %v", defaults, err)
	}
	setRoleList(t, g, protocol.RoleModelsKey(protocol.RoleMerger),
		protocol.RoleModel{Harness: "opencode", Provider: "deepseek", Model: "deepseek/deepseek-v4.1-flash"})
	got, err := g.pickRole(node, protocol.RolePickRequest{Role: protocol.RoleMerger})
	if err != nil {
		t.Fatal(err)
	}
	if got.Harness != "opencode" || got.Model != "deepseek/deepseek-v4.1-flash" {
		t.Fatalf("merger picked %+v, want its setting", got)
	}
	if _, err := strconv.Atoi(strings.TrimSpace(got.Refused)); err == nil {
		t.Fatalf("unexpected refusal %q", got.Refused)
	}
}
