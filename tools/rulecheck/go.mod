// The checker is its own module, so golang.org/x/tools stays out of the
// agent-net module graph and the agent-net binary (B1).
module github.com/niparis/agent-net/tools/rulecheck

go 1.27.3

require golang.org/x/tools v0.52.0

require (
	golang.org/x/mod v0.42.0 // indirect
	golang.org/x/sync v0.24.0 // indirect
)
