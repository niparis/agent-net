package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The real app server and a local fake model check the role's shell settings.
// Set AGENT_NET_TEST_REAL_CODEX to a Codex binary (or 1 for PATH) to run it.
func TestCodexMergerRealServerRunsWithoutApproval(t *testing.T) {
	bin := os.Getenv("AGENT_NET_TEST_REAL_CODEX")
	if bin == "" {
		t.Skip("set AGENT_NET_TEST_REAL_CODEX to run the real Codex check")
	}
	if bin == "1" {
		var err error
		if bin, err = exec.LookPath("codex"); err != nil {
			t.Skip("codex is not on PATH")
		}
	}
	home, codexHome := shortDir(t), shortDir(t)
	for k, v := range map[string]string{"HOME": home, "TMPDIR": home, "XDG_CONFIG_HOME": home + "/.config", "XDG_STATE_HOME": home + "/.state", "XDG_DATA_HOME": home + "/.data", "CODEX_HOME": codexHome} {
		t.Setenv(k, v)
	}
	model := &toolLogModel{inner: &mockModel{}}
	server := httptest.NewServer(model)
	t.Cleanup(server.Close)
	config := fmt.Sprintf("model_provider = \"mock\"\nmodel = \"mock-model\"\n[model_providers.mock]\nname = \"mock\"\nbase_url = %q\nwire_api = \"responses\"\nrequires_openai_auth = false\n", server.URL+"/v1")
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	port, err := freePort()
	if err != nil {
		t.Fatal(err)
	}
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	token := "test-token"
	tokenFile := filepath.Join(home, "token")
	if err := os.WriteFile(tokenFile, []byte(token), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "app-server", "--listen", "ws://"+addr, "--ws-auth", "capability-token", "--ws-token-file", tokenFile)
	cmd.Dir = home
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); cmd.Wait() })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rpc, err := dialCodexWS(ctx, addr, token, "test", func() bool { return true }, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rpc.close()
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	repo := newRepo(t, "shop")
	dir, err := n.d.mergerFolder(context.Background(), repo, "shop")
	if err != nil {
		t.Fatal(err)
	}
	r := protocol.StartedSession{Type: "merger", Path: dir}
	params := map[string]any{"cwd": dir, "model": "mock-model"}
	params["config"] = n.d.codexOwnerRolePolicy(context.Background(), r, params)
	raw, err := rpc.call(context.Background(), "thread/start", params)
	if err != nil {
		t.Fatal(err)
	}
	var started struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(raw, &started); err != nil || started.Thread.ID == "" {
		t.Fatalf("thread/start = %s: %v", raw, err)
	}
	command := "printf '%s' \"$GOCACHE\" > cache.txt; git add cache.txt; git -c user.name=test -c user.email=test@example.invalid commit -m test"
	args, _ := json.Marshal(map[string]string{"cmd": command})
	model.inner.setArgs(string(args))
	_, err = rpc.call(context.Background(), "turn/start", map[string]any{"threadId": started.Thread.ID, "input": []map[string]any{{"type": "text", "text": "CALLTOOL exec_command", "text_elements": []any{}}}})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "shell result reaches the model", 90*time.Second, func() bool { return len(model.outputs()) > 0 })
	out := model.outputs()[0]
	if got, err := os.ReadFile(filepath.Join(dir, "cache.txt")); err != nil || string(got) != n.d.workerGoCacheDir() {
		t.Fatalf("GOCACHE = %q, err = %v; tool output %s", got, err, out)
	}
	if !strings.Contains(out, "1 file changed") {
		t.Fatalf("commit did not finish: %s", out)
	}
}
