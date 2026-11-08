package protocol

import (
	"fmt"
	"strings"
)

// The owner's global charter level, for a session that no project node covers
// and for the MCP server instructions of a session that is starting.
//
// A session whose folder has a node already reads the global level in its
// effective charter: `refuses` and `rules` join across global, team, repo and
// agent by item, and `escalate` starts from the global value. The text here is
// only for the cases where there is no node text to carry it.

// GlobalCharter is the part of the global level that governs behavior.
type GlobalCharter struct {
	Rules    string `json:"rules,omitempty"`
	Refuses  string `json:"refuses,omitempty"`
	Escalate string `json:"escalate,omitempty"`
}

// Empty tells whether the owner set none of them.
func (g GlobalCharter) Empty() bool { return g.Rules == "" && g.Refuses == "" && g.Escalate == "" }

// GlobalCharterText is the charter text of a session that has no node: the
// global level, in the same line shape as a node's effective charter. It is ""
// when the owner set none.
func GlobalCharterText(g GlobalCharter) string {
	var b strings.Builder
	for _, a := range []struct{ name, value string }{
		{"rules", g.Rules},
		{"refuses", g.Refuses},
		{"escalate", g.Escalate},
	} {
		if a.value != "" {
			fmt.Fprintf(&b, "%s: %s\n", a.name, a.value)
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return "[agent-net] Your effective charter, from agent-net (global level only: no project node applies to this session):\n" +
		strings.TrimRight(b.String(), "\n")
}

// GlobalCharterGot answers the gateway frame "charter.global": the global level
// of the charter, for a connection that has no session yet (a Codex MCP
// process before a thread calls it), a worker brief or a new owner thread of a
// folder with no node.
type GlobalCharterGot struct {
	Global GlobalCharter `json:"global"`
}

// FrameCharterChanged is the frame the gateway sends to every connected daemon
// after the owner or the CTO changes a charter value (any level) or a team. A
// daemon drops what it cached of charter text, so the next turn of every
// session reads the new value without a timer running out first.
const FrameCharterChanged = "charter.changed"

// MethodCharterText is the local method with which an MCP server asks, once at
// start, for the charter text that goes into its instructions.
const MethodCharterText = "charter_text"

// CharterTextResult answers MethodCharterText. Text is the charter context of
// the bound session (its node and its agent), or only the global level when the
// connection has no session yet. It is empty when there is nothing to say.
type CharterTextResult struct {
	Text string `json:"text,omitempty"`
}

// MaxInstructionsRunes is how long the MCP server instructions may be. Claude
// Code caps the length of MCP server text (about 2 KB unless the owner raises
// CLAUDE_CODE_MAX_MCP_DESCRIPTION_LENGTH), so the text stays under that and the
// cut falls where this code chooses.
const MaxInstructionsRunes = 2000

const instructionsCut = "\n(cut here to fit the client's limit; your charter on every turn has the rest)"

// InstructionsWith adds the charter text to the fixed MCP instructions. The
// fixed text always comes first and whole; the charter is cut at a line end if
// the total would pass MaxInstructionsRunes. With no charter it returns base.
func InstructionsWith(charter, base string) string {
	charter = strings.TrimSpace(charter)
	if charter == "" {
		return base
	}
	out := base + "\n\n" + charter
	if len([]rune(out)) <= MaxInstructionsRunes {
		return out
	}
	room := MaxInstructionsRunes - len([]rune(base+"\n\n")) - len([]rune(instructionsCut))
	if room <= 0 {
		return base
	}
	cut := []rune(charter)[:room]
	if i := strings.LastIndex(string(cut), "\n"); i > 0 {
		cut = []rune(string(cut)[:i])
	}
	return fmt.Sprintf("%s\n\n%s%s", base, string(cut), instructionsCut)
}
