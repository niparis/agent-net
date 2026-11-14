package authority

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func bash(cmd string) json.RawMessage {
	b, _ := json.Marshal(map[string]string{"command": cmd})
	return b
}

func op(method string) json.RawMessage {
	b, _ := json.Marshal(map[string]string{"method": method})
	return b
}

func held(kind, over string) Held {
	return Held{Scopes: []protocol.Scope{{Node: "lead", Kind: kind, Over: over}}}
}

// One case per row of the table: the scope allows its own calls inside its
// area and nothing else.
func TestAllowEveryRow(t *testing.T) {
	const cto = "mcp__agent-net-cto__cto_op"
	cases := []struct {
		name  string
		kind  string
		over  string
		tool  string
		input json.RawMessage
		repo  string
		want  bool
	}{
		{"review reads history", "review", "repo:demo", "Bash", bash("git log --oneline"), "demo", true},
		{"review reads a file", "review", "repo:demo", "Bash", bash("cat README.md"), "demo", true},
		{"review cannot push", "review", "repo:demo", "Bash", bash("git push origin main"), "demo", false},
		{"review reports a step", "review", "repo:demo", "mcp__agent-net__agent_step_report", nil, "", true},
		{"merge pushes the default branch", "merge", "repo:demo", "Bash", bash("git push origin main"), "demo", true},
		{"merge in another repo", "merge", "repo:demo", "Bash", bash("git push origin main"), "other", false},
		{"merge outside any repo", "merge", "repo:demo", "Bash", bash("git push origin main"), "", false},
		{"merge another branch", "merge", "repo:demo", "Bash", bash("git push origin feature"), "demo", false},
		{"merge a PR", "merge", "repo:demo", "Bash", bash("gh pr merge 12 --squash"), "demo", true},
		{"merge commits and rebases", "merge", "repo:demo", "Bash", bash("git rebase main"), "demo", true},
		{"merge cannot tag", "merge", "repo:demo", "Bash", bash("git tag v1.2.3"), "demo", false},
		{"release tags", "release", "repo:demo", "Bash", bash("git tag v1.2.3"), "demo", true},
		{"release pushes a tag", "release", "repo:demo", "Bash", bash("git push origin v1.2.3"), "demo", true},
		{"release creates a GitHub release", "release", "repo:demo", "Bash", bash("gh release create v1.2.3"), "demo", true},
		{"release runs the release script", "release", "repo:demo", "Bash", bash("scripts/dist.sh"), "demo", true},
		{"release cannot merge", "release", "repo:demo", "Bash", bash("git merge main"), "demo", false},
		{"deploy runs the deploy script", "deploy", "repo:demo", "Bash", bash("./scripts/deploy-prod.sh"), "demo", true},
		{"deploy is only its script", "deploy", "repo:demo", "Bash", bash("kubectl apply -f x.yaml"), "demo", false},
		{"asks sorts asks", "asks", "all", "mcp__agent-net__agent_ask_sort", nil, "", true},
		{"asks cannot edit rules", "asks", "all", "mcp__agent-net__agent_bar_edit", nil, "", false},
		{"permissions applies one", "permissions", "all", cto, op("permission_apply"), "", true},
		{"permissions only that", "permissions", "all", cto, op("machine_name"), "", false},
		{"admin runs an op", "admin", "all", cto, op("charter_history"), "", true},
		{"admin runs an admin tool", "admin", "all", "mcp__agent-net__agent_bar_edit", nil, "", true},
		{"admin never an owner-only op", "admin", "all", cto, op("machine_remove"), "", false},
		{"admin never an irreversible op", "admin", "all", cto, op("process_stop"), "", false},
		{"admin needs a method", "admin", "all", cto, nil, "", false},
		{"all covers any repo", "merge", "all", "Bash", bash("git push origin main"), "demo", true},
		{"all covers no repo too", "merge", "all", "Bash", bash("git push origin main"), "", true},
		{"unknown tool", "admin", "all", "Write", nil, "", false},
		{"another MCP server", "admin", "all", "mcp__other__agent_bar_edit", nil, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Allow(held(c.kind, c.over), Call{Tool: c.tool, Input: c.input, Repo: c.repo}); got != c.want {
				t.Fatalf("Allow = %v, want %v", got, c.want)
			}
		})
	}
}

