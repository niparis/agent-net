package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

type fakeAgyRecord struct {
	Args                        []string
	Cwd, Native, Worker, Socket string
	PID, PGID, Child            int
	Turns                       []string
	MCPError                    string
	InputClosed                 bool
}

// The fake agy script execs this test binary. No vendor binaries, credentials,
// or user configuration are used, including by its MCP client.
func TestAgyProcess(t *testing.T) {
	if os.Getenv("FAKE_AGY_PROCESS") != "1" {
		return
	}
	args := os.Args
	for i, a := range args {
		if a == "--" {
			args = args[i+1:]
			break
		}
	}
	cwd, _ := os.Getwd()
	native := argValue(args, "--conversation")
	if native == "" {
		native = "00000000-0000-4000-8000-000000000049"
	}
	pgid, _ := syscall.Getpgid(0)
	r := fakeAgyRecord{Args: args, Cwd: cwd, Native: native, Worker: os.Getenv("AGENT_NET_WORKER"), Socket: os.Getenv("AGENT_NET_SOCKET"), PID: os.Getpid(), PGID: pgid}
	path := filepath.Join(os.Getenv("FAKE_AGY_RECORDS"), strconv.Itoa(r.PID)+".json")
	save := func() {
		b, _ := json.Marshal(r)
		tmp := path + ".tmp"
		_ = os.WriteFile(tmp, b, 0600)
		_ = os.Rename(tmp, path)
	}
	write := func(v any) { b, _ := json.Marshal(v); fmt.Println(string(b)) }
	save()
	write(map[string]any{"event": "init", "conversation_id": native, "init": map[string]any{"model": "gemini-3.8-flash-low", "cwd": cwd}})
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 4096), 1<<20)
	for sc.Scan() {
		var in struct {
			Event   string `json:"event"`
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &in) != nil || in.Event != "user" {
			os.Exit(2)
		}
		body := in.Message.Content
		r.Turns = append(r.Turns, body)
		save()
		write(map[string]any{"event": "step_update", "step_update": map[string]any{"conversation_id": native}})
		if strings.Contains(body, "FAKE:usage") {
			// Two model steps of one turn. agy reports each step's usage, and the
			// result below repeats the run's sum.
			for _, u := range []map[string]any{
				{"input_tokens": 10000, "output_tokens": 40, "thinking_tokens": 10, "cache_read_tokens": 0, "total_tokens": 10050},
				{"input_tokens": 10448, "output_tokens": 59, "thinking_tokens": 0, "cache_read_tokens": 0, "total_tokens": 10507},
			} {
				write(map[string]any{"event": "step_update", "step_update": map[string]any{
					"conversation_id": native, "step_type": "agent_response", "state": "DONE", "usage": u}})
			}
		}
		if strings.Contains(body, "FAKE:gate") {
			for {
				if _, err := os.Stat(filepath.Join(os.Getenv("FAKE_AGY_RECORDS"), "release")); err == nil {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
		}
		if strings.Contains(body, "FAKE:hang") {
			signal.Ignore(syscall.SIGTERM)
			child := exec.Command("sh", "-c", "trap '' TERM; while :; do sleep 1; done")
			if child.Start() == nil {
				r.Child = child.Process.Pid
				save()
			}
			for {
				time.Sleep(time.Second)
			}
		}
		if strings.Contains(body, "FAKE:error") {
			fmt.Fprintln(os.Stderr, `AGY_ERROR: {"message":"local worker exploded"}`)
			os.Exit(3)
		}
		if strings.Contains(body, "FAKE:edit") {
			_ = os.WriteFile(filepath.Join(cwd, "agy-work.txt"), []byte("work\n"), 0644)
		}
		if strings.Contains(body, "FAKE:ignored") {
			_ = os.WriteFile(filepath.Join(cwd, "ignored.txt"), []byte("ignored\n"), 0644)
		}
		if strings.Contains(body, "FAKE:selfcommit") {
			cmd := exec.Command("git", "add", "agy-work.txt")
			cmd.Dir = cwd
			if err := cmd.Run(); err != nil {
				os.Exit(3)
			}
			// CI has no global git identity; give the fake its own.
			cmd = exec.Command("git", "-c", "user.name=fake agy", "-c", "user.email=fake@localhost", "commit", "-q", "-m", "worker commit")
			cmd.Dir = cwd
			if err := cmd.Run(); err != nil {
				os.Exit(3)
			}
		}
		partial := strings.Contains(r.Turns[0], "FAKE:partial")
		if strings.Contains(body, "FAKE:handback") || partial {
			conn, err := net.Dial("unix", r.Socket)
			if err == nil {
				enc, dec := json.NewEncoder(conn), json.NewDecoder(conn)
				call := func(id, method string, p any) bool {
					_ = enc.Encode(map[string]any{"id": id, "method": method, "params": p})
					for {
						var v struct {
							ID    string          `json:"id"`
							Error json.RawMessage `json:"error"`
						}
						if dec.Decode(&v) != nil {
							return false
						}
						if v.ID == id {
							return len(v.Error) == 0 || string(v.Error) == "null"
						}
					}
				}
				hello := func(id string) map[string]any {
					return map[string]any{"client": "mcp", "harness": "agy", "native_id": id, "protocol": protocol.LocalProtocol, "cwd": cwd}
				}
				if call("wrong", "hello", hello("other-conversation")) {
					r.MCPError = "accepted another conversation"
				}
				conn.Close()
				conn, err = net.Dial("unix", r.Socket)
				if err != nil {
					os.Exit(2)
				}
				enc, dec = json.NewEncoder(conn), json.NewDecoder(conn)
				if !call("hello", "hello", hello(native)) {
					r.MCPError += " rejected worker conversation"
				}
				complete := !partial
				var files []string
				if strings.Contains(body, "FAKE:filehandback") {
					file := filepath.Join(cwd, "agy-result.txt")
					if err := os.WriteFile(file, []byte("agy file\n"), 0600); err != nil {
						os.Exit(3)
					}
					files = []string{file}
				}
				if !call("handback", protocol.OpWorkerHandback, protocol.WorkerHandbackRequest{Report: "finished agy task", Complete: &complete, Files: files}) {
					r.MCPError += " rejected handback"
				}
				conn.Close()
			} else {
				r.MCPError = err.Error()
			}
			save()
		}
		if strings.Contains(body, "FAKE:result-gate") {
			for {
				if _, err := os.Stat(filepath.Join(os.Getenv("FAKE_AGY_RECORDS"), "result-release")); err == nil {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
		}
		result := map[string]any{"conversation_id": native, "status": "SUCCESS", "response": "turn ended", "usage": map[string]any{"input_tokens": 1200, "output_tokens": 340, "thinking_tokens": 50, "cache_read_tokens": 90, "total_tokens": 1590}}
		if strings.Contains(body, "FAKE:usage") {
			result["usage"] = map[string]any{"input_tokens": 20448, "output_tokens": 99, "thinking_tokens": 10, "cache_read_tokens": 0, "total_tokens": 20557}
		}
		if strings.Contains(body, "FAKE:failed-result") {
			result["status"] = "ERROR"
			result["response"] = "failed tool"
		}
		if strings.Contains(body, "FAKE:quota") {
			result["status"], result["response"], result["finished_reason"] = "ERROR", "", "error"
			result["error"] = "Individual quota reached. Resets in 3h42m27s."
		}
		if strings.Contains(body, "FAKE:timeout") {
			result["finished_reason"] = "print_timeout"
		}
		if strings.Contains(body, "FAKE:denied") {
			result["denied_actions"] = []map[string]any{
				{"tool_name": "ReadFile", "args": map[string]any{"Path": "/home/test/repo/.git/worktrees/w_test/HEAD"}, "reason": "outside the workspace"},
				{"tool_name": "run_command", "args": map[string]any{"CommandLine": "curl -H 'Authorization: Bearer sk-secretsecretsecretsecret'"}, "reason": "not permitted"},
			}
		}
		write(map[string]any{"event": "result", "result": result})
		if strings.Contains(body, "FAKE:exit") {
			os.Exit(0)
		}
	}
	r.InputClosed = true
	save()
	os.Exit(0)
}

func TestAgyWorkerHandbackFiles(t *testing.T) {
	w, _, _ := startAgyNode(t, shortSet)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "agy", Model: "gemini-3.8-flash-low", Task: "FAKE:handback FAKE:filehandback", Isolation: protocol.IsolationWorktree})
	n := w.waitNotice(t, 1)[0]
	if n.From != res.SessionID || len(n.Files) != 1 || n.Files[0].Name != "agy-result.txt" {
		t.Fatalf("notice: %+v", n)
	}
	path := filepath.Join(w.cfg.StateDir, "inbox", w.ownerID(), n.ID, "agy-result.txt")
	eventually(t, "Antigravity handback file", func() bool { b, err := os.ReadFile(path); return err == nil && string(b) == "agy file\n" })
}

func TestAgyWorkerHandbackFilesSnapshotOnce(t *testing.T) {
	w, dir, _ := startAgyNode(t, shortSet)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "agy", Model: "gemini-3.8-flash-low", Task: "FAKE:handback FAKE:filehandback FAKE:result-gate", Isolation: protocol.IsolationWorktree})
	wk := w.d.workerBySession(res.SessionID)
	eventually(t, "Antigravity acknowledged handback", func() bool {
		wk.agy.mu.Lock()
		defer wk.agy.mu.Unlock()
		return wk.agy.handback != nil
	})
	if err := os.WriteFile(filepath.Join(res.Worktree, "agy-result.txt"), []byte("later content\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "result-release"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	n := w.waitNotice(t, 1)[0]
	path := filepath.Join(w.cfg.StateDir, "inbox", w.ownerID(), n.ID, "agy-result.txt")
	eventually(t, "Antigravity snapshot delivered", func() bool { _, err := os.Stat(path); return err == nil })
	if got := string(mustReadFile(t, path)); got != "agy file\n" {
		t.Fatalf("got %q, want bytes checked at handback", got)
	}
}

func TestAgyWorkerHandbackFilesAfterRestart(t *testing.T) {
	set := shortSet
	set.Idle, set.IdleNotice = time.Hour, time.Hour
	w, dir, _ := startAgyNode(t, set)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "agy", Model: "gemini-3.8-flash-low", Task: "FAKE:handback FAKE:filehandback FAKE:result-gate FAKE:exit", Isolation: protocol.IsolationWorktree})
	wk := w.d.workerBySession(res.SessionID)
	eventually(t, "Antigravity acknowledged handback", func() bool {
		wk.agy.mu.Lock()
		defer wk.agy.mu.Unlock()
		return wk.agy.handback != nil
	})
	pid := agyRecords(dir)[0].PID
	w.d.Stop()
	if err := os.WriteFile(filepath.Join(dir, "result-release"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	eventually(t, "Antigravity result written during downtime", func() bool { return !alive(pid) })
	// Keep the gateway unavailable while the saved result is replayed. A
	// file handback cannot use the offline outbox and must wait for the link.
	d, err := New(w.cfg)
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	early := make(chan struct{}, 1)
	d.cfg.testGatewayCall = func(_ context.Context, typ string, _ any) (json.RawMessage, bool, error) {
		if typ == "msg.send" && !d.Connected() {
			select {
			case early <- struct{}{}:
			default:
			}
		}
		return nil, false, nil
	}
	transport := d.client.Transport.(*http.Transport).Clone()
	dial := transport.DialContext
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		select {
		case <-release:
			return dial(ctx, network, addr)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	d.client.Transport = transport
	if err := d.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	w.node.d = d
	t.Cleanup(d.Stop)
	recovered := d.workerBySession(res.SessionID)
	eventually(t, "Antigravity result replayed before gateway connection", func() bool {
		select {
		case <-recovered.agy.stopped:
			return true
		default:
			return false
		}
	})
	select {
	case <-early:
		t.Fatal("file handback attempted before the gateway connected")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	eventually(t, "daemon reconnected to gateway", d.Connected)
	w.cli = cliClient(t, w.node)
	n := w.waitNotice(t, 1)[0]
	path := filepath.Join(w.cfg.StateDir, "inbox", w.ownerID(), n.ID, "agy-result.txt")
	eventually(t, "recovered Antigravity file", func() bool { _, err := os.Stat(path); return err == nil })
	if got := string(mustReadFile(t, path)); got != "agy file\n" {
		t.Fatalf("recovered file = %q", got)
	}
}

func startAgyNode(t *testing.T, set WorkerSettings) (*workerNode, string, string) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "agy")
	script := "#!/bin/sh\nFAKE_AGY_RECORDS=" + shellQuote(dir) + " FAKE_AGY_PROCESS=1 exec " + shellQuote(os.Args[0]) + " -test.run '^TestAgyProcess$' -- \"$@\"\n"
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNodeWithDetect(t, g, set, agyInventory, func(c *Config) { c.HarnessPaths = map[string]string{"agy": bin} })
	repo := newRepo(t, "shop")
	w.register(t, repo)
	return w, dir, repo
}

func agyRecords(dir string) []fakeAgyRecord {
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	var out []fakeAgyRecord
	for _, f := range files {
		var r fakeAgyRecord
		b, _ := os.ReadFile(f)
		if json.Unmarshal(b, &r) == nil {
			out = append(out, r)
		}
	}
	return out
}

func sendAgy(t *testing.T, w *workerNode, sid, body string) {
	t.Helper()
	w.cli.mustCall(protocol.OpSend, protocol.OwnerSendRequest{To: sid, Body: body})
}

func TestAgyWorkerLifecycleAndResume(t *testing.T) {
	t.Parallel()
	w, dir, _ := startAgyNode(t, shortSet)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "agy", Model: "gemini-3.8-flash-low", Task: "FAKE:handback", Isolation: protocol.IsolationWorktree})
	n := w.waitNotice(t, 1)[0]
	for _, s := range []string{"status: done", "1200 in, 340 out, 50 reasoning, 90 cache read", "finished agy task"} {
		if !strings.Contains(n.Body, s) {
			t.Errorf("notice lacks %q: %s", s, n.Body)
		}
	}
	eventually(t, "agy ends after handback", func() bool { return w.d.workerCount() == 0 })
	recs := agyRecords(dir)
	if len(recs) != 1 {
		t.Fatalf("records: %+v", recs)
	}
	r := recs[0]
	if r.Cwd != res.Worktree || r.PID != r.PGID || r.Worker == "" || r.Socket != w.cfg.SocketPath || r.MCPError != "" {
		t.Fatalf("record: %+v", r)
	}
	if len(r.Turns) != 1 || !strings.Contains(r.Turns[0], protocol.WorkerPreamble) {
		t.Fatalf("brief: %+v", r.Turns)
	}
	noFlagThatSkipsPermissions(t, r.Args)
	if r.Args[len(r.Args)-1] != "-p=" || argValue(r.Args, "--input-format") != "stream-json" || argValue(r.Args, "--output-format") != "stream-json" || argValue(r.Args, "--model") != "gemini-3.8-flash-low" || argValue(r.Args, "--mode") != "accept-edits" || !strings.Contains(strings.Join(r.Args, " "), "--sandbox") || argValue(r.Args, "--settings") != `{"permissions":{"allow":["command(*)","mcp(agent-net_agent-net/*)"]}}` {
		t.Fatalf("args: %v", r.Args)
	}
	sendAgy(t, w, res.SessionID, "FAKE:handback again")
	w.waitNotice(t, 2)
	eventually(t, "agy resumes and ends", func() bool { return w.d.workerCount() == 0 && len(agyRecords(dir)) == 2 })
	for _, rec := range agyRecords(dir) {
		if rec.PID != r.PID && argValue(rec.Args, "--conversation") != r.Native {
			t.Fatalf("resume args: %v", rec.Args)
		}
	}
}

