package mcpserver

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/protocol"
)

type settingsReadOut struct {
	Settings []protocol.SettingView `json:"settings"`
}

func addSettingsReadTool(s *mcp.Server, call Caller) {
	roAdd(s, "agent_settings_read", func(ctx context.Context, _ *mcp.CallToolRequest, in protocol.SettingsReadRequest) (*mcp.CallToolResult, settingsReadOut, error) {
		raw, err := call(ctx, "settings_read", in)
		if err != nil {
			return nil, settingsReadOut{}, err
		}
		var rows []protocol.SettingView
		if err = json.Unmarshal(raw, &rows); err != nil {
			return nil, settingsReadOut{}, err
		}
		return nil, settingsReadOut{Settings: rows}, nil
	})
}
