package daemon

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
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
				if !call("handback", protocol.OpWorkerHandback, protocol.WorkerHandbackRequest{Report: "finished agy task", Complete: &complete}) {
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
		write(map[string]any{"event": "result", "result": result})
		if strings.Contains(body, "FAKE:exit") {
			os.Exit(0)
		}
	}
	r.InputClosed = true
	save()
	os.Exit(0)
}

func startAgyNode(t *testing.T, set WorkerSettings) (*workerNode, string, string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("FAKE_AGY_RECORDS", dir)
	bin := filepath.Join(dir, "agy")
	script := "#!/bin/sh\nFAKE_AGY_PROCESS=1 exec '" + strings.ReplaceAll(os.Args[0], "'", "'\\''") + "' -test.run '^TestAgyProcess$' -- \"$@\"\n"
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

func TestAgyWorkerFollowupAndAutoContinue(t *testing.T) {
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
	w, dir, _ := startAgyNode(t, shortSet)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "agy", Model: "gemini-3.8-flash-low", Task: "reply OK", AutoContinue: autoContinueOff()})
	w.cli.mustCall(protocol.OpWorkerStop, protocol.WorkerStopRequest{Session: res.SessionID})
	eventually(t, "worker process stopped", func() bool { rs := agyRecords(dir); return len(rs) == 1 && !alive(rs[0].PID) })
}

func TestAgyWorkerHandbackKeepsQueuedFollowup(t *testing.T) {
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
	set := shortSet
	set.Idle = scaled(500 * time.Millisecond)
	set.IdleNotice = time.Hour
	w, _, _ := startAgyNode(t, set)
	w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "agy", Model: "gemini-3.8-flash-low", Task: "FAKE:exit", AutoContinue: autoContinueOff()})
	deadline := time.Now().Add(scaled(3 * time.Second))
	for time.Now().Before(deadline) {
		if ns := w.notices(t); len(ns) > 0 {
			if !strings.Contains(ns[0].Body, "status: timed out") {
				t.Fatalf("notice: %s", ns[0].Body)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("a process that exited after a successful turn bypassed the idle limit")
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

func TestAgyWorkerReadOnly(t *testing.T) {
	w, dir, repo := startAgyNode(t, shortSet)
	setWorkersLevel(t, w, repo, protocol.WorkersReadOnly)
	w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "agy", Model: "gemini-3.8-flash-low", Task: "FAKE:handback"})
	w.waitNotice(t, 1)
	r := agyRecords(dir)[0]
	if argValue(r.Args, "--mode") != "plan" || argValue(r.Args, "--settings") != `{"permissions":{"allow":["mcp(agent-net_agent-net/*)"]}}` {
		t.Fatalf("read-only args: %v", r.Args)
	}
}

// The same user cannot impersonate a worker conversation from another group.
func TestAgyWorkerMCPRejectsOutsideProcess(t *testing.T) {
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
