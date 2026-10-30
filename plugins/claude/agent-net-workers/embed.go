// Package agentnetworkers embeds the Claude Code "agent-net workers" plugin so
// `agent-net setup` can install it from a local marketplace.
package agentnetworkers

import _ "embed"

// PluginJSON is the content of .claude-plugin/plugin.json.
//
//go:embed all:.claude-plugin/plugin.json
var PluginJSON []byte

// HooksJSON is the content of hooks/hooks.json.
//
//go:embed hooks/hooks.json
var HooksJSON []byte

// Register is the content of hooks/register.tsx.
//
//go:embed hooks/register.tsx
var Register []byte

// Types is the content of types/index.d.ts.
//
//go:embed types/index.d.ts
var Types []byte

// Files returns the plugin files keyed by their path inside the plugin folder.
// register.test.tsx is not a plugin file and is not embedded.
func Files() map[string][]byte {
	return map[string][]byte{
		".claude-plugin/plugin.json": PluginJSON,
		"hooks/hooks.json":           HooksJSON,
		"hooks/register.tsx":         Register,
		"types/index.d.ts":           Types,
	}
}