// agy reports per-step usage in its stream but no context window, so the
// limit comes from the model's family. The worker shows the context of the
// last agent_response step, that limit, the run's tokens, and a notice with
// the percentage.
func TestAgyWorkerContextFromSteps(t *testing.T) {
	t.Parallel()
	w, dir, _ := startAgyNode(t, shortSet)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "agy", Model: "gemini-3.8-flash-low", Task: "FAKE:usage FAKE:gate FAKE:handback", Isolation: protocol.IsolationWorktree})
	wk := w.d.workerBySession(res.SessionID)
	eventually(t, "step usage recorded", func() bool {
		info := w.d.workerInfo(wk)
		return info.ContextTokens == 10448 && info.ContextLimit == 1048576 && info.Tokens == 20557
	})
	if err := os.WriteFile(filepath.Join(dir, "release"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	n := w.waitNotice(t, 1)[0]
	for _, s := range []string{"context at end: 10k / 1M (1%)\n", "tokens: 20448 in, 99 out, 10 reasoning\n"} {
		if !strings.Contains(n.Body, s) {
			t.Errorf("notice lacks %q:\n%s", s, n.Body)
		}
	}
	eventually(t, "agy ends after handback", func() bool { return w.d.workerCount() == 0 })
}

// agyContextLimit maps an Antigravity model to the context window of its
// family. A model outside the table has no limit, so the views show tokens
// only, as before.
func TestAgyContextLimit(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		model string
		want  int64
	}{
		{"gemini-3.8-flash-medium", 1048576},
		{"gemini-3.8-flash-low", 1048576},
		{"gemini-3.1-pro-low", 1048576},
		{"gemini-4-argon-medium", 1048576},
		{"claude-opus-5-5-medium", 1000000},
		{"claude-sonnet-5-5-low", 1000000},
		{"gpt-oss-120b-medium", 131072},
		{"mystery-model", 0},
		{"", 0},
	} {
		if got := agyContextLimit(c.model); got != c.want {
			t.Errorf("agyContextLimit(%q) = %d, want %d", c.model, got, c.want)
		}
	}
}

