package daemon

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
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
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/niparis/agent-net/internal/protocol"
)

// Fake `claude`, `pi` and `codex` for the worker tests. Like the fake
// OpenCode, each is the test binary itself: the test sets one of these
// variables and points HarnessPaths at os.Args[0].
//
//	FAKE_CLAUDE_BIN=1   `claude --bg ...` and `claude stop <id>`
//	FAKE_PI_BIN=1       `pi --mode rpc ...`
//	FAKE_CODEX_BIN=1    `codex app-server --listen ws://...`
//
// Each writes a record file in its working folder (fake-claude.json,
// fake-pi.json, fake-codex.json) with the arguments and the environment it
// saw. The messages it gets decide what it does ("FAKE:..." in the body), as in
// the fake OpenCode.
const (
	envFakeClaude        = "FAKE_CLAUDE_BIN"
	envFakeClaudeSession = "FAKE_CLAUDE_SESSION" // set for the session process the fake `claude --bg` starts
	envFakeClaudeMCP     = "FAKE_CLAUDE_MCP"     // set for the fake `agent-net mcp` inside the session
	envFakePi            = "FAKE_PI_BIN"
	envFakeCodex         = "FAKE_CODEX_BIN"

	// Variables that change what the fakes do.
	envFakeClaudeSessions   = "FAKE_CLAUDE_SESSIONS"     // the folder of the session files
	envFakeClaudeLog        = "FAKE_CLAUDE_LOG"          // one line per `claude stop`
	envFakeClaudeSockets    = "FAKE_CLAUDE_SOCKETS"      // the folder of the inbox sockets
	envFakeStopIgnored      = "FAKE_CLAUDE_STOP_IGNORED" // `claude stop` does nothing
	envFakeIgnoreTerm       = "FAKE_IGNORE_TERM"
	envFakeStartFails       = "FAKE_START_FAILS" // the fake exits at once
	envFakeCodexNoApprovals = "FAKE_CODEX_NOAPPROVAL"
)

// runFakeHarness runs the fake that the environment asks for. It reports
// whether it did, in which case the test binary must stop.
func runFakeHarness() bool {
	switch {
	case os.Getenv(envFakeClaudeSession) != "":
		fakeClaudeSession()
	case os.Getenv(envFakeCodexShim) != "":
		fakeCodexShim()
	case os.Getenv(envFakeClaudeMCP) != "":
		fakeClaudeMCP()
	case os.Getenv(envFakeClaude) != "" && len(os.Args) > 1 && (os.Args[1] == "--bg" || os.Args[1] == "stop"):
		fakeClaudeCLI()
	case os.Getenv(envFakePi) != "" && len(os.Args) > 1 && os.Args[1] == "--mode":
		fakePi()
	case os.Getenv(envFakePi) != "" && len(os.Args) > 1 && os.Args[1] == "--list-models":
		fakePiModels()
	case os.Getenv(envFakeCodex) != "" && len(os.Args) > 1 && os.Args[1] == "app-server":
		fakeCodex()
	case os.Getenv(envFakeCodex) != "" && len(os.Args) > 2 && os.Args[1] == "debug" && os.Args[2] == "models":
		fakeCodexModels()
	default:
		return false
	}
	return true
}

