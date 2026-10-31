package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func (f *importFixture) nativePlan(t *testing.T, c protocol.MCPImportCandidate) protocol.MCPNativeRemovePlan {
	t.Helper()
	var p protocol.MCPNativeRemovePlan
	if err := json.Unmarshal(f.cli.mustCall("mcp_native_remove", protocol.MCPNativeRemoveRequest{Candidate: c.Key}), &p); err != nil || p.ID == "" {
		t.Fatalf("native plan: %+v %v", p, err)
	}
	return p
}

func (f *importFixture) nativeCall(t *testing.T, req protocol.MCPNativeRemoveRequest) protocol.MCPNativeRemoveResult {
	t.Helper()
	var r protocol.MCPNativeRemoveResult
	if err := json.Unmarshal(f.cli.mustCall("mcp_native_remove", req), &r); err != nil {
		t.Fatal(err)
	}
	return r
}

// importProbe imports the "probe" entry of the fixture and returns its candidate.
func (f *importFixture) importProbe(t *testing.T) protocol.MCPImportCandidate {
	t.Helper()
	cand := f.candidate(t, "claude", "probe")
	if res := f.apply(t, f.plan(t, cand).ID); res.State != protocol.MCPImportOK {
		t.Fatalf("import: %+v", res)
	}
	return f.candidate(t, "claude", "probe")
}

func TestMCPNativeRemoveEndToEnd(t *testing.T) {
	f := newImportFixture(t, map[string]string{"probe": stdioNative(), "other": stdioNative()}, "")
	file := filepath.Join(f.home, ".claude.json")
	original, _ := os.ReadFile(file)
	cand := f.importProbe(t)
	if len(cand.Gaps) == 0 {
		t.Fatalf("an imported candidate says nothing about what federation does not provide: %+v", cand)
	}
	joined := strings.Join(cand.Gaps, " ")
	for _, want := range []string{"resources", "prompts", "OAuth", "per-client"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("gaps lack %q: %v", want, cand.Gaps)
		}
	}
	plan := f.nativePlan(t, cand)
	if len(plan.Conflicts) != 0 || plan.FileHash == "" || plan.Server != "probe" || len(plan.Gaps) == 0 {
		t.Fatalf("plan: %+v", plan)
	}
	if after, _ := os.ReadFile(file); string(after) != string(original) {
		t.Fatal("planning changed the file")
	}
	res := f.nativeCall(t, protocol.MCPNativeRemoveRequest{PlanID: plan.ID})
	if res.State != protocol.MCPNativeRemoved {
		t.Fatalf("apply: %+v", res)
	}
	var doc struct {
		Servers map[string]json.RawMessage `json:"mcpServers"`
	}
	after, _ := os.ReadFile(file)
	if err := json.Unmarshal(after, &doc); err != nil || len(doc.Servers) != 1 || doc.Servers["other"] == nil {
		t.Fatalf("file after removal: %s %v", after, err)
	}
	// The federated server keeps working, and the native entry is gone from the list.
	if out, err := callTool(t, f.cli, "probe", "echo", map[string]string{"text": "hi"}); err != nil || out.Text != "echo: hi" {
		t.Fatalf("call: %+v %v", out, err)
	}
	f.cli.mustCall("inventory_scan", nil)
	eventually(t, "the removed entry leaves the candidate list", func() bool {
		for _, c := range f.candidates(t).Candidates {
			if c.Name == "probe" {
				return false
			}
		}
		return true
	})
	// A plan applies once.
	if _, err := f.cli.call("mcp_native_remove", protocol.MCPNativeRemoveRequest{PlanID: plan.ID}); err == nil {
		t.Fatal("a plan applied twice")
	}
	var page protocol.ToolsPage
	if err := json.Unmarshal(f.cli.mustCall("tools_items", map[string]any{"kind": "mcp"}), &page); err != nil {
		t.Fatal(err)
	}
	for _, it := range page.Items {
		if it.Federated && it.Name == "probe" && (it.Imported == nil || len(it.Imported.Gaps) == 0 || it.NativeStillPresent) {
			t.Fatalf("item details: %+v", it)
		}
	}
	// The audit records the plan and the removal, without file content.
	nativeItem, _ := json.Marshal([]string{"mcp", "probe", plan.NativeHash})
	hist := string(f.cli.mustCall("tools_history", protocol.ToolHistoryRequest{Item: string(nativeItem)}))
	if !strings.Contains(hist, `"action":"mcp_native_plan"`) || !strings.Contains(hist, `"action":"mcp_native_remove"`) || strings.Contains(hist, os.Args[0]) {
		t.Fatalf("history: %s", hist)
	}

	// Undo puts the original bytes back.
	undo := f.nativeCall(t, protocol.MCPNativeRemoveRequest{UndoPlanID: plan.ID})
	if undo.State != protocol.MCPNativeRestored || len(undo.Notes) == 0 {
		t.Fatalf("undo: %+v", undo)
	}
	if restored, _ := os.ReadFile(file); string(restored) != string(original) {
		t.Fatalf("not restored:\n%s", restored)
	}
	if _, err := f.cli.call("mcp_native_remove", protocol.MCPNativeRemoveRequest{UndoPlanID: plan.ID}); err == nil {
		t.Fatal("a removal was undone twice")
	}
}