func TestAgyWorkerFollowupAndAutoContinue(t *testing.T) {
	t.Parallel()
	set := shortSet
	set.Idle = time.Hour
	set.IdleNotice = 30 * time.Millisecond
	w, dir, _ := startAgyNode(t, set)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "agy", Model: "gemini-3.8-flash-low", Task: "reply OK"})
	eventually(t, "two auto continues in the same process", func() bool { rs := agyRecords(dir); return len(rs) == 1 && len(rs[0].Turns) == 3 })
	sendAgy(t, w, res.SessionID, "follow-up line")
	eventually(t, "followup in same process", func() bool {
		rs := agyRecords(dir)
		return len(rs) == 1 && len(rs[0].Turns) == 4 && strings.Contains(rs[0].Turns[3], "follow-up line")
	})
	wk := w.d.workerBySession(res.SessionID)
	eventually(t, "usage recorded", func() bool {
		st := w.d.workerStats(wk)
		return st.TotalTokens == 4*1590 && strings.Contains(st.Tokens, "4800 in")
	})
	w.d.wmu.Lock()
	count := wk.autoContinueCount
	w.d.wmu.Unlock()
	if count != 2 {
		t.Fatalf("auto continues: %d", count)
	}
}

func TestAgyWorkerPartialHandbackAutoContinue(t *testing.T) {
	t.Parallel()
	set := shortSet
	set.IdleNotice = time.Hour
	w, dir, _ := startAgyNode(t, set)
	w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "agy", Model: "gemini-3.8-flash-low", Task: "FAKE:partial"})
	n := w.waitNotice(t, 1)[0]
	if !strings.Contains(n.Body, "auto-continues: 2") || !strings.Contains(n.Body, "3600 in") {
		t.Fatalf("notice: %s", n.Body)
	}
	eventually(t, "partial handback ends", func() bool { return w.d.workerCount() == 0 })
	rs := agyRecords(dir)
	if len(rs) != 1 || len(rs[0].Turns) != 3 || rs[0].MCPError != "" {
		t.Fatalf("records: %+v", rs)
	}
}