func argValue(args []string, name string) string {
	for i, a := range args {
		if a == name && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func writeRecord(cwd, name string, rec any) {
	b, _ := json.Marshal(rec)
	os.WriteFile(filepath.Join(cwd, name), b, 0o644)
}

func readRecord(t *testing.T, dir, name string, rec any) {
	t.Helper()
	eventually(t, "the fake wrote "+name, func() bool {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		return err == nil && json.Unmarshal(raw, rec) == nil
	})
}

func randHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// ---- Claude ----

type fakeClaudeRecord struct {
	Args     []string          `json:"args"`
	Cwd      string            `json:"cwd"`
	Settings map[string]any    `json:"settings"`
	Env      map[string]string `json:"env"`
	Pid      int               `json:"pid"`
	Pgid     int               `json:"pgid"`
	ChildPid int               `json:"child_pid"`
	ID       string            `json:"id"`
	Session  string            `json:"session"`
}

type fakeClaudeInit struct {
	Args []string `json:"args"`
	Cwd  string   `json:"cwd"`
}

// fakeClaudeCLI is `claude --bg ...` and `claude stop <id>`.
func fakeClaudeCLI() {
	dir := os.Getenv(envFakeClaudeSessions)
	if os.Args[1] == "stop" {
		id := os.Args[2]
		if f, err := os.OpenFile(os.Getenv(envFakeClaudeLog), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			fmt.Fprintf(f, "stop %s\n", id)
			f.Close()
		}
		if os.Getenv(envFakeStopIgnored) != "" {
			fmt.Println("stopped", id)
			return
		}
		if sf, ok := findClaudeBg(dir, id); ok {
			syscall.Kill(sf.PID, syscall.SIGTERM)
			os.Remove(filepath.Join(dir, strconv.Itoa(sf.PID)+".json"))
		}
		fmt.Println("stopped", id)
		return
	}
	if os.Getenv(envFakeStartFails) != "" {
		fmt.Println("Workspace not trusted. Run `claude` in this folder once and accept the trust prompt, then retry.")
		os.Exit(1)
	}
	cwd, _ := os.Getwd()
	init, _ := json.Marshal(fakeClaudeInit{Args: os.Args[1:], Cwd: cwd})
	env := os.Environ()
	// The settings "env" reaches the session, as in the real Claude Code. The
	// environment of this command does not.
	var settings struct {
		Env map[string]string `json:"env"`
	}
	json.Unmarshal([]byte(argValue(os.Args, "--settings")), &settings)
	env = workerEnv(env, settings.Env)
	env = append(env, envFakeClaudeSession+"="+string(init))
	cmd := exec.Command(os.Args[0], "-test.run=XXX")
	cmd.Dir, cmd.Env = cwd, env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		fmt.Println("cannot start the session:", err)
		os.Exit(1)
	}
	spid := cmd.Process.Pid
	cmd.Process.Release()
	var sf claudeSessionFile
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if raw, err := os.ReadFile(filepath.Join(dir, strconv.Itoa(spid)+".json")); err == nil && json.Unmarshal(raw, &sf) == nil && sf.JobID != "" {
			fmt.Printf("backgrounded · %s · %s (idle - send a prompt to start)\n  claude attach %s    open in this terminal\n  claude stop %s      stop this session\n",
				sf.JobID, argValue(os.Args, "--name"), sf.JobID, sf.JobID)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	fmt.Println("the session did not start")
	os.Exit(1)
}

// fakeClaudeSession is the process of a background session: it writes the
// session file, listens on its inbox socket, and runs the fake MCP server as
// its child, like Claude Code does.
func fakeClaudeSession() {
	var in fakeClaudeInit
	json.Unmarshal([]byte(os.Getenv(envFakeClaudeSession)), &in)
	if os.Getenv(envFakeIgnoreTerm) != "" {
		signal.Ignore(syscall.SIGTERM)
	}
	dir := os.Getenv(envFakeClaudeSessions)
	id := randHex(4)
	session := id + "-0000-4000-8000-" + randHex(6)
	if resumed := argValue(in.Args, "--resume"); resumed != "" {
		session = resumed // the same conversation, a new background job
	}
	sock := filepath.Join(os.Getenv(envFakeClaudeSockets), "fc-"+id+".sock")
	os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		os.Exit(1)
	}
	defer os.Remove(sock)

	rec := fakeClaudeRecord{Args: in.Args, Cwd: in.Cwd, Pid: os.Getpid(), ID: id, Session: session, Env: map[string]string{}}
	rec.Pgid, _ = syscall.Getpgid(0)
	json.Unmarshal([]byte(argValue(in.Args, "--settings")), &rec.Settings)
	for _, k := range []string{"AGENT_NET_SOCKET", "AGENT_NET_WORKER", "FOO_FROM_COMMAND"} {
		rec.Env[k] = os.Getenv(k)
	}
	sleeper := exec.Command("sleep", "600")
	if sleeper.Start() == nil {
		rec.ChildPid = sleeper.Process.Pid
		go sleeper.Wait()
	}
	mcp := exec.Command(os.Args[0], "-test.run=XXX")
	mcp.Env = append(os.Environ(), envFakeClaudeMCP+"="+session)
	mcp.Env = workerEnv(mcp.Env, map[string]string{envFakeClaudeSession: ""})
	mcpIn, _ := mcp.StdinPipe()
	if mcp.Start() != nil {
		os.Exit(1)
	}
	go mcp.Wait()
	writeRecord(in.Cwd, "fake-claude.json", rec)
	sf := map[string]any{"pid": os.Getpid(), "sessionId": session, "jobId": id, "cwd": in.Cwd, "kind": "bg",
		"name": argValue(in.Args, "--name"), "status": "idle", "messagingSocketPath": sock}
	b, _ := json.Marshal(sf)
	tmp := filepath.Join(dir, "."+strconv.Itoa(os.Getpid())+".tmp")
	os.WriteFile(tmp, b, 0o600)
	os.Rename(tmp, filepath.Join(dir, strconv.Itoa(os.Getpid())+".json"))

	var mu sync.Mutex
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			defer c.Close()
			sc := bufio.NewScanner(c)
			sc.Buffer(make([]byte, 0, 1<<16), 1<<20)
			for sc.Scan() {
				mu.Lock()
				mcpIn.Write(append(append([]byte(nil), sc.Bytes()...), '\n'))
				mu.Unlock()
			}
		}()
	}
}