func TestMCPNativeRemoveUndoStopsAfterALaterEdit(t *testing.T) {
	f := newImportFixture(t, map[string]string{"probe": stdioNative(), "other": stdioNative()}, "")
	file := filepath.Join(f.home, ".claude.json")
	plan := f.nativePlan(t, f.importProbe(t))
	if res := f.nativeCall(t, protocol.MCPNativeRemoveRequest{PlanID: plan.ID}); res.State != protocol.MCPNativeRemoved {
		t.Fatalf("apply: %+v", res)
	}
	now, _ := os.ReadFile(file)
	later := strings.Replace(string(now), `{"mcpServers":`, `{"theme":"dark","mcpServers":`, 1)
	if err := os.WriteFile(file, []byte(later), 0o600); err != nil {
		t.Fatal(err)
	}
	undo := f.nativeCall(t, protocol.MCPNativeRemoveRequest{UndoPlanID: plan.ID})
	if undo.State != protocol.MCPNativeRefused || undo.Reason != "changed since apply" {
		t.Fatalf("undo: %+v", undo)
	}
	if kept, _ := os.ReadFile(file); string(kept) != later {
		t.Fatal("undo overwrote a later edit")
	}
}

func TestMCPNativeRemoveRefusesAFileChangedSincePlan(t *testing.T) {
	f := newImportFixture(t, map[string]string{"probe": stdioNative(), "other": stdioNative()}, "")
	file := filepath.Join(f.home, ".claude.json")
	plan := f.nativePlan(t, f.importProbe(t))
	b, _ := os.ReadFile(file)
	edited := strings.Replace(string(b), `{"mcpServers":`, `{"theme":"dark","mcpServers":`, 1)
	if err := os.WriteFile(file, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := f.cli.call("mcp_native_remove", protocol.MCPNativeRemoveRequest{PlanID: plan.ID})
	var r protocol.MCPNativeRemoveResult
	if err == nil {
		_ = json.Unmarshal(res, &r)
	}
	t.Logf("apply after a user edit: %+v %v", r, err)
	if r.State == protocol.MCPNativeRemoved || (err == nil && r.Reason != "changed since plan") {
		t.Fatalf("a changed file was edited or refused for the wrong reason: %+v", r)
	}
	if kept, _ := os.ReadFile(file); string(kept) != edited {
		t.Fatal("the user's edit was lost")
	}
}

func TestMCPNativeRemoveNeedsAnImportedAndTestedServer(t *testing.T) {
	f := newImportFixture(t, map[string]string{"probe": stdioNative(), "other": stdioNative()}, "")
	file := filepath.Join(f.home, ".claude.json")
	original, _ := os.ReadFile(file)
	// Not imported at all.
	plan := f.nativePlan(t, f.candidate(t, "claude", "other"))
	if len(plan.Conflicts) == 0 || !strings.Contains(plan.Conflicts[0], "not imported") {
		t.Fatalf("plan: %+v", plan)
	}
	if _, err := f.cli.call("mcp_native_remove", protocol.MCPNativeRemoveRequest{PlanID: plan.ID}); err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("apply: %v", err)
	}
	// A federated server of the same name that was added by hand is not an import.
	f.cli.mustCall("mcp_add", protocol.MCPAdd{Server: stdioSpec("other")})
	plan = f.nativePlan(t, f.candidate(t, "claude", "other"))
	if len(plan.Conflicts) == 0 {
		t.Fatalf("a hand-added server counted as an import: %+v", plan)
	}
	// A failed import leaves nothing to remove it against.
	if after, _ := os.ReadFile(file); string(after) != string(original) {
		t.Fatal("the file changed")
	}
}