func TestAgyWorkerQueuesFollowups(t *testing.T) {
	t.Parallel()
	set := shortSet
	set.IdleNotice = time.Hour
	w, dir, _ := startAgyNode(t, set)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "agy", Model: "gemini-3.8-flash-low", Task: "FAKE:gate", AutoContinue: autoContinueOff()})
	eventually(t, "first turn running", func() bool { rs := agyRecords(dir); return len(rs) == 1 && len(rs[0].Turns) == 1 })
	sendAgy(t, w, res.SessionID, "second turn")
	sendAgy(t, w, res.SessionID, "third turn")
	wk := w.d.workerBySession(res.SessionID)
	eventually(t, "both followups queued", func() bool { w.d.wmu.Lock(); defer w.d.wmu.Unlock(); return len(wk.turnQueue) == 2 })
	if err := os.WriteFile(filepath.Join(dir, "release"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	eventually(t, "queued turns delivered in order", func() bool {
		rs := agyRecords(dir)
		return len(rs) == 1 && len(rs[0].Turns) == 3 && strings.Contains(rs[0].Turns[1], "second turn") && strings.Contains(rs[0].Turns[2], "third turn")
	})
	eventually(t, "last result leaves the worker idle", func() bool { return !w.d.sessionBusy("agy", agyRecords(dir)[0].Native) })
}

func TestAgyWorkerStopClosesInput(t *testing.T) {
	t.Parallel()
	w, dir, _ := startAgyNode(t, shortSet)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "agy", Model: "gemini-3.8-flash-low", Task: "reply OK", AutoContinue: autoContinueOff()})
	w.cli.mustCall(protocol.OpWorkerStop, protocol.WorkerStopRequest{Session: res.SessionID})
	eventually(t, "worker process stopped", func() bool { rs := agyRecords(dir); return len(rs) == 1 && !alive(rs[0].PID) })
}