// fakeClaudeMCP is `agent-net mcp` inside the session: it says hello as the
// session (the daemon checks that its parent is the session process), and acts
// on the messages the session gets.
func fakeClaudeMCP() {
	session := os.Getenv(envFakeClaudeMCP)
	cwd, _ := os.Getwd()
	// Like the real MCP server, it dials again (and says hello again) when the
	// daemon goes away and comes back.
	var wmu sync.Mutex
	var c net.Conn
	next := 0
	hello := func() {
		next++
		b, _ := json.Marshal(map[string]any{"id": strconv.Itoa(next), "method": "hello", "params": map[string]any{
			"client": "mcp", "harness": "claude", "native_id": session, "cwd": cwd}})
		c.Write(append(b, '\n'))
	}
	dial := func() bool {
		conn, err := net.Dial("unix", os.Getenv("AGENT_NET_SOCKET"))
		if err != nil {
			return false
		}
		wmu.Lock()
		c = conn
		hello()
		wmu.Unlock()
		sc := bufio.NewScanner(conn)
		sc.Buffer(make([]byte, 0, 1<<16), 1<<20)
		for sc.Scan() { // answers from the daemon are not needed
		}
		return true
	}
	first := make(chan struct{})
	go func() {
		once := true
		for {
			if !dial() {
				time.Sleep(20 * time.Millisecond)
				continue
			}
			_ = once
			time.Sleep(20 * time.Millisecond)
		}
	}()
	go func() { time.Sleep(10 * time.Millisecond); close(first) }()
	<-first
	send := func(method string, params any) {
		wmu.Lock()
		defer wmu.Unlock()
		next++
		b, _ := json.Marshal(map[string]any{"id": strconv.Itoa(next), "method": method, "params": params})
		if c != nil {
			c.Write(append(b, '\n'))
		}
	}
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 1<<16), 1<<20)
	for in.Scan() {
		var m struct {
			Body string `json:"body"`
		}
		if json.Unmarshal(in.Bytes(), &m) != nil {
			continue
		}
		fakeAgentAct(send, m.Body, cwd)
	}
}