func TestMCPNativeRemoveRefusesACommittedProjectFile(t *testing.T) {
	f := newImportFixture(t, map[string]string{"probe": stdioNative()}, "")
	cand := f.candidate(t, "claude", "projsrv")
	if res := f.apply(t, f.plan(t, cand).ID); res.State != protocol.MCPImportOK {
		t.Fatalf("import: %+v", res)
	}
	gitIn(t, f.repo, "add", ".mcp.json")
	gitIn(t, f.repo, "commit", "-q", "-m", "mcp")
	before, _ := os.ReadFile(filepath.Join(f.repo, ".mcp.json"))
	plan := f.nativePlan(t, f.candidate(t, "claude", "projsrv"))
	if len(plan.Conflicts) != 1 || plan.Conflicts[0] != "committed in the repo: remove it with a commit" {
		t.Fatalf("plan: %+v", plan)
	}
	if _, err := f.cli.call("mcp_native_remove", protocol.MCPNativeRemoveRequest{PlanID: plan.ID}); err == nil {
		t.Fatal("a committed file was edited")
	}
	if after, _ := os.ReadFile(filepath.Join(f.repo, ".mcp.json")); string(after) != string(before) {
		t.Fatal("the committed file changed")
	}
}

func TestMCPNativeRemoveOwnerGate(t *testing.T) {
	f := newImportFixture(t, map[string]string{"probe": stdioNative(), "other": stdioNative()}, "")
	cand := f.importProbe(t)
	agent, _ := helloPi(t, f.n, "agent-native", "/work", true)
	if _, err := agent.call("mcp_native_remove", protocol.MCPNativeRemoveRequest{Candidate: cand.Key}); err == nil {
		t.Fatal("an agent planned a native removal")
	}
	plan := f.nativePlan(t, cand)
	if _, err := agent.call("mcp_native_remove", protocol.MCPNativeRemoveRequest{PlanID: plan.ID}); err == nil {
		t.Fatal("an agent applied a native removal")
	}
	if _, err := agent.call("mcp_native_remove", protocol.MCPNativeRemoveRequest{UndoPlanID: plan.ID}); err == nil {
		t.Fatal("an agent undid a native removal")
	}
	b, _ := os.ReadFile(filepath.Join(f.home, ".claude.json"))
	if !strings.Contains(string(b), `"probe"`) {
		t.Fatal("a refused request edited the file")
	}
	// The CTO tool goes through the same owner path and is recorded as the CTO.
	v, err := f.n.d.runCTOToolRaw(context.Background(), "mcp_native_remove", importJSON(t, protocol.MCPNativeRemoveRequest{PlanID: plan.ID}))
	if err != nil {
		t.Fatal(err)
	}
	if out, _ := json.Marshal(v); !strings.Contains(string(out), `"state":"removed"`) {
		t.Fatalf("cto apply: %s", out)
	}
}

