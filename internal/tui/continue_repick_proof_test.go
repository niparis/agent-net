package tui

import (
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// continueMovedNote is the note the gateway wrote for a continue of an Opus
// worker while the owner had paused Opus (gateway TestContinueMovesAPausedModel).
const continueMovedNote = "difficulty R0 S0 C0 U0 V0 K0 · D 3.38 · hard; continue moved from claude claude-opus-5-5:high to opencode deepseek/deepseek-v4.1-flash:high at hard (old model: paused by the owner); uses pay-per-use provider (deepseek)"

// The worker log of a continued worker names the old model, the new one and
// why the continue moved, at both screen sizes (decision 0189). With
// AGENT_NET_PROOF_DIR set, the frames are written there
// (scripts/continue-repick-proof.sh).
func TestContinueRepickProofScenario(t *testing.T) {
	workerLogProof(t, workerLogScenario{
		file:   "continue-repick",
		action: "open the TUI after a continue of an Opus worker while the owner paused Opus (score 0)",
		want:   "continue moved from claude claude-opus-5-5:high to opencode deepseek/deepseek-v4.1-flash:high at hard (old model: paused by the owner)",
		entry: protocol.WorkerLogEntry{StartedAt: t0.Unix() - 600, Title: "Fix the review findings", Harness: "opencode",
			Model: "deepseek/deepseek-v4.1-flash:high", Status: "done", Difficulty: "hard", RunSeconds: 540, ModelNote: continueMovedNote},
	})
}
