package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/pelletier/go-toml/v2"
)

func coverageDaemon(t *testing.T, source string) (*Daemon, string) {
	t.Helper()
	root := t.TempDir()
	project := filepath.Join(root, "homelab")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".mcp.json"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{cfg: Config{InventoryHome: root, StateDir: t.TempDir(), Hostname: "research", NodeID: "n_r"}}
	return d, project
}

const coverageSource = `{"mcpServers":{"homelab-mcp":{"type":"stdio","command":"homelab-mcp","args":["--stdio"],"env":{"HOMELAB_TOKEN":"${HOMELAB_TOKEN}"}}}}`

func coverageRequest(project string, harnesses ...string) protocol.MCPCoverageRequest {
	return protocol.MCPCoverageRequest{Project: "homelab", Checkout: project, Server: "homelab-mcp",
		SourceHarness: "claude", SourcePath: filepath.Join(project, ".mcp.json"), Harnesses: harnesses}
}

func TestMCPCoveragePlanApplyWritesEachHarnessFormatAndUndoRestores(t *testing.T) {
	t.Parallel()
	d, project := coverageDaemon(t, coverageSource)
	plan := d.mcpCoveragePlan(coverageRequest(project, "pi", "opencode"))
	if plan.State != "pending" {
		t.Fatalf("plan = %+v", plan)
	}
	if len(plan.Targets) != 2 {
		t.Fatalf("targets = %+v", plan.Targets)
	}
	for _, target := range plan.Targets {
		if target.State != "pending" || target.Path == "" || target.PostHash == "" {
			t.Fatalf("target = %+v", target)
		}
		found := false
		for _, line := range target.Preview {
			if strings.Contains(line, target.Path) {
				found = true
			}
		}
		if !found {
			t.Fatalf("preview does not name the file: %+v", target.Preview)
		}
	}

	res := d.mcpCoverageApply(plan.ID)
	if res.State != "applied" {
		t.Fatalf("apply = %+v", res)
	}
	piPath := filepath.Join(project, ".pi/mcp.json")
	piData, err := os.ReadFile(piPath)
	if err != nil {
		t.Fatal(err)
	}
	var pi map[string]any
	if err := json.Unmarshal(piData, &pi); err != nil {
		t.Fatal(err)
	}
	servers, _ := pi["mcpServers"].(map[string]any)
	entry, _ := servers["homelab-mcp"].(map[string]any)
	if entry["command"] != "homelab-mcp" {
		t.Fatalf("pi entry = %#v", entry)
	}
	if env, _ := entry["env"].(map[string]any); env["HOMELAB_TOKEN"] != "${HOMELAB_TOKEN}" {
		t.Fatalf("pi env reference lost: %#v", entry)
	}

	ocPath := filepath.Join(project, "opencode.json")
	ocData, err := os.ReadFile(ocPath)
	if err != nil {
		t.Fatal(err)
	}
	var oc map[string]any
	if err := json.Unmarshal(ocData, &oc); err != nil {
		t.Fatal(err)
	}
	mcp, _ := oc["mcp"].(map[string]any)
	ocEntry, _ := mcp["homelab-mcp"].(map[string]any)
	if ocEntry["type"] != "local" {
		t.Fatalf("opencode entry = %#v", ocEntry)
	}
	if env, _ := ocEntry["environment"].(map[string]any); env["HOMELAB_TOKEN"] != "${HOMELAB_TOKEN}" {
		t.Fatalf("opencode env reference lost: %#v", ocEntry)
	}
	if strings.Contains(string(ocData), "supersecret") || strings.Contains(string(piData), "supersecret") {
		t.Fatal("a literal environment value was copied")
	}

	if res := d.mcpCoverageUndo(plan.ID); res.State != "applied" {
		t.Fatalf("undo = %+v", res)
	}
	for _, path := range []string{piPath, ocPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("undo left %s: %v", path, err)
		}
	}
}

