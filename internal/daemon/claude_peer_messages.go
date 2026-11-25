package daemon

import (
	"context"
	"path/filepath"
	"time"

	"github.com/niparis/agent-net/internal/peermessages"
	"github.com/niparis/agent-net/internal/protocol"
)

// peerMessagesStatePath is the record setup writes about the owner's answer
// for Claude Code's cross-session setting.
func peerMessagesStatePath() string {
	return filepath.Join(ConfigDir(), "claude-peer-messages.json")
}

// checkClaudePeerMessages compares the live Claude setting with the recorded
// answer. A managed settings file can lose the key after setup, and then
// agent-net reports messages as delivered while Claude Code holds them. The
// check only reads files: the owner decides whether to accept peer messages.
func (d *Daemon) checkClaudePeerMessages(ctx context.Context) {
	path := d.claudeSettingsFile()
	if path == "" {
		return
	}
	rep := peermessages.Compare(peermessages.LoadState(peerMessagesStatePath()), path)
	if rep.Problem == "" {
		return
	}
	key := "peer-messages:" + path
	if !d.mouseNoticeDue(key, time.Now()) {
		return
	}
	d.Logf("claude peer messages: %s", rep.Problem)
	d.reportClaudeNotice(ctx, key, rep.Problem, peerMessagesIntake)
}

func peerMessagesIntake(text string) protocol.IntakeReport {
	return protocol.IntakeReport{Kind: protocol.IntakeClaudePeerMessages, Subject: "claude", Text: text}
}
