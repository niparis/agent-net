package daemon_test

import (
	"time"

	"github.com/niparis/agent-net/internal/daemon"
	"github.com/niparis/agent-net/internal/daemon/claude"
)

func init() {
	daemon.TestClaudeAdapterFactory = func(sessions, replies string) daemon.Adapter {
		return claude.New(claude.Config{SessionsDir: sessions, ReplyDir: replies, PollInterval: 20 * time.Millisecond})
	}
}