// fakeAgentAct is what the model of a worker does with a message, by the
// FAKE: commands in its body. send makes a call on the daemon socket.
func fakeAgentAct(send func(string, any), body, cwd string) {
	if strings.Contains(body, "FAKE:ignore") {
		return
	}
	if strings.Contains(body, "FAKE:edit") {
		fakeEdit(cwd)
	}
	status := ""
	if strings.Contains(body, "FAKE:blocked") {
		status = "blocked"
	}
	send("worker_handback", map[string]any{"report": "finished the task in " + cwd + " (task was " + strconv.Itoa(len(body)) + " bytes)", "status": status})
}

// fsClaudeAdapter is the Claude adapter of the tests: it reads the session
// files of a folder, and delivers by writing a line to the inbox socket of the
// session. The real adapter (package claude) does the same with Claude's frame.
type fsClaudeAdapter struct {
	dir string
}

func (a *fsClaudeAdapter) Harness() string { return "claude" }

func (a *fsClaudeAdapter) Start(ctx context.Context, h Host) error {
	go func() {
		t := time.NewTicker(20 * time.Millisecond)
		defer t.Stop()
		for {
			a.poll(h)
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
	return nil
}

func (a *fsClaudeAdapter) poll(h Host) {
	entries, _ := os.ReadDir(a.dir)
	var infos []protocol.SessionInfo
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(a.dir, e.Name()))
		if err != nil {
			continue
		}
		var sf struct {
			PID                 int    `json:"pid"`
			SessionID           string `json:"sessionId"`
			Cwd                 string `json:"cwd"`
			Name                string `json:"name"`
			Kind                string `json:"kind"`
			MessagingSocketPath string `json:"messagingSocketPath"`
		}
		if json.Unmarshal(raw, &sf) != nil || sf.SessionID == "" {
			continue
		}
		if _, alive := procToken(sf.PID); !alive {
			continue
		}
		mode := protocol.ModeForeground
		if sf.Kind == "bg" {
			mode = protocol.ModeBackground
		}
		infos = append(infos, protocol.SessionInfo{NativeID: sf.SessionID, PID: sf.PID, Cwd: sf.Cwd, Name: sf.Name,
			Status: "idle", Delivery: "native-claude", Mode: mode})
	}
	h.SetSessions("claude", infos)
}

func (a *fsClaudeAdapter) Deliver(ctx context.Context, target protocol.SessionInfo, m protocol.Message) Result {
	entries, _ := os.ReadDir(a.dir)
	for _, e := range entries {
		raw, err := os.ReadFile(filepath.Join(a.dir, e.Name()))
		var sf struct {
			SessionID           string `json:"sessionId"`
			MessagingSocketPath string `json:"messagingSocketPath"`
		}
		if err != nil || json.Unmarshal(raw, &sf) != nil || sf.SessionID != target.NativeID {
			continue
		}
		c, err := net.DialTimeout("unix", sf.MessagingSocketPath, time.Second)
		if err != nil {
			return Result{Detail: "inbox: " + err.Error()}
		}
		defer c.Close()
		b, _ := json.Marshal(map[string]string{"body": m.Body})
		c.Write(append(b, '\n'))
		return Result{Delivered: true}
	}
	return Result{Detail: "no such Claude session"}
}