func TestAgyWorkerHandbackKeepsQueuedFollowup(t *testing.T) {
	t.Parallel()
	w, dir, _ := startAgyNode(t, shortSet)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "agy", Model: "gemini-3.8-flash-low", Task: "FAKE:gate FAKE:handback"})
	wk := w.d.workerBySession(res.SessionID)
	eventually(t, "first turn is running", func() bool { return len(agyRecords(dir)[0].Turns) == 1 })
	sendAgy(t, w, res.SessionID, "FAKE:handback queued followup")
	eventually(t, "followup is queued", func() bool { w.d.wmu.Lock(); defer w.d.wmu.Unlock(); return len(wk.turnQueue) == 1 })
	if err := os.WriteFile(filepath.Join(dir, "release"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	w.waitNotice(t, 2)
	eventually(t, "worker ends after both reports", func() bool { return w.d.workerCount() == 0 })
	rs := agyRecords(dir)
	if len(rs) != 1 || len(rs[0].Turns) != 2 || rs[0].MCPError != "" {
		t.Fatalf("queued handback: %+v", rs)
	}
}

func TestAgyWorkerStopInterruptsBlockedInput(t *testing.T) {
	rd, wr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer rd.Close()
	defer wr.Close()
	c := &agyConn{in: wr}
	w := &worker{agy: c, dir: t.TempDir()}
	c.w = w
	sent := make(chan error, 1)
	go func() { sent <- c.send(bytes.Repeat([]byte("x"), 1<<20)) }()
	// Observing a byte proves the write started; the rest exceeds pipe capacity.
	if _, err := rd.Read(make([]byte, 1)); err != nil {
		t.Fatal(err)
	}
	stopped := make(chan struct{})
	go func() { (agyLauncher{}).stop(&Daemon{}, w); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		rd.Close()
		t.Fatal("stop waited for a blocked input write")
	}
	select {
	case err := <-sent:
		if err == nil {
			t.Fatal("blocked write unexpectedly completed")
		}
	case <-time.After(time.Second):
		t.Fatal("closing stdin did not interrupt the write")
	}
}

func TestAgyWorkerResumeAfterProcessExit(t *testing.T) {
	t.Parallel()
	set := shortSet
	set.IdleNotice = time.Hour
	set.Idle = time.Hour
	w, dir, _ := startAgyNode(t, set)
	defer func() {
		if t.Failed() {
			t.Logf("records: %+v; logs: %v", agyRecords(dir), w.logs.lines())
		}
	}()
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "agy", Model: "gemini-3.8-flash-low", Task: "FAKE:exit", AutoContinue: autoContinueOff()})
	wk := w.d.workerBySession(res.SessionID)
	eventually(t, "process exits", func() bool { return w.d.gone(wk) })
	sendAgy(t, w, res.SessionID, "reply OK after exit")
	eventually(t, "new process resumes conversation", func() bool {
		rs := agyRecords(dir)
		if len(rs) != 2 {
			return false
		}
		for _, r := range rs {
			if argValue(r.Args, "--conversation") == r.Native && len(r.Turns) == 1 {
				return true
			}
		}
		return false
	})
}

func TestAgyWorkerExitedProcessStillExpires(t *testing.T) {
	t.Parallel()
	set := shortSet
	set.Idle = scaled(500 * time.Millisecond)
	set.IdleNotice = time.Hour
	w, _, _ := startAgyNode(t, set)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "agy", Model: "gemini-3.8-flash-low", Task: "FAKE:exit", AutoContinue: autoContinueOff()})
	wk := w.d.workerBySession(res.SessionID)
	if wk == nil {
		t.Fatal("the worker is gone before the idle limit")
	}
	// Wait for the real event: the fake process drains its turn and exits.
	// The exit must not end the worker, so the idle limit is what ends it.
	// Do not use a fixed deadline here: under load the daemon can take
	// seconds to send the notice, after the idle limit has already fired.
	eventually(t, "process exits after its turn", func() bool { return w.d.gone(wk) })
	n := w.waitNotice(t, 1)[0]
	if !strings.Contains(n.Body, "status: timed out") {
		t.Fatalf("notice: %s", n.Body)
	}
}

func TestAgyWorkerStreamFraming(t *testing.T) {
	c := &agyConn{initialized: make(chan string, 1)}
	_, _ = c.Write([]byte(`{"event":"init","conversation_`))
	select {
	case <-c.initialized:
		t.Fatal("accepted an incomplete event")
	default:
	}
	_, _ = c.Write([]byte("id\":\"conversation-one\"}\n"))
	select {
	case id := <-c.initialized:
		if id != "conversation-one" {
			t.Fatalf("ID: %s", id)
		}
	default:
		t.Fatal("lost a fragmented init event")
	}
	_, _ = c.Write(bytes.Repeat([]byte("x"), (16<<20)+1))
	if c.failure == "" || len(c.buffer) != 0 {
		t.Fatal("unbounded incomplete stream event")
	}
}

func TestAgyWorkerDrainsEventsBeforeExit(t *testing.T) {
	d := &Daemon{}
	exited := make(chan struct{})
	w := &worker{harness: "agy", pid: 1 << 30, exited: exited}
	if d.gone(w) {
		t.Fatal("reported exit before stdout events were drained")
	}
	close(exited)
	if !d.gone(w) {
		t.Fatal("did not report a drained process exit")
	}
}

func TestAgyWorkerStopKillsGroup(t *testing.T) {
	t.Parallel()
	w, dir, _ := startAgyNode(t, shortSet)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "agy", Model: "gemini-3.8-flash-low", Task: "FAKE:hang"})
	var r fakeAgyRecord
	eventually(t, "child started", func() bool {
		rs := agyRecords(dir)
		if len(rs) == 1 {
			r = rs[0]
		}
		return r.Child > 0
	})
	w.cli.mustCall(protocol.OpWorkerStop, protocol.WorkerStopRequest{Session: res.SessionID})
	eventually(t, "worker group stopped", func() bool { return !alive(r.PID) && !alive(r.Child) })
}

