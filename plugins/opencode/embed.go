// Package opencode embeds the OpenCode plugin so `agent-net setup` can install it.
package opencode

import _ "embed"

// Plugin is the content of agent-net.ts.
//
//go:embed agent-net.ts
var Plugin []byte

// Tools is the generated native tool catalog.
//
//go:embed tools.gen.ts
var Tools []byte