// useFakeClaude points a test at the fake Claude and returns the folders it
// uses: the session files and the log of `claude stop`.
func useFakeClaude(t *testing.T) (sessions, stopLog string) {
	t.Helper()
	base := t.TempDir()
	sessions = filepath.Join(base, "sessions")
	if err := os.MkdirAll(sessions, 0o700); err != nil {
		t.Fatal(err)
	}
	stopLog = filepath.Join(base, "stops.log")
	t.Setenv(envFakeClaudeSockets, shortDir(t))
	// No fake session outlives its test, even when the test fails half way.
	t.Cleanup(func() {
		entries, _ := os.ReadDir(sessions)
		for _, e := range entries {
			var sf claudeSessionFile
			if raw, err := os.ReadFile(filepath.Join(sessions, e.Name())); err == nil && json.Unmarshal(raw, &sf) == nil && sf.PID > 1 {
				syscall.Kill(-sf.PID, syscall.SIGKILL)
			}
		}
	})
	t.Setenv(envFakeClaude, "1")
	t.Setenv(envFakeClaudeSessions, sessions)
	t.Setenv(envFakeClaudeLog, stopLog)
	return
}

// ---- Pi ----

type fakePiRecord struct {
	Args     []string          `json:"args"`
	Cwd      string            `json:"cwd"`
	Env      map[string]string `json:"env"`
	Pid      int               `json:"pid"`
	Pgid     int               `json:"pgid"`
	ChildPid int               `json:"child_pid"`
	Stdin    string            `json:"stdin"`
}

// fakePi is `pi --mode rpc`: it ends when its standard input closes, as Pi
// does, and the Pi extension runs inside it (a connection to the daemon).
func fakePi() {
	if os.Getenv(envFakeStartFails) != "" {
		fmt.Fprintln(os.Stderr, "Model not found: nope/none")
		os.Exit(2)
	}
	if os.Getenv(envFakeIgnoreTerm) != "" {
		signal.Ignore(syscall.SIGTERM)
	}
	cwd, _ := os.Getwd()
	rec := fakePiRecord{Args: os.Args[1:], Cwd: cwd, Pid: os.Getpid(), Env: map[string]string{}}
	rec.Pgid, _ = syscall.Getpgid(0)
	for _, k := range []string{"AGENT_NET_WORKER", "AGENT_NET_SOCKET"} {
		rec.Env[k] = os.Getenv(k)
	}
	if fi, err := os.Stdin.Stat(); err == nil {
		rec.Stdin = fi.Mode().String()
	}
	child := exec.Command("sleep", "600")
	if child.Start() == nil {
		rec.ChildPid = child.Process.Pid
		go child.Wait()
	}
	writeRecord(cwd, "fake-pi.json", rec)
	session := argValue(os.Args, "--session-id")
	fakePiSessionFile(session, cwd)
	go fakeOCPlugin("pi", session, cwd)
	// RPC mode ends when stdin closes. Its stdout is an event stream that the
	// daemon discards; the usage comes from the session file.
	in := bufio.NewReader(os.Stdin)
	for {
		if _, err := in.ReadByte(); err != nil {
			os.Exit(0)
		}
	}
}