func TestAgyWorkerFailures(t *testing.T) {
	t.Parallel()
	for _, task := range []string{"FAKE:error", "FAKE:failed-result", "FAKE:timeout", "FAKE:quota"} {
		t.Run(task, func(t *testing.T) {
			w, _, _ := startAgyNode(t, shortSet)
			w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "agy", Model: "gemini-3.8-flash-low", Task: task, AutoContinue: autoContinueOff()})
			n := w.waitNotice(t, 1)[0]
			if !strings.Contains(n.Body, "status: failed") {
				t.Fatalf("notice: %s", n.Body)
			}
			if task == "FAKE:error" && !strings.Contains(n.Body, "AGY_ERROR") {
				t.Fatalf("error missing: %s", n.Body)
			}
			if task == "FAKE:quota" && !strings.Contains(n.Body, "Individual quota reached") {
				t.Fatalf("result error missing: %s", n.Body)
			}
		})
	}
}

func TestAgyWorkerDeniedNamesTool(t *testing.T) {
	t.Parallel()
	w, _, _ := startAgyNode(t, shortSet)
	w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "agy", Model: "gemini-3.8-flash-low", Task: "FAKE:denied", AutoContinue: autoContinueOff()})
	n := w.waitNotice(t, 1)[0]
	if !strings.Contains(n.Body, "status: failed") {
		t.Fatalf("notice: %s", n.Body)
	}
	for _, want := range []string{"Antigravity denied a tool action", "ReadFile", "path=/home/test/repo/.git/worktrees/w_test/HEAD", "run_command", "command=curl"} {
		if !strings.Contains(n.Body, want) {
			t.Errorf("notice lacks %q: %s", want, n.Body)
		}
	}
	if strings.Contains(n.Body, "sk-secretsecretsecretsecret") || !strings.Contains(n.Body, "[REDACTED]") {
		t.Errorf("secret not redacted: %s", n.Body)
	}
	logs := w.logs.lines("worker.agy.denied")
	if len(logs) != 1 || !strings.Contains(logs[0], "ReadFile") || !strings.Contains(logs[0], "run_command") || strings.Contains(logs[0], "sk-secretsecretsecretsecret") {
		t.Fatalf("denied log: %v", logs)
	}
}

func TestAgyDeniedText(t *testing.T) {
	t.Parallel()
	denied := []json.RawMessage{
		json.RawMessage(`"plain denial"`),
		json.RawMessage(`{"toolName":"write_file","arguments":{"file_path":"/tmp/a"}}`),
		json.RawMessage(`{"tool_name":"run_command","args":"go test ./..."}`),
		json.RawMessage(`{"tool_name":"ReadFile","args":{"Path":"/tmp/b"}}`),
	}
	got := agyDeniedText(denied)
	for _, want := range []string{"plain denial", "write_file path=/tmp/a", "run_command go test ./...", "and 1 more"} {
		if !strings.Contains(got, want) {
			t.Errorf("text lacks %q: %s", want, got)
		}
	}
}

func TestAgyWorkerGitDirsInLinkedWorktree(t *testing.T) {
	t.Parallel()
	w, dir, repo := startAgyNode(t, shortSet)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "agy", Model: "gemini-3.8-flash-low", Task: "FAKE:handback", Isolation: protocol.IsolationWorktree})
	w.waitNotice(t, 1)
	r := agyRecords(dir)[0]
	var got []string
	for i, arg := range r.Args {
		if arg == "--add-dir" && i+1 < len(r.Args) {
			got = append(got, r.Args[i+1])
		}
	}
	common := filepath.Join(repo, ".git")
	for _, want := range []string{filepath.Join(common, "worktrees", filepath.Base(res.Worktree)), filepath.Join(common, "objects"), filepath.Join(common, "refs"), filepath.Join(common, "logs")} {
		if !strings.Contains("\n"+strings.Join(got, "\n")+"\n", "\n"+want+"\n") {
			t.Fatalf("git folder %s missing from --add-dir: %v", want, got)
		}
	}
}

func TestAgyWorkerReadOnly(t *testing.T) {
	t.Parallel()
	w, dir, repo := startAgyNode(t, shortSet)
	setWorkersLevel(t, w, repo, protocol.WorkersReadOnly)
	w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "agy", Model: "gemini-3.8-flash-low", Task: "FAKE:handback"})
	w.waitNotice(t, 1)
	r := agyRecords(dir)[0]
	if argValue(r.Args, "--mode") != "plan" || argValue(r.Args, "--settings") != `{"permissions":{"allow":["mcp(agent-net_agent-net/*)"]}}` {
		t.Fatalf("read-only args: %v", r.Args)
	}
	for i, arg := range r.Args {
		if arg == "--add-dir" && i+1 < len(r.Args) && strings.HasPrefix(r.Args[i+1], filepath.Join(repo, ".git")) {
			t.Fatalf("read-only worker got a git folder: %v", r.Args[i+1])
		}
	}
}

// The same user cannot impersonate a worker conversation from another group.
func TestAgyWorkerMCPRejectsOutsideProcess(t *testing.T) {
	t.Parallel()
	w, dir, _ := startAgyNode(t, shortSet)
	w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "agy", Model: "gemini-3.8-flash-low", Task: "reply OK"})
	r := agyRecords(dir)[0]
	conn, err := net.Dial("unix", w.cfg.SocketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = json.NewEncoder(conn).Encode(map[string]any{"id": "x", "method": "hello", "params": map[string]any{"client": "mcp", "harness": "agy", "native_id": r.Native, "protocol": protocol.LocalProtocol}})
	var resp struct {
		Error json.RawMessage `json:"error"`
	}
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Error) == 0 || string(resp.Error) == "null" {
		t.Fatal("outside process accepted")
	}
}