// A federated server that is switched off or unhealthy is a plan conflict on
// its own: the native entry is its only working copy.
func TestMCPNativeRemoveRefusesAServerThatIsOffOrUnhealthy(t *testing.T) {
	f := newImportFixture(t, map[string]string{"probe": stdioNative()}, "")
	file := filepath.Join(f.home, ".claude.json")
	original, _ := os.ReadFile(file)
	cand := f.importProbe(t)
	conflict := func(want string) {
		t.Helper()
		plan := f.nativePlan(t, cand)
		if len(plan.Conflicts) == 0 || !strings.Contains(strings.Join(plan.Conflicts, ";"), want) {
			t.Fatalf("want a conflict with %q: %+v", want, plan.Conflicts)
		}
		if _, err := f.cli.call("mcp_native_remove", protocol.MCPNativeRemoveRequest{PlanID: plan.ID}); err == nil {
			t.Fatalf("a plan with the conflict %q was applied", want)
		}
	}
	// Switched off.
	f.cli.mustCall("mcp_enable", protocol.MCPSetEnabled{Name: "probe", Enabled: false})
	conflict("switched off")
	f.cli.mustCall("mcp_enable", protocol.MCPSetEnabled{Name: "probe", Enabled: true})
	// Not healthy: the server cannot start.
	t.Setenv(helperEnv, "")
	f.cli.mustCall("mcp_test", protocol.MCPName{Name: "probe"})
	conflict("not healthy")
	if after, _ := os.ReadFile(file); string(after) != string(original) {
		t.Fatal("the file changed")
	}
}

// The federated server is tested again when the owner applies: a server that
// was healthy at plan time but fails now keeps the native entry.
func TestMCPNativeRemoveTestsTheServerAgainAtApply(t *testing.T) {
	f := newImportFixture(t, map[string]string{"probe": stdioNative()}, "")
	file := filepath.Join(f.home, ".claude.json")
	original, _ := os.ReadFile(file)
	plan := f.nativePlan(t, f.importProbe(t))
	if len(plan.Conflicts) != 0 {
		t.Fatalf("plan: %+v", plan)
	}
	// The recorded health is still good; only the live test fails.
	t.Setenv(helperEnv, "")
	_, err := f.cli.call("mcp_native_remove", protocol.MCPNativeRemoveRequest{PlanID: plan.ID})
	if err == nil || !strings.Contains(err.Error(), "fails its test") {
		t.Fatalf("apply: %v", err)
	}
	if after, _ := os.ReadFile(file); string(after) != string(original) {
		t.Fatal("the native entry was removed although the federated server fails")
	}
}

// Any change to the scanned state makes a removal plan stale, and a plan that
// was applied says so when it is applied again.
func TestMCPNativeRemovePlanGoesStaleAndAppliesOnce(t *testing.T) {
	f := newImportFixture(t, map[string]string{"probe": stdioNative(), "other": stdioNative()}, "")
	file := filepath.Join(f.home, ".claude.json")
	plan := f.nativePlan(t, f.importProbe(t))
	// An unrelated entry appears after the plan.
	b, _ := os.ReadFile(file)
	changed := strings.Replace(string(b), `"other":`, `"another":`+stdioNative()+`,"other":`, 1)
	if err := os.WriteFile(file, []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}
	f.cli.mustCall("inventory_scan", nil)
	eventually(t, "new entry reaches the gateway", func() bool {
		for _, c := range f.candidates(t).Candidates {
			if c.Name == "another" {
				return true
			}
		}
		return false
	})
	if _, err := f.cli.call("mcp_native_remove", protocol.MCPNativeRemoveRequest{PlanID: plan.ID}); err == nil || !strings.Contains(err.Error(), "out of date") {
		t.Fatalf("stale plan: %v", err)
	}
	if kept, _ := os.ReadFile(file); string(kept) != changed {
		t.Fatal("a stale plan edited the file")
	}
	// A fresh plan applies, and only once.
	fresh := f.nativePlan(t, f.candidate(t, "claude", "probe"))
	if res := f.nativeCall(t, protocol.MCPNativeRemoveRequest{PlanID: fresh.ID}); res.State != protocol.MCPNativeRemoved {
		t.Fatalf("apply: %+v", res)
	}
	if _, err := f.cli.call("mcp_native_remove", protocol.MCPNativeRemoveRequest{PlanID: fresh.ID}); err == nil || !strings.Contains(err.Error(), "already applied") {
		t.Fatalf("second apply: %v", err)
	}
}
