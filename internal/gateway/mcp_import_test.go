package gateway

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func nativeEntry(harness, scope, project, name, location string, info *protocol.MCPImportInfo) protocol.InventoryItem {
	return protocol.InventoryItem{Harness: harness, Kind: protocol.InventoryMCP, Scope: scope, Project: project, Location: location,
		Name: name, Source: protocol.InventoryNative, Hash: "h-" + name, ScannedAt: time.Now(), State: protocol.InventoryOK, Import: info}
}

func setNative(t *testing.T, g *Gateway, node string, items ...protocol.InventoryItem) {
	t.Helper()
	if err := g.store.SetInventory(node, protocol.InventoryReport{Items: items, State: protocol.InventoryOK, ScannedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
}

func candidatePage(t *testing.T, g *Gateway, f protocol.MCPImportCandidatesRequest) protocol.MCPImportCandidatesPage {
	t.Helper()
	p, err := g.mcpImportCandidates(f)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestImportCandidatesListVerdictsWithoutValues(t *testing.T) {
	g, node := scopeGateway(t)
	ok := &protocol.MCPImportInfo{Class: protocol.MCPImportable, Transport: "stdio", EnvNames: []string{"API_TOKEN"}}
	no := &protocol.MCPImportInfo{Class: protocol.MCPUnsupported, Reason: "signs in with OAuth; the federation cannot sign in for you"}
	opaque := nativeEntry("pi", protocol.InventoryUser, "", "extensions", "/home/u/.pi/agent/extensions", nil)
	opaque.Source = protocol.InventoryPlugin
	opaque.Unsupported = []string{"extension MCP requires manual inspection"}
	setNative(t, g, node,
		nativeEntry("claude", protocol.InventoryUser, "", "Fetch_Tool", "/home/u/.claude.json", ok),
		nativeEntry("claude", protocol.InventoryProject, "alpha", "oauthy", "/repo/alpha/.mcp.json", no),
		nativeEntry("codex", protocol.InventoryUser, "", "old", "/home/u/.codex/config.toml", nil),
		opaque,
		// A source error has no name and is not a candidate.
		protocol.InventoryItem{Harness: "claude", Kind: protocol.InventoryMCP, Scope: protocol.InventoryUser, Location: "/x", State: protocol.InventoryError, Reason: "malformed configuration"},
	)
	page := candidatePage(t, g, protocol.MCPImportCandidatesRequest{})
	by := map[string]protocol.MCPImportCandidate{}
	for _, c := range page.Candidates {
		by[c.Harness+":"+c.Name] = c
	}
	if len(page.Candidates) != 4 || page.Total != 4 {
		t.Fatalf("candidates: %+v", page)
	}
	if c := by["claude:Fetch_Tool"]; c.Class != protocol.MCPImportable || c.ServerName != "fetch-tool" || c.Machine != "nas" || strings.Join(c.EnvNames, ",") != "API_TOKEN" || c.Key == "" {
		t.Errorf("importable: %+v", c)
	}
	if c := by["claude:oauthy"]; c.Class != protocol.MCPUnsupported || !strings.Contains(c.Reason, "OAuth") || c.Project != "alpha" {
		t.Errorf("oauth: %+v", c)
	}
	if c := by["codex:old"]; c.Class != protocol.MCPUnsupported || c.Reason == "" {
		t.Errorf("unjudged entry must be unsupported with a reason: %+v", c)
	}
	if c := by["pi:extensions"]; c.Class != protocol.MCPUnsupported || !strings.Contains(c.Reason, "manual") {
		t.Errorf("extension source: %+v", c)
	}
	if only := candidatePage(t, g, protocol.MCPImportCandidatesRequest{Class: protocol.MCPImportable}); len(only.Candidates) != 1 {
		t.Errorf("class filter: %+v", only)
	}
	if only := candidatePage(t, g, protocol.MCPImportCandidatesRequest{Harness: "codex"}); len(only.Candidates) != 1 {
		t.Errorf("harness filter: %+v", only)
	}
	if only := candidatePage(t, g, protocol.MCPImportCandidatesRequest{Project: "alpha"}); len(only.Candidates) != 1 {
		t.Errorf("project filter: %+v", only)
	}
}

func TestImportCandidatesPageStaysSmall(t *testing.T) {
	g, node := scopeGateway(t)
	var items []protocol.InventoryItem
	for i := 0; i < 100; i++ {
		items = append(items, nativeEntry("claude", protocol.InventoryUser, "", fmt.Sprintf("server-%03d", i), "/home/user/.claude.json",
			&protocol.MCPImportInfo{Class: protocol.MCPUnsupported, Reason: strings.Repeat("r", 100)}))
	}
	setNative(t, g, node, items...)
	page := candidatePage(t, g, protocol.MCPImportCandidatesRequest{})
	raw, _ := json.Marshal(page)
	if len(page.Candidates) != protocol.MCPImportMaxCandidates || page.Next != protocol.MCPImportMaxCandidates || page.Total != 100 || len(raw) > 20<<10 {
		t.Fatalf("page of %d (next %d, total %d) is %d bytes", len(page.Candidates), page.Next, page.Total, len(raw))
	}
	last := candidatePage(t, g, protocol.MCPImportCandidatesRequest{Offset: 90})
	if len(last.Candidates) != 10 || last.Next != 0 {
		t.Fatalf("last page: %d next %d", len(last.Candidates), last.Next)
	}
}

// A native entry and a federated server that share a name are reported on both
// items and never merged into one.
func TestSameNameNativeAndFederatedIsAConflict(t *testing.T) {
	g, node := scopeGateway(t) // registers the federated server "files"
	setNative(t, g, node, nativeEntry("claude", protocol.InventoryUser, "", "files", "/home/u/.claude.json",
		&protocol.MCPImportInfo{Class: protocol.MCPImportable, Transport: "stdio"}))
	items, err := g.store.ToolsItems(protocol.ToolsFilter{Kind: protocol.InventoryMCP}, func(string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	var fed, native *protocol.ToolItem
	for i := range items {
		if items[i].Name == "files" && items[i].Federated {
			fed = &items[i]
		} else if items[i].Name == "files" {
			native = &items[i]
		}
	}
	if fed == nil || native == nil || fed.ID == native.ID {
		t.Fatalf("expected two separate items: %+v", items)
	}
	if len(fed.Conflicts) != 1 || !strings.Contains(fed.Conflicts[0], "nas (claude)") {
		t.Errorf("federated item conflicts: %v", fed.Conflicts)
	}
	if len(native.Conflicts) != 1 || !strings.Contains(native.Conflicts[0], `"files"`) {
		t.Errorf("native item conflicts: %v", native.Conflicts)
	}
	c := candidatePage(t, g, protocol.MCPImportCandidatesRequest{}).Candidates
	if len(c) != 1 || !strings.Contains(c[0].Conflict, "already exists") {
		t.Errorf("candidate conflict: %+v", c)
	}
	// A session's catalog lists the server once.
	srvs, err := g.mcpCatalog("")
	if err != nil {
		t.Fatal(err)
	}
	if len(srvs) != 1 || srvs[0].Name != "files" {
		t.Errorf("catalog: %+v", srvs)
	}
}