// fakePiSessionFile writes the JSONL session Pi would write: a header and one
// assistant message with usage, under the folder named by
// PI_CODING_AGENT_SESSION_DIR.
func fakePiSessionFile(session, cwd string) {
	dir := os.Getenv("PI_CODING_AGENT_SESSION_DIR")
	if dir == "" {
		return
	}
	folder := "--" + strings.NewReplacer("/", "-", "\\", "-", ":", "-").Replace(strings.TrimPrefix(cwd, "/")) + "--"
	path := filepath.Join(dir, folder)
	if err := os.MkdirAll(path, 0o700); err != nil {
		return
	}
	lines := []string{
		`{"type":"session","version":3,"id":"` + session + `","cwd":"` + cwd + `"}`,
		`{"type":"message","id":"a1","parentId":null,"timestamp":"2026-10-30T10:00:00.000Z","message":{` +
			`"role":"assistant","provider":"openai","model":"gpt-4.1-nano","usage":{` +
			`"input":50000,"output":10000,"cacheRead":40000,"cacheWrite":5000,"totalTokens":105000,` +
			`"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}}}`,
	}
	name := strconv.FormatInt(time.Now().UnixNano(), 10) + "_" + session + ".jsonl"
	os.WriteFile(filepath.Join(path, name), []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}

// fakePiModels is `pi --list-models`: the table with the context window.
func fakePiModels() {
	fmt.Print("provider  model        context  max-out  thinking  images\n" +
		"openai    gpt-4.1-nano 200K     32.8K    no        yes\n")
}

// ---- Codex ----

type fakeCodexRecord struct {
	Args     []string          `json:"args"`
	Cwd      string            `json:"cwd"`
	Env      map[string]string `json:"env"`
	Pid      int               `json:"pid"`
	Pgid     int               `json:"pgid"`
	ChildPid int               `json:"child_pid"`
	Thread   map[string]any    `json:"thread"`
	Turns    []string          `json:"turns"`
	Answers  []any             `json:"answers"`
	Resumed  int               `json:"resumed"`
	// TurnParams are the params of each turn/start; ResumeParams those of the
	// last thread/resume.
	TurnParams   []map[string]any `json:"turn_params"`
	ResumeParams map[string]any   `json:"resume_params"`
}

// fakeCodexModels is `codex debug models`: the catalog that names the
// reasoning efforts of each model. gpt-fake takes low to high, not max.
func fakeCodexModels() {
	fmt.Print(`{"models":[{"slug":"gpt-fake","supported_reasoning_levels":[` +
		`{"effort":"low","description":"Low"},{"effort":"medium","description":"Medium"},{"effort":"high","description":"High"}]}]}`)
}

// fakeCodex is `codex app-server --listen ws://...`: a WebSocket server with
// the JSON-RPC of the real one, for the parts a worker uses. A turn is
// answered by what the text says: FAKE:approve asks for an approval first (and
// records the answer), FAKE:fail ends the turn as failed, FAKE:edit changes a
// file, FAKE:slow keeps the turn open until the connection ends.
func fakeCodex() {
	if os.Getenv(envFakeStartFails) != "" {
		fmt.Fprintln(os.Stderr, "codex: cannot start")
		os.Exit(2)
	}
	if os.Getenv(envFakeIgnoreTerm) != "" {
		signal.Ignore(syscall.SIGTERM)
	}
	cwd, _ := os.Getwd()
	rec := fakeCodexRecord{Args: os.Args[1:], Cwd: cwd, Pid: os.Getpid(), Env: map[string]string{}}
	rec.Pgid, _ = syscall.Getpgid(0)
	child := exec.Command("sleep", "600")
	if child.Start() == nil {
		rec.ChildPid = child.Process.Pid
		go child.Wait()
	}
	var mu sync.Mutex
	save := func() {
		mu.Lock()
		defer mu.Unlock()
		writeRecord(cwd, "fake-codex.json", rec)
	}
	save()
	listen := strings.TrimPrefix(argValue(os.Args, "--listen"), "ws://")
	tokenFile := argValue(os.Args, "--ws-token-file")
	tokenRaw, _ := os.ReadFile(tokenFile)
	token := strings.TrimSpace(string(tokenRaw))

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token || token == "" {
			http.Error(w, "unauthorized", 401)
			return
		}
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		var wmu sync.Mutex
		write := func(v any) {
			b, _ := json.Marshal(v)
			wmu.Lock()
			defer wmu.Unlock()
			c.Write(r.Context(), websocket.MessageText, b)
		}
		answers := make(chan json.RawMessage, 4)
		thread := ""
		for {
			_, data, err := c.Read(r.Context())
			if err != nil {
				return
			}
			var m codexMsg
			if json.Unmarshal(data, &m) != nil {
				continue
			}
			if m.Method == "" { // an answer to a request of ours
				if len(m.Result) > 0 {
					answers <- m.Result
				} else {
					answers <- json.RawMessage(`{"error":true}`)
				}
				continue
			}
			var p map[string]any
			json.Unmarshal(m.Params, &p)
			switch m.Method {
			case "initialize":
				write(map[string]any{"id": m.ID, "result": map[string]any{"userAgent": "fake", "codexHome": "/x"}})
			case "initialized":
			case "thread/start":
				thread = "th-" + randHex(4)
				mu.Lock()
				rec.Thread = p
				mu.Unlock()
				save()
				write(map[string]any{"id": m.ID, "result": map[string]any{"thread": map[string]any{"id": thread, "model": "fake-model"}, "model": "fake-model"}})
			case "thread/resume":
				thread, _ = p["threadId"].(string)
				mu.Lock()
				rec.Resumed++
				rec.ResumeParams = p
				mu.Unlock()
				save()
				write(map[string]any{"id": m.ID, "result": map[string]any{"thread": map[string]any{"id": thread}}})
			case "turn/start":
				text := ""
				if in, ok := p["input"].([]any); ok && len(in) > 0 {
					text, _ = in[0].(map[string]any)["text"].(string)
				}
				mu.Lock()
				rec.Turns = append(rec.Turns, text)
				rec.TurnParams = append(rec.TurnParams, p)
				mu.Unlock()
				save()
				turn := "turn-" + randHex(3)
				write(map[string]any{"id": m.ID, "result": map[string]any{"turn": map[string]any{"id": turn, "status": "inProgress"}}})
				go func() {
					write(map[string]any{"method": "turn/started", "params": map[string]any{"threadId": thread, "turn": map[string]any{"id": turn, "status": "inProgress"}}})
					if strings.Contains(text, "FAKE:slow") {
						<-r.Context().Done()
						return
					}
					if strings.Contains(text, "FAKE:edit") {
						fakeEdit(cwd)
					}
					if strings.Contains(text, "FAKE:approve") {
						write(map[string]any{"id": "srv-1", "method": "item/commandExecution/requestApproval",
							"params": map[string]any{"threadId": thread, "turnId": turn, "itemId": "i1", "command": "rm -rf build", "reason": "outside the sandbox"}})
						select {
						case a := <-answers:
							var v any
							json.Unmarshal(a, &v)
							mu.Lock()
							rec.Answers = append(rec.Answers, v)
							mu.Unlock()
							save()
						case <-time.After(3 * time.Second):
						}
					}
					status, errObj := "completed", any(nil)
					if strings.Contains(text, "FAKE:fail") {
						status, errObj = "failed", map[string]any{"message": "model overloaded"}
					}
					write(map[string]any{"method": "item/completed", "params": map[string]any{"threadId": thread, "turnId": turn,
						"item": map[string]any{"type": "agentMessage", "id": "m1", "phase": "final_answer", "text": "codex done in " + cwd + " (" + strconv.Itoa(len(text)) + " bytes)"}}})
					write(map[string]any{"method": "thread/tokenUsage/updated", "params": map[string]any{"threadId": thread, "turnId": turn,
						"tokenUsage": map[string]any{
							"last":               map[string]any{"totalTokens": 1700, "inputTokens": 1500, "cachedInputTokens": 1000, "cacheWriteInputTokens": 0, "outputTokens": 200, "reasoningOutputTokens": 0},
							"total":              map[string]any{"totalTokens": 1700, "inputTokens": 1500, "cachedInputTokens": 1000, "cacheWriteInputTokens": 0, "outputTokens": 200, "reasoningOutputTokens": 0},
							"modelContextWindow": 200000,
						}}})
					write(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": thread,
						"turn": map[string]any{"id": turn, "status": status, "error": errObj}}})
				}()
			default:
				write(map[string]any{"id": m.ID, "error": map[string]any{"code": -32601, "message": "unknown " + m.Method}})
			}
		}
	})
	if err := http.ListenAndServe(listen, handler); err != nil {
		fmt.Fprintln(os.Stderr, "fake codex:", err)
		os.Exit(1)
	}
}
