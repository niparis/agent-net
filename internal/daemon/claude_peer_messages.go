package daemon

import (
	"context"
	"path/filepath"
	"time"

	"github.com/niparis/agent-net/internal/peermessages"
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
	now := time.Now()
	if !d.mouseNoticeDue(key, now) {
		return
	}
	d.markMouseNotice(key, now)
	d.Logf("claude peer messages: %s", rep.Problem)
}
