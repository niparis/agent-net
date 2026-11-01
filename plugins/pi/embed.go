// Package pi embeds the Pi extension so `agent-net setup` can install it.
package pi

import _ "embed"

// Extension is the content of agent-net.ts.
//
//go:embed agent-net.ts
var Extension []byte

// Tools is the generated native tool catalog.
//
//go:embed tools.gen.ts
var Tools []byte