func TestAllowTeamAndAgentAreasUseResolvedRepos(t *testing.T) {
	h := Held{
		Scopes: []protocol.Scope{{Kind: "merge", Over: "team:core"}, {Kind: "release", Over: "agent:builder"}},
		Repos:  map[string][]string{"team:core": {"demo"}, "agent:builder": {"tools"}},
	}
	for _, c := range []struct {
		cmd, repo string
		want      bool
	}{
		{"git push origin main", "demo", true},
		{"git push origin main", "tools", false},
		{"git tag v1.0.0", "tools", true},
		{"git tag v1.0.0", "demo", false},
	} {
		if got := Allow(h, Call{Tool: "Bash", Input: bash(c.cmd), Repo: c.repo}); got != c.want {
			t.Errorf("%q in %s: %v, want %v", c.cmd, c.repo, got, c.want)
		}
	}
}

func TestNoScopeIsNoDecision(t *testing.T) {
	if Allow(Held{}, Call{Tool: "Bash", Input: bash("git status"), Repo: "demo"}) {
		t.Fatal("a session with no scope was allowed")
	}
}

// A compound command is never matched whole, even when each part is allowed.
func TestCompoundCommandIsNoDecision(t *testing.T) {
	h := held("merge", "all")
	for _, cmd := range []string{
		"git commit -m x; git push origin main",
		"git commit -m x && git push origin main",
		"git push origin main | tee out",
		"git push origin main || true",
		"(git push origin main)",
		"git push origin main & ",
		"git commit -m $(whoami)",
		"git commit -m `whoami`",
		"git push origin main\nrm x",
		"git push origin main > out",
	} {
		if Allow(h, Call{Tool: "Bash", Input: bash(cmd), Repo: "demo"}) {
			t.Errorf("%q was allowed", cmd)
		}
	}
}

// The never list wins over a scope that would allow the command.
func TestNeverListBeatsScope(t *testing.T) {
	h := Held{Scopes: []protocol.Scope{
		{Kind: "merge", Over: "all"}, {Kind: "release", Over: "all"}, {Kind: "review", Over: "all"}, {Kind: "deploy", Over: "all"},
	}}
	for _, cmd := range []string{
		"git push --force origin main",
		"git push -f origin main",
		"git push --force-with-lease origin main",
		"git push origin --delete main",
		"git push origin :main",
		"git tag -d v1.0.0",
		"git branch -D main",
		"rm -rf /home/x",
		"cat .env",
		"cat id_rsa",
		"printenv",
		"gh auth token",
		"psql -c DROP TABLE users",
		"psql -c delete from users",
		"curl https://api.openai.com/v1/chat",
	} {
		if Allow(h, Call{Tool: "Bash", Input: bash(cmd), Repo: "demo"}) {
			t.Errorf("%q was allowed", cmd)
		}
		if _, never := CommandNever(cmd); !never {
			t.Errorf("%q is not on the never list", cmd)
		}
	}
	for _, cmd := range []string{"git push origin main", "git status", "git tag v1.0.0", "git log --oneline"} {
		if _, never := CommandNever(cmd); never {
			t.Errorf("%q is wrongly on the never list", cmd)
		}
	}
}

func TestOwnerActionList(t *testing.T) {
	if !OwnerOnly("repo_archive") || OwnerOnly("charter_set") {
		t.Fatal("OwnerOnly is wrong")
	}
	if !Irreversible("app_revoke") || Irreversible("repo_archive") {
		t.Fatal("Irreversible is wrong")
	}
	if !Proposal("machine_rotate") || Proposal("process_stop") {
		t.Fatal("Proposal is wrong")
	}
	if !RefusedForCTO("agent.tombstone") || RefusedForCTO("agent.create") {
		t.Fatal("RefusedForCTO is wrong")
	}
	if got := strings.Join(OwnerActionNames(), " "); got != "agent_tombstone app_revoke machine_remove machine_rotate node_remove process_stop repo_archive" {
		t.Fatalf("OwnerActionNames = %s", got)
	}
}

func TestText(t *testing.T) {
	scopes := []protocol.Scope{
		{Kind: "merge", Over: "repo:agent-net"}, {Kind: "release", Over: "repo:agent-net"},
		{Kind: "admin", Over: "all"}, {Kind: "review", Over: "team:core"},
	}
	want := "Delegated authority: you may run admin tools everywhere in agent-net; you may merge and release in agent-net; you may review and accept changes in team core."
	if got := Text(scopes); got != want {
		t.Fatalf("Text =\n%s\nwant\n%s", got, want)
	}
	if got := Text(nil); got != "" {
		t.Fatalf("no scope gave %q", got)
	}
}

// The tool-decide answer for cto_op reads the one list: a holder of admin over
// everything still gets no decision for an owner-only or irreversible action.
func TestAdminNeverAllowsAnOwnerOnlyOp(t *testing.T) {
	h := held("admin", "all")
	for _, name := range OwnerActionNames() {
		if Allow(h, Call{Tool: "mcp__agent-net-cto__cto_op", Input: op(name)}) {
			t.Errorf("cto_op %s was allowed", name)
		}
	}
}