func TestMCPCoverageApplyUpdatesExistingFileAndUndoRestoresExactBytes(t *testing.T) {
	d, project := coverageDaemon(t, coverageSource)
	ocPath := filepath.Join(project, "opencode.json")
	original := []byte("{\n  \"$schema\": \"https://opencode.ai/config.json\",\n  \"mcp\": {\n    \"other\": {\"type\": \"local\", \"command\": [\"other\"]}\n  }\n}\n")
	if err := os.WriteFile(ocPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	plan := d.mcpCoveragePlan(coverageRequest(project, "opencode"))
	if plan.State != "pending" {
		t.Fatalf("plan = %+v", plan)
	}
	if res := d.mcpCoverageApply(plan.ID); res.State != "applied" {
		t.Fatalf("apply = %+v", res)
	}
	got, err := os.ReadFile(ocPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "homelab-mcp") || !strings.Contains(string(got), "$schema") {
		t.Fatalf("file after apply:\n%s", got)
	}
	if res := d.mcpCoverageUndo(plan.ID); res.State != "applied" {
		t.Fatalf("undo = %+v", res)
	}
	after, err := os.ReadFile(ocPath)
	if err != nil || string(after) != string(original) {
		t.Fatalf("undo did not restore the file: %q %v", after, err)
	}
}

func TestMCPCoverageApplyChecksEveryFileBeforeWriting(t *testing.T) {
	d, project := coverageDaemon(t, coverageSource)
	plan := d.mcpCoveragePlan(coverageRequest(project, "pi", "opencode"))
	if plan.State != "pending" {
		t.Fatalf("plan = %+v", plan)
	}
	ocPath := filepath.Join(project, "opencode.json")
	if err := os.WriteFile(ocPath, []byte(`{"mcp":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	res := d.mcpCoverageApply(plan.ID)
	if res.State != "conflict" {
		t.Fatalf("apply = %+v", res)
	}
	if _, err := os.Stat(filepath.Join(project, ".pi/mcp.json")); !os.IsNotExist(err) {
		t.Fatalf("a conflicting plan wrote the other file: %v", err)
	}
}

func TestMCPCoverageRefusesSameNameDifferentDefinition(t *testing.T) {
	d, project := coverageDaemon(t, coverageSource)
	if err := os.MkdirAll(filepath.Join(project, ".pi"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".pi/mcp.json"), []byte(`{"mcpServers":{"homelab-mcp":{"command":"other"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	plan := d.mcpCoveragePlan(coverageRequest(project, "pi"))
	if plan.State != "conflict" || len(plan.Conflicts) == 0 {
		t.Fatalf("plan = %+v", plan)
	}
	if _, err := os.Stat(filepath.Join(project, "opencode.json")); !os.IsNotExist(err) {
		t.Fatalf("a conflicting plan wrote a file: %v", err)
	}
}

func TestMCPCoverageOwnerGateRefusesAgentCallers(t *testing.T) {
	d, _ := coverageDaemon(t, coverageSource)
	d.inSession = func(int) bool { return false }
	lc := &localConn{d: d, client: "pi"}
	err := lc.operatorGate("mcp_coverage_plan")
	if err == nil || !strings.Contains(err.Error(), "only available to the owner") {
		t.Fatalf("agent caller was not refused: %v", err)
	}
	if err := (&localConn{d: d, client: "cli"}).operatorGate("mcp_coverage_plan"); err != nil {
		t.Fatalf("the owner was refused: %v", err)
	}
}

func TestMCPCoverageWritesCodexProjectConfig(t *testing.T) {
	d, project := coverageDaemon(t, coverageSource)
	codexDir := filepath.Join(project, ".codex")
	if err := os.MkdirAll(codexDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(codexDir, "config.toml")
	original := []byte("[mcp_servers.other]\ncommand = \"other\"\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	plan := d.mcpCoveragePlan(coverageRequest(project, "codex"))
	if plan.State != "pending" {
		t.Fatalf("plan = %+v", plan)
	}
	if res := d.mcpCoverageApply(plan.ID); res.State != "applied" {
		t.Fatalf("apply = %+v", res)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := toml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("invalid TOML: %v\n%s", err, data)
	}
	servers, _ := doc["mcp_servers"].(map[string]any)
	if servers["other"] == nil || servers["homelab-mcp"] == nil {
		t.Fatalf("servers after apply: %#v\n%s", servers, data)
	}
	entry, _ := servers["homelab-mcp"].(map[string]any)
	if env, _ := entry["env"].(map[string]any); env["HOMELAB_TOKEN"] != "${HOMELAB_TOKEN}" {
		t.Fatalf("codex env reference lost: %#v", entry)
	}
	if res := d.mcpCoverageUndo(plan.ID); res.State != "applied" {
		t.Fatalf("undo = %+v", res)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(original) {
		t.Fatalf("undo did not restore the TOML file: %q %v", after, err)
	}
}