// Keep the same harness process and request across daemon replacement.
func TestAgyWorkerRestart(t *testing.T) {
	t.Parallel()
	for _, afterHandback := range []bool{false, true} {
		t.Run(fmt.Sprint(afterHandback), func(t *testing.T) {
			set := shortSet
			set.Idle, set.IdleNotice = time.Hour, time.Hour
			w, dir, _ := startAgyNode(t, set)
			task := "FAKE:gate FAKE:handback"
			if afterHandback {
				task = "FAKE:handback FAKE:result-gate FAKE:exit"
			}
			res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "agy", Model: "gemini-3.8-flash-low", Task: task, AutoContinue: autoContinueOff()})
			wk := w.d.workerBySession(res.SessionID)
			eventually(t, "in-flight turn", func() bool {
				rs := agyRecords(dir)
				if len(rs) != 1 || len(rs[0].Turns) != 1 {
					return false
				}
				if !afterHandback {
					return true
				}
				wk.agy.mu.Lock()
				defer wk.agy.mu.Unlock()
				return wk.agy.handback != nil
			})
			pid := agyRecords(dir)[0].PID
			w.d.Stop()
			// In this case the result arrives while no daemon is reading.
			if afterHandback {
				if err := os.WriteFile(filepath.Join(dir, "result-release"), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if afterHandback {
				eventually(t, "result written during downtime", func() bool { return !alive(pid) })
			}
			w.node.restart()
			w.cli = cliClient(t, w.node)
			if !afterHandback {
				if err := os.WriteFile(filepath.Join(dir, "release"), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			n := w.waitNotice(t, 1)[0]
			for _, want := range []string{"status: done", "finished agy task", "1200 in, 340 out, 50 reasoning, 90 cache read"} {
				if !strings.Contains(n.Body, want) {
					t.Errorf("notice lacks %q: %s", want, n.Body)
				}
			}
			eventually(t, "worker stopped", func() bool { return w.d.workerCount() == 0 })
			rs := agyRecords(dir)
			if len(rs) != 1 || rs[0].PID != pid || rs[0].MCPError != "" {
				t.Fatalf("records: %+v", rs)
			}
			w.d.Stop()
			w.node.restart()
			w.cli = cliClient(t, w.node)
			if ns := w.notices(t); len(ns) != 1 {
				t.Fatalf("duplicate notices: %+v", ns)
			}
		})
	}
}

func TestAgyHandbackCommit(t *testing.T) {
	t.Parallel()
	w, _, repo := startAgyNode(t, shortSet)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "agy", Model: "gemini-3.8-flash-low", Title: "Finish agy work", Task: "FAKE:edit FAKE:handback", Isolation: protocol.IsolationWorktree})
	w.waitNotice(t, 1)
	msg := gitOutT(t, repo, "log", "-1", "--format=%B", res.Branch)
	if !strings.HasPrefix(msg, "Finish agy work\n") || !strings.Contains(msg, "Agent-Net-Harness: agy") || !strings.Contains(msg, "Agent-Net-Worker: "+res.SessionID) || !strings.Contains(msg, "Agent-Net-Model: gemini-3.8-flash-low") || !strings.Contains(msg, "Agent-Net-Caller:") || strings.Contains(strings.ToLower(msg), "co-authored-by:") {
		t.Fatalf("commit message: %s", msg)
	}
	if got := gitOutT(t, repo, "show", res.Branch+":agy-work.txt"); got != "work" {
		t.Fatal(got)
	}
}

func TestAgyHandbackCommitScope(t *testing.T) {
	t.Parallel()
	w, _, repo := startAgyNode(t, shortSet)
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("ignored.txt\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", ".gitignore")
	gitIn(t, repo, "commit", "-q", "-m", "ignore file")
	if err := os.WriteFile(filepath.Join(repo, "outside.txt"), []byte("outside\n"), 0644); err != nil {
		t.Fatal(err)
	}
	base := gitOutT(t, repo, "rev-parse", "HEAD")
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "agy", Model: "gemini-3.8-flash-low", Task: "FAKE:edit FAKE:ignored FAKE:handback", Isolation: protocol.IsolationWorktree})
	w.waitNotice(t, 1)
	if got := gitOutT(t, repo, "rev-list", "--count", base+".."+res.Branch); got != "1" {
		t.Fatalf("commit count: %s", got)
	}
	files := gitOutT(t, repo, "diff-tree", "--no-commit-id", "--name-only", "-r", res.Branch)
	if files != "agy-work.txt" {
		t.Fatalf("committed files: %q", files)
	}
	if got := gitOutT(t, repo, "rev-parse", "HEAD"); got != base {
		t.Fatal("owner branch changed")
	}
	if got, err := os.ReadFile(filepath.Join(repo, "outside.txt")); err != nil || string(got) != "outside\n" {
		t.Fatalf("outside file: %q %v", got, err)
	}
}

func TestAgyHandbackNoWorktreeCommit(t *testing.T) {
	t.Parallel()
	w, _, repo := startAgyNode(t, shortSet)
	base := gitOutT(t, repo, "rev-parse", "HEAD")
	w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "agy", Model: "gemini-3.8-flash-low", Task: "FAKE:edit FAKE:handback", Isolation: protocol.IsolationNone})
	w.waitNotice(t, 1)
	if got := gitOutT(t, repo, "rev-parse", "HEAD"); got != base {
		t.Fatal("owner branch changed")
	}
	if _, err := os.Stat(filepath.Join(repo, "agy-work.txt")); err != nil {
		t.Fatal(err)
	}
}

