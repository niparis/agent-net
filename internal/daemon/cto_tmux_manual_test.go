package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestManualCTOTmux(t *testing.T) {
	if os.Getenv("AGENT_NET_TMUX_CAPTURE") != "1" {
		t.Skip("manual tmux capture")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{detect: fakeInventory, interval: time.Hour, cfg: func(c *Config) { c.HarnessPaths = map[string]string{"opencode": os.Args[0]} }})
	cli := cliClient(t, n)
	cli.mustCall("cto_send", map[string]string{"text": "hello from tmux"})
	t.Setenv("AGENT_NET_SOCKET", n.sock)
	sock := filepath.Join(shortDir(t), "tmux.sock")
	tm := func(args ...string) (string, error) {
		cmd := exec.Command("tmux", append([]string{"-S", sock, "-f", "/dev/null"}, args...)...)
		b, err := cmd.CombinedOutput()
		return string(b), err
	}
	out, err := tm("new-session", "-d", "-s", "cto", "-x", "120", "-y", "40", os.Args[0], "fake-tui")
	if err != nil {
		t.Fatalf("tmux: %s: %v", out, err)
	}
	defer tm("kill-server")
	if out, err = tm("send-keys", "-t", "cto", "C"); err != nil {
		t.Fatalf("send key: %s %v", out, err)
	}
	var capture string
	eventually(t, "CTO pane in tmux", func() bool {
		capture, _ = tm("capture-pane", "-p", "-t", "cto")
		return strings.Contains(capture, "CTO (") && strings.Contains(capture, "hello from tmux")
	})
	t.Logf("tmux capture:\n%s", capture)
}
