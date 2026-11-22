package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/toolcatalog"
)

func TestHarnessToolParity(t *testing.T) {
	// 1. Catalog tools for Agent and Worker
	var catalogAgentNames, catalogWorkerNames []string
	for _, entry := range toolcatalog.ForRole(toolcatalog.RoleAgent) {
		catalogAgentNames = append(catalogAgentNames, entry.Name)
	}
	for _, entry := range toolcatalog.ForRole(toolcatalog.RoleWorker) {
		catalogWorkerNames = append(catalogWorkerNames, entry.Name)
	}
	sort.Strings(catalogAgentNames)
	sort.Strings(catalogWorkerNames)

	// 2. PluginTools (generates Pi and OpenCode)
	pluginTools, err := toolcatalog.PluginTools()
	if err != nil {
		t.Fatalf("PluginTools: %v", err)
	}
	var pluginAgentNames, pluginWorkerNames []string
	for _, pt := range pluginTools {
		for _, role := range pt.Roles {
			if role == toolcatalog.RoleAgent {
				pluginAgentNames = append(pluginAgentNames, pt.Name)
			}
			if role == toolcatalog.RoleWorker {
				pluginWorkerNames = append(pluginWorkerNames, pt.Name)
			}
		}
	}
	sort.Strings(pluginAgentNames)
	sort.Strings(pluginWorkerNames)

	if !reflect.DeepEqual(catalogAgentNames, pluginAgentNames) {
		t.Errorf("plugin agent tools mismatch catalog:\nwant %v\ngot  %v", catalogAgentNames, pluginAgentNames)
	}
	if !reflect.DeepEqual(catalogWorkerNames, pluginWorkerNames) {
		t.Errorf("plugin worker tools mismatch catalog:\nwant %v\ngot  %v", catalogWorkerNames, pluginWorkerNames)
	}

	// 3. MCP server for Agent
	t.Setenv("AGENT_NET_WORKER", "")
	agentSession := connectServer(t, okCaller)
	agentList, err := agentSession.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list agent tools: %v", err)
	}
	var mcpAgentNames []string
	for _, tl := range agentList.Tools {
		mcpAgentNames = append(mcpAgentNames, tl.Name)
	}
	sort.Strings(mcpAgentNames)

	if !reflect.DeepEqual(catalogAgentNames, mcpAgentNames) {
		t.Errorf("MCP agent tools mismatch catalog:\nwant %v\ngot  %v", catalogAgentNames, mcpAgentNames)
	}

	// 4. MCP server for Worker
	workerSession := connectWorker(t)
	workerList, err := workerSession.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list worker tools: %v", err)
	}
	var mcpWorkerNames []string
	for _, tl := range workerList.Tools {
		mcpWorkerNames = append(mcpWorkerNames, tl.Name)
	}
	sort.Strings(mcpWorkerNames)

	if !reflect.DeepEqual(catalogWorkerNames, mcpWorkerNames) {
		t.Errorf("MCP worker tools mismatch catalog:\nwant %v\ngot  %v", catalogWorkerNames, mcpWorkerNames)
	}

	// 5. Verify checked-in plugins/pi/tools.gen.ts and plugins/opencode/tools.gen.ts
	for _, relPath := range []string{"../../plugins/pi/tools.gen.ts", "../../plugins/opencode/tools.gen.ts"} {
		raw, err := os.ReadFile(filepath.Clean(relPath))
		if err != nil {
			t.Fatalf("read %s: %v", relPath, err)
		}
		text := string(raw)
		start := strings.Index(text, "[")
		end := strings.LastIndex(text, "]")
		if start == -1 || end == -1 || end <= start {
			t.Fatalf("cannot parse JSON array in %s", relPath)
		}
		var parsed []struct {
			Name  string   `json:"name"`
			Roles []string `json:"roles"`
		}
		if err := json.Unmarshal([]byte(text[start:end+1]), &parsed); err != nil {
			t.Fatalf("decode %s: %v", relPath, err)
		}
		var genAgentNames, genWorkerNames []string
		for _, item := range parsed {
			for _, r := range item.Roles {
				if r == "agent" {
					genAgentNames = append(genAgentNames, item.Name)
				}
				if r == "worker" {
					genWorkerNames = append(genWorkerNames, item.Name)
				}
			}
		}
		sort.Strings(genAgentNames)
		sort.Strings(genWorkerNames)
		if !reflect.DeepEqual(catalogAgentNames, genAgentNames) {
			t.Errorf("%s agent tools mismatch catalog:\nwant %v\ngot  %v", relPath, catalogAgentNames, genAgentNames)
		}
		if !reflect.DeepEqual(catalogWorkerNames, genWorkerNames) {
			t.Errorf("%s worker tools mismatch catalog:\nwant %v\ngot  %v", relPath, catalogWorkerNames, genWorkerNames)
		}
	}
}