func TestAgyHandbackSelfCommit(t *testing.T) {
	t.Parallel()
	w, _, repo := startAgyNode(t, shortSet)
	base := gitOutT(t, repo, "rev-parse", "HEAD")
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "agy", Model: "gemini-3.8-flash-low", Task: "FAKE:edit FAKE:selfcommit FAKE:handback", Isolation: protocol.IsolationWorktree})
	w.waitNotice(t, 1)
	if got := gitOutT(t, repo, "rev-list", "--count", base+".."+res.Branch); got != "1" {
		t.Fatalf("commit count: %s", got)
	}
	if got := gitOutT(t, repo, "log", "-1", "--format=%s", res.Branch); got != "worker commit" {
		t.Fatalf("subject: %q", got)
	}
}

func TestAgyUnfinishedCommitUsesTitle(t *testing.T) {
	t.Parallel()
	w, _, repo := startAgyNode(t, shortSet)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "agy", Model: "gemini-3.8-flash-low", Title: "Finish unfinished work", Task: "FAKE:edit", Isolation: protocol.IsolationWorktree, AutoContinue: autoContinueOff()})
	eventually(t, "fake agy writes work", func() bool { _, err := os.Stat(filepath.Join(res.Worktree, "agy-work.txt")); return err == nil })
	w.cli.mustCall("worker_stop", protocol.WorkerStopRequest{Session: res.SessionID})
	notice := w.waitNotice(t, 1)[0]
	if !strings.Contains(notice.Body, "unfinished changes were saved as a commit on branch "+res.Branch) {
		t.Fatalf("notice: %s", notice.Body)
	}
	if got := gitOutT(t, repo, "log", "-1", "--format=%s", res.Branch); got != "Finish unfinished work" {
		t.Fatalf("subject: %s", got)
	}
}

// The result can arrive while the failure check observes process exit.
func TestAgyFailureAfterResultAtExit(t *testing.T) {
	c := &agyConn{}
	failed := agyFailedAfterExit(c, func() bool {
		c.mu.Lock()
		c.completed = true
		c.mu.Unlock()
		return true
	}, nil)
	if failed {
		t.Fatal("successful result was reported as a failed exit")
	}
}

func TestAgyWorkerHandbackFilesAcrossNodes(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "agy")
	script := "#!/bin/sh\nFAKE_AGY_RECORDS=" + shellQuote(dir) + " FAKE_AGY_PROCESS=1 exec " + shellQuote(os.Args[0]) + " -test.run '^TestAgyProcess$' -- \"$@\"\n"
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	g := startGateway(t, t.TempDir(), "")
	var failRoute atomic.Bool
	var routeTarget atomic.Value
	var routeCalls atomic.Int32
	w := startWorkerNodeWithDetect(t, g, shortSet, agyInventory, func(c *Config) {
		c.testGatewayCall = func(_ context.Context, typ string, p any) (json.RawMessage, bool, error) {
			if msg, ok := p.(protocol.MsgSend); typ == "msg.send" && ok && msg.WorkerNotice && len(msg.Files) > 0 {
				routeTarget.Store(msg.Recipient)
				failRoute.Store(true)
			}
			if q, ok := p.(protocol.QueryAgent); typ == "query.agent" && ok && failRoute.Load() && q.SessionID == routeTarget.Load().(string) {
				if routeCalls.Add(1) == 1 {
					return nil, true, fmt.Errorf("temporary route lookup failure")
				}
			}
			return nil, false, nil
		}
		c.HarnessPaths = map[string]string{"agy": bin}
		c.Files.TailscalePATH = t.TempDir()
		c.Files.TailscaleFallback = []string{}
	})
	w.register(t, newRepo(t, "shop"))
	remote := &fakeAdapter{harness: "fake"}
	b := startNode(t, g, nodeOpts{adapter: remote, cfg: func(c *Config) {
		c.Files.Home = t.TempDir()
		c.Files.TailscalePATH = t.TempDir()
		c.Files.TailscaleFallback = []string{}
		c.Files.Poll, c.Files.PollMax = 20*time.Millisecond, 50*time.Millisecond
	}})
	remote.setSessions(protocol.SessionInfo{Harness: "fake", NativeID: "caller", PID: 1, Cwd: t.TempDir(), Status: "idle"})
	var caller string
	eventually(t, "remote caller", func() bool { caller = remote.host.SessionID("fake", "caller"); return caller != "" })
	ctx, cancel := context.WithTimeout(context.Background(), scaled(30*time.Second))
	defer cancel()
	res, err := b.d.workerStartCall(ctx, caller, protocol.WorkerStartRequest{Project: "shop", Harness: "agy", Model: "gemini-3.8-flash-low", Task: "FAKE:handback FAKE:filehandback FAKE:result-gate", Isolation: protocol.IsolationWorktree}, false, "")
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "agy handback acknowledged", func() bool {
		wk := w.d.workerBySession(res.SessionID)
		if wk == nil {
			return false
		}
		w.d.wmu.Lock()
		c := wk.agy
		w.d.wmu.Unlock()
		if c == nil {
			return false
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.handback != nil
	})
	if err := os.WriteFile(filepath.Join(dir, "result-release"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	eventually(t, "remote caller receives agy file notice", func() bool {
		for _, m := range remote.delivered() {
			if m.From == res.SessionID && strings.Contains(m.Body, "finished agy task") {
				path := filepath.Join(b.cfg.StateDir, "inbox", caller, m.ID, "agy-result.txt")
				data, err := os.ReadFile(path)
				return err == nil && string(data) == "agy file\n"
			}
		}
		return false
	})
	if got := routeCalls.Load(); got < 2 {
		t.Fatalf("route lookup calls = %d, want a retry", got)
	}
}
