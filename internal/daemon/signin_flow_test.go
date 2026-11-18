package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/client"
	"github.com/niparis/agent-net/internal/protocol"
)

const fakeConsoleToken = "throwaway-console-token-for-test"

// TestSignInFakeBL is a subprocess used in place of bl. It does not contact
// Alibaba or read the owner's files.
func TestSignInFakeBL(t *testing.T) {
	if os.Getenv("SIGNIN_FAKE_BL") != "1" {
		return
	}
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	args = args[1:]
	dir := os.Getenv("BAILIAN_CONFIG_DIR")
	if dir == "" {
		dir = filepath.Join(os.Getenv("HOME"), ".bailian")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "argv"), []byte(strings.Join(args, " ")), 0600); err != nil {
		t.Fatal(err)
	}
	save := func(token string) {
		raw, _ := json.Marshal(map[string]string{"access_token": token, "console_site": "international", "console_region": "ap-southeast-1"})
		if err := os.WriteFile(filepath.Join(dir, "config.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if len(args) > 0 && args[0] == "usage" {
		b, err := os.ReadFile(filepath.Join(dir, "config.json"))
		if err != nil || !strings.Contains(string(b), fakeConsoleToken) {
			t.Fatal("missing console credential")
		}
		fmt.Fprint(os.Stdout, `{"providers":[{"provider":"alibaba"}]}`)
		return
	}
	if strings.Contains(strings.Join(args, " "), "--token-stdin") {
		b, err := io.ReadAll(os.Stdin)
		if err != nil || string(b) != fakeConsoleToken {
			t.Fatal("bad stdin")
		}
		save(string(b))
		return
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := os.WriteFile(os.Getenv("SIGNIN_READY"), []byte(listener.Addr().String()), 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		save(fakeConsoleToken)
		w.WriteHeader(http.StatusOK)
		close(done)
	})}
	go server.Serve(listener)
	<-done
	// Close would drop the callback connection before its response is
	// flushed, and the test would see EOF. Shutdown waits for it.
	_ = server.Shutdown(context.Background())
}

