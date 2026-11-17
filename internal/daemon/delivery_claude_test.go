package daemon

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// confirmingCTO is a node whose fake Claude CTO writes the prompts it takes
// up to its transcript, with the delivery check on and short.
type confirmingCTO struct {
	n        *node
	logs     *logBuf
	peer     *localClient
	root     string // a message from the CTO that the peer replies to
	dropFile string
	sessions string
}

func startConfirmingCTO(t *testing.T) *confirmingCTO {
	t.Helper()
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	sessions, _ := useFakeClaude(t)
	t.Setenv(envFakeClaudeTranscribe, "1")
	dropFile := filepath.Join(t.TempDir(), "drop")
	t.Setenv(envFakeClaudeDropFile, dropFile)
	logs := &logBuf{}
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi", detect: fakeInventory, interval: time.Hour, logger: log.New(logs, "", 0), cfg: func(c *Config) {
		c.ClaudeSessionsDir = sessions
		c.Adapters = append(c.Adapters, &fsClaudeAdapter{dir: sessions})
		c.HarnessPaths = map[string]string{"claude": os.Args[0]}
		c.testNoDeliveryConfirm = false
		c.deliveryConfirmWait = scaled(500 * time.Millisecond)
	}})
	cli := cliClient(t, n)
	peer, peerID := helloPi(t, n, "cto-confirm-peer", "/w/peer", true)
	harness := "claude"
	cli.mustCall("cto_settings", protocol.CTOSettingsRequest{Harness: &harness})
	cli.mustCall("cto_send", map[string]string{"text": "initial"})
	eventually(t, "the first message confirmed", func() bool { return strings.Contains(logs.String(), "cto.delivery_confirmed") })
	root, _, err := g.gw.Store().StoreMessage(protocol.MsgSend{Sender: n.d.ctoSessionID(), Recipient: peerID, Body: "question"})
	if err != nil {
		t.Fatal(err)
	}
	return &confirmingCTO{n: n, logs: logs, peer: peer, root: root.ID, dropFile: dropFile, sessions: sessions}
}

// reply sends body from the peer to the CTO and returns the message ID.
func (c *confirmingCTO) reply(t *testing.T, body string) string {
	t.Helper()
	raw := c.peer.mustCall("reply", map[string]string{"message_id": c.root, "body": body})
	var r struct {
		Message protocol.Message `json:"message"`
	}
	if err := json.Unmarshal(raw, &r); err != nil || r.Message.ID == "" {
		t.Fatalf("reply result: %s %v", raw, err)
	}
	return r.Message.ID
}

// userTurns counts the user turns of the CTO's transcript that hold text.
func (c *confirmingCTO) userTurns(t *testing.T, text string) int {
	t.Helper()
	id, err := c.n.d.claudeCTOSession()
	if err != nil {
		t.Fatal(err)
	}
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(c.sessions), "projects", "*", id+".jsonl"))
	count := 0
	for _, path := range matches {
		raw, _ := os.ReadFile(path)
		for _, line := range strings.Split(string(raw), "\n") {
			var l struct {
				Type    string `json:"type"`
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			}
			if json.Unmarshal([]byte(line), &l) == nil && l.Type == "user" && strings.Contains(l.Message.Content, text) {
				count++
			}
		}
	}
	return count
}

// The incident of 2026-11-16: the running CTO's inbox accepted a message and
// Claude never started a turn for it. agent-net delivers it again, and then
// restarts the CTO's conversation with the message as its first prompt.
func TestClaudeCTODeliveryRetriedThenRestarted(t *testing.T) {
	c := startConfirmingCTO(t)
	before, err := c.n.d.claudeCTOSession()
	if err != nil {
		t.Fatal(err)
	}
	// The inbox takes the next two lines and does nothing with them.
	if err := os.WriteFile(c.dropFile, []byte("2"), 0o600); err != nil {
		t.Fatal(err)
	}
	id := c.reply(t, "please answer this")
	eventually(t, "the message is a user turn after the restart", func() bool { return c.userTurns(t, "please answer this") == 1 })
	for _, want := range []string{"cto.delivery_retry msg=" + id, "cto.restart_for_delivery msg=" + id} {
		if got := len(c.logs.lines(want)); got != 1 {
			t.Errorf("%q logged %d times, want 1:\n%s", want, got, c.logs.String())
		}
	}
	rec := readClaude(t, filepath.Join(c.n.cfg.StateDir, "cto", "claude"))
	if argValue(rec.Args, "--resume") != before || argAfterDashes(rec.Args) == "" || !strings.Contains(argAfterDashes(rec.Args), "please answer this") {
		t.Fatalf("restart must resume the conversation with the message as its first prompt: %q", rec.Args)
	}
	if after, _ := c.n.d.claudeCTOSession(); after != before {
		t.Fatalf("conversation changed: %q -> %q", before, after)
	}
	t.Log("CTO lines:\n" + strings.Join(c.logs.lines("cto."), "\n"))
}

// A message the CTO takes up at once is checked and never sent again.
func TestClaudeCTODeliveryTakenUpOnce(t *testing.T) {
	c := startConfirmingCTO(t)
	id := c.reply(t, "quick question")
	eventually(t, "the delivery confirmed", func() bool { return len(c.logs.lines("cto.delivery_confirmed msg="+id)) == 1 })
	if got := c.userTurns(t, "quick question"); got != 1 {
		t.Fatalf("user turns with the message: %d, want 1", got)
	}
	if lines := c.logs.lines("msg="+id, "cto.delivery_retry"); len(lines) != 0 {
		t.Fatalf("retried a message the CTO took up: %v", lines)
	}
	if lines := c.logs.lines("cto.restart_for_delivery"); len(lines) != 0 {
		t.Fatalf("restarted for a message the CTO took up: %v", lines)
	}
}

// The cost of the check on a taken message: one read of the transcript, and
// nothing else. The check never polls.
func TestClaudeCTODeliveryReadsOncePerTakenMessage(t *testing.T) {
	c := startConfirmingCTO(t)
	before := c.n.d.deliveryReads.Load()
	id := c.reply(t, "one read please")
	eventually(t, "the delivery confirmed", func() bool { return len(c.logs.lines("cto.delivery_confirmed msg="+id)) == 1 })
	if got := c.n.d.deliveryReads.Load() - before; got != 1 {
		t.Fatalf("reads for one taken delivery: %d, want 1", got)
	}
}