func fakeSignInSetup(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	for _, key := range []string{"AGENT_NET_CONFIG_DIR", "AGENT_NET_STATE_DIR", "AGENT_NET_GATEWAY_DATA", "AGENT_NET_CLAUDE_SESSIONS_DIR"} {
		t.Setenv(key, filepath.Join(root, key))
	}
	t.Setenv("AGENT_NET_SOCKET", filepath.Join(root, "socket"))
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("BAILIAN_CONFIG_DIR", "")
	t.Setenv("TMPDIR", root)
	t.Setenv("SIGNIN_FAKE_BL", "1")
	ready := filepath.Join(root, "ready")
	t.Setenv("SIGNIN_READY", ready)
	bin := filepath.Join(root, "bl")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	body := "#!/bin/sh\nFAKE_AGY_PROCESS=1; export FAKE_AGY_PROCESS\nexec " + shellQuote(self) + " -test.run='^TestSignInFakeBL$' -- \"$@\"\n"
	if err := os.WriteFile(bin, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	return root, ready
}

func runSignInListener(t *testing.T, plan protocol.EnterPlan, ready string) {
	t.Helper()
	cmd := exec.Command(plan.Argv[0], plan.Argv[1:]...)
	cmd.Env = os.Environ()
	for k, v := range plan.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	var addr string
	eventually(t, "fake bl listener", func() bool { b, err := os.ReadFile(ready); addr = string(b); return err == nil && addr != "" })
	response, err := http.Get("http://" + addr + "/")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("callback status %s", response.Status)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestSignInLocalListenerUsesQuotaCredential(t *testing.T) {
	root, ready := fakeSignInSetup(t)
	d := &Daemon{cfg: Config{NodeID: "local", testGatewayCall: func(_ context.Context, typ string, _ any) (json.RawMessage, bool, error) {
		if typ == "query.sign_in" {
			return json.RawMessage(`{"provider":"alibaba","node_id":"local"}`), true, nil
		}
		return nil, false, nil
	}}}
	plan, err := d.signInOpen(context.Background(), protocol.SignInOpenRequest{AskID: "i_test"})
	if err != nil {
		t.Fatal(err)
	}
	runSignInListener(t, plan, ready)
	if err := d.signInComplete(context.Background(), "i_test", false); err != nil {
		t.Fatal(err)
	}
	args, err := os.ReadFile(filepath.Join(root, "home", ".bailian", "argv"))
	if err != nil || string(args) != "auth login --console --no-browser --console-site international" {
		t.Fatalf("unexpected login args %q: %v", args, err)
	}
	checkFakeQuota(t, os.Environ())
}

func checkFakeQuota(t *testing.T, env []string) {
	t.Helper()
	bin, err := signInBinary(env)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "usage", "token-plan", "--output", "json")
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil || !strings.Contains(string(out), `"alibaba"`) {
		t.Fatalf("quota row missing: %v", err)
	}
}

func TestSignInRemoteRelaysFreshTokenThroughStdin(t *testing.T) {
	root, ready := fakeSignInSetup(t)
	targetDir := filepath.Join(root, "target")
	targetEnv := append(os.Environ(), "BAILIAN_CONFIG_DIR="+targetDir)
	var transfer protocol.SignInTransfer
	d := &Daemon{cfg: Config{NodeID: "local", testGatewayCall: func(ctx context.Context, typ string, p any) (json.RawMessage, bool, error) {
		switch typ {
		case "query.sign_in":
			return json.RawMessage(`{"provider":"alibaba","node_id":"remote"}`), true, nil
		case "query.sign_in_transfer":
			transfer = p.(protocol.SignInTransfer)
			err := importSignInWithEnv(ctx, transfer.Token, targetEnv)
			return json.RawMessage(`{}`), true, err
		}
		return nil, false, nil
	}}}
	plan, err := d.signInOpen(context.Background(), protocol.SignInOpenRequest{AskID: "i_test"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Env["BAILIAN_CONFIG_DIR"] == "" {
		t.Fatal("no isolated config")
	}
	runSignInListener(t, plan, ready)
	if err := d.signInComplete(context.Background(), "i_test", false); err != nil {
		t.Fatal(err)
	}
	if transfer.Token != fakeConsoleToken || transfer.AskID != "i_test" {
		t.Fatal("wrong credential relayed")
	}
	if _, err := os.Stat(plan.Env["BAILIAN_CONFIG_DIR"]); !os.IsNotExist(err) {
		t.Fatal("temporary credentials remain")
	}
	want := "auth login --console --token-stdin --console-region ap-southeast-1 --console-site international"
	for _, dir := range []string{targetDir, filepath.Join(root, "home", ".bailian")} {
		b, err := os.ReadFile(filepath.Join(dir, "argv"))
		if err != nil || string(b) != want || strings.Contains(string(b), fakeConsoleToken) {
			t.Fatalf("unsafe import argv %q: %v", b, err)
		}
	}
	if b, _ := json.Marshal(plan); strings.Contains(string(b), fakeConsoleToken) {
		t.Fatal("token in TUI plan")
	}
	if !reflect.DeepEqual(plan.Argv[1:], []string{"auth", "login", "--console", "--no-browser", "--console-site", "international"}) {
		t.Fatalf("bad interactive argv: %v", plan.Argv)
	}
	checkFakeQuota(t, targetEnv)
	checkFakeQuota(t, os.Environ())
}

func TestSignInCancelledRemoteAttemptDoesNotTransfer(t *testing.T) {
	_, _ = fakeSignInSetup(t)
	calls := 0
	d := &Daemon{cfg: Config{NodeID: "local", testGatewayCall: func(_ context.Context, typ string, _ any) (json.RawMessage, bool, error) {
		if typ == "query.sign_in" {
			return json.RawMessage(`{"provider":"alibaba","node_id":"remote"}`), true, nil
		}
		if typ == "query.sign_in_transfer" {
			calls++
		}
		return nil, false, nil
	}}}
	plan, err := d.signInOpen(context.Background(), protocol.SignInOpenRequest{AskID: "i_test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.signInComplete(context.Background(), "i_test", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(plan.Env["BAILIAN_CONFIG_DIR"]); !os.IsNotExist(err) {
		t.Fatal("cancelled attempt left credentials")
	}
	if calls != 0 {
		t.Fatal("cancelled attempt sent credentials")
	}
}

func TestSignInWebClickDelegatesToOwnerTUI(t *testing.T) {
	var relay protocol.OwnerUIRelay
	d := &Daemon{cfg: Config{NodeID: "web", testGatewayCall: func(_ context.Context, typ string, p any) (json.RawMessage, bool, error) {
		if typ == "owner.ui.request" {
			relay = p.(protocol.OwnerUIRelay)
			return json.RawMessage(`{}`), true, nil
		}
		return nil, false, nil
	}}}
	plan, err := d.signInOpen(withVia(context.Background(), protocol.ViaPhone), protocol.SignInOpenRequest{AskID: "i_test"})
	if err != nil || !plan.OpenedHerdr || relay.Request.SignInAskID != "i_test" || len(plan.Argv) != 0 {
		t.Fatalf("web handoff failed: %+v %v", plan, err)
	}
}

func TestSignInWebClickReachesOneRemoteOwnerTUI(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	web := startNode(t, g, nodeOpts{})
	browser := startNode(t, g, nodeOpts{})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	requests := make(chan protocol.UIRequest, 1)
	c, err := client.DialPushData(browser.sock, func(method string, raw json.RawMessage) {
		var q protocol.UIRequest
		if method == "cto_ui" && json.Unmarshal(raw, &q) == nil && q.SignInAskID != "" {
			requests <- q
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Call(ctx, "hello", map[string]any{"client": "cli", "tui": true}); err != nil {
		t.Fatal(err)
	}
	raw, err := web.d.call(ctx, "sign_in.raise", protocol.SignInRaise{Provider: "alibaba"})
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		Ask protocol.Ask `json:"ask"`
	}
	if json.Unmarshal(raw, &created) != nil || created.Ask.ID == "" {
		t.Fatal("missing ask")
	}
	_, err = web.d.signInOpen(withVia(ctx, protocol.ViaPhone), protocol.SignInOpenRequest{AskID: created.Ask.ID})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case q := <-requests:
		if q.SignInAskID != created.Ask.ID {
			t.Fatalf("wrong ask: %+v", q)
		}
	case <-ctx.Done():
		t.Fatal("request did not reach the browser machine's TUI")
	}
}

func TestSignInUnfinishedRemoteAttemptDoesNotReuseCredentials(t *testing.T) {
	_, _ = fakeSignInSetup(t)
	calls := 0
	d := &Daemon{cfg: Config{NodeID: "local", testGatewayCall: func(_ context.Context, typ string, _ any) (json.RawMessage, bool, error) {
		if typ == "query.sign_in" {
			return json.RawMessage(`{"provider":"alibaba","node_id":"remote"}`), true, nil
		}
		if typ == "query.sign_in_transfer" {
			calls++
		}
		return nil, false, nil
	}}}
	plan, err := d.signInOpen(context.Background(), protocol.SignInOpenRequest{AskID: "i_test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.signInComplete(context.Background(), "i_test", false); err == nil {
		t.Fatal("unfinished login was accepted")
	}
	if _, err := os.Stat(plan.Env["BAILIAN_CONFIG_DIR"]); !os.IsNotExist(err) {
		t.Fatal("unfinished attempt left credentials")
	}
	if calls != 0 {
		t.Fatal("unfinished attempt sent credentials")
	}
}
