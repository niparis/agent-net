package daemon

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
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
	envFakeOpenCode      = "FAKE_OPENCODE_BIN"
	envFakeOpenCodeReply = "FAKE_OPENCODE_REPLY"
	envFakeCodexExec     = "FAKE_CODEX_EXEC_DIR" // canned one-shot replies
	envFakeCodexExecHang = "FAKE_CODEX_EXEC_HANG"

	// envFakeSocketProbe makes the test binary dial AGENT_NET_SOCKET inside a
	// sandbox, and write the result for a test that restarts the daemon socket
	// under it. READY is written when it waits, GATE releases it, OUT gets the
	// result. All three are paths inside the sandbox's private /tmp.
	envFakeSocketProbe      = "FAKE_SOCKET_PROBE"
	envFakeSocketProbeReady = "FAKE_SOCKET_PROBE_READY"
	envFakeSocketProbeGate  = "FAKE_SOCKET_PROBE_GATE"
	envFakeSocketProbeOut   = "FAKE_SOCKET_PROBE_OUT"

	// envFakeFenceProbe makes the test binary try, inside a worker sandbox,
	// what decision 0197 allows and the one write it forbids, and write the
	// results as JSON to $TMPDIR/fence.json. envFakeFenceHome names the
	// folder outside the sandbox's writable paths.
	envFakeFenceProbe = "FAKE_FENCE_PROBE"
	envFakeFenceHome  = "FAKE_FENCE_HOME"

	// Variables that change what the fakes do.
	envFakeClaudeSessions = "FAKE_CLAUDE_SESSIONS"     // the folder of the session files
	envFakeClaudeLog      = "FAKE_CLAUDE_LOG"          // one line per `claude stop`
	envFakeClaudeSockets  = "FAKE_CLAUDE_SOCKETS"      // the folder of the inbox sockets
	envFakeStopIgnored    = "FAKE_CLAUDE_STOP_IGNORED" // `claude stop` does nothing
	envFakeIgnoreTerm     = "FAKE_IGNORE_TERM"
	envFakeStartFails     = "FAKE_START_FAILS" // the fake exits at once
	// envFakeResumeSettleGate makes a resume write its session file first
	// under a fresh conversation ID, and switch to the resumed one only when
	// this file exists.
	envFakeResumeSettleGate = "FAKE_CLAUDE_RESUME_SETTLE_GATE"
	envFakeCodexNoApprovals = "FAKE_CODEX_NOAPPROVAL"
	// envFakeClaudeTranscribe makes the session write each prompt it takes
	// up (the first message and each inbox line) to its transcript as a user
	// turn, as Claude Code does.
	envFakeClaudeTranscribe = "FAKE_CLAUDE_TRANSCRIBE"
	// envFakeClaudeDropFile names a file that holds a count. While it is
	// above zero, the session accepts an inbox line and never takes it up,
	// and counts down.
	envFakeClaudeDropFile = "FAKE_CLAUDE_DROP_FILE"
	// envFakeClaudeReturnGate makes `claude --bg` wait, once the session is
	// up, until this file exists before it names the job and returns. The
	// session runs meanwhile, as a real one runs its first prompt.
	envFakeClaudeReturnGate = "FAKE_CLAUDE_RETURN_GATE"
	// envFakeClaudeNoInbox runs the session with no inbox socket, for tests
	// that never deliver to it.
	envFakeClaudeNoInbox = "FAKE_CLAUDE_NO_INBOX"
	// envFakeHangReads names a file. While it exists, the fake Codex never
	// answers thread/read and the fake OpenCode never answers a message list,
	// like a harness that hangs.
	envFakeHangReads = "FAKE_HANG_READS"
)

// fakeReadsHang says whether the test asked the fake to stop answering reads.
func fakeReadsHang() bool {
	path := os.Getenv(envFakeHangReads)
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// runFakeHarness runs the fake that the environment asks for. It reports
// whether it did, in which case the test binary must stop.
func runFakeHarness() bool {
	switch {
	case os.Getenv(envFakeSocketProbe) != "":
		fakeSocketProbe()
	case os.Getenv(envFakeFenceProbe) != "":
		fakeFenceProbe()
	case os.Getenv(envFakeClaudeSession) != "":
		fakeClaudeSession()
	case os.Getenv(envFakeCodexShim) != "":
		fakeCodexShim()
	case os.Getenv(envFakeClaudeMCP) != "":
		fakeClaudeMCP()
	case os.Getenv(envFakeClaude) != "" && len(os.Args) > 1 && (os.Args[1] == "--bg" || os.Args[1] == "agents" || os.Args[1] == "stop" || os.Args[1] == "--help" || os.Args[1] == "--version"):
		fakeClaudeCLI()
	case os.Getenv(envFakePi) != "" && len(os.Args) > 1 && os.Args[1] == "--mode":
		fakePi()
	case os.Getenv(envFakePi) != "" && len(os.Args) > 1 && os.Args[1] == "--list-models":
		fakePiModels()
	case os.Getenv(envFakeCodex) != "" && len(os.Args) > 1 && os.Args[1] == "app-server":
		fakeCodex()
	case os.Getenv(envFakeCodex) != "" && len(os.Args) > 1 && os.Args[1] == "exec":
		fakeCodexExec()
	case os.Getenv(envFakeOpenCode) != "" && len(os.Args) > 2 && os.Args[1] == "run" && os.Args[2] == "--help":
		// opencode 2.3.0: the effort rides on the model, so there is no
		// --variant flag to list.
		fmt.Println("Usage: opencode run [message..]")
		fmt.Println("  -m, --model string  provider/model#variant")
	case os.Getenv(envFakeOpenCode) != "" && len(os.Args) > 2 && os.Args[1] == "run":
		fakeOpenCodeRun()
	case os.Getenv(envFakeOpenCode) != "" && len(os.Args) > 2 && os.Args[1] == "session":
		fakeOpenCodeSession()
	case os.Getenv(envFakeCodex) != "" && len(os.Args) > 2 && os.Args[1] == "debug" && os.Args[2] == "models":
		fakeCodexModels()
	default:
		return false
	}
	return true
}

// fakeSocketProbe dials the worker's AGENT_NET_SOCKET and writes the result.
// A test releases it with a file after it replaced the daemon socket, so the
// dial is the first thing the probe does after the restart.
func fakeSocketProbe() {
	if ready := os.Getenv(envFakeSocketProbeReady); ready != "" {
		_ = os.WriteFile(ready, []byte("ready"), 0o600)
	}
	gate := os.Getenv(envFakeSocketProbeGate)
	for {
		if _, err := os.Stat(gate); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	result := "connected"
	var err error
	for i := 0; i < 100; i++ {
		var c net.Conn
		if c, err = net.DialTimeout("unix", os.Getenv("AGENT_NET_SOCKET"), time.Second); err == nil {
			c.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		result = err.Error()
	}
	_ = os.WriteFile(os.Getenv(envFakeSocketProbeOut), []byte(result), 0o600)
}

// fenceProbe is what fakeFenceProbe found: "" for each step that worked, else
// the error.
type fenceProbe struct {
	Commit     string `json:"commit"`
	SwitchC    string `json:"switch_c"`
	TmpWrite   string `json:"tmp_write"`
	UnixSocket string `json:"unix_socket"`
	Loopback   string `json:"loopback"`
	HomeWrite  string `json:"home_write"`
}

// fakeFenceProbe tries the writes and sockets of decision 0197 in its working
// folder, the worker's worktree.
func fakeFenceProbe() {
	tmp := os.Getenv("TMPDIR")
	git := func(args ...string) string {
		out, err := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...).CombinedOutput()
		if err != nil {
			return err.Error() + ": " + strings.TrimSpace(string(out))
		}
		return ""
	}
	var p fenceProbe
	p.Commit = errText(os.WriteFile("probe.txt", []byte("x"), 0o600))
	if p.Commit == "" {
		if p.Commit = git("add", "probe.txt"); p.Commit == "" {
			p.Commit = git("commit", "-qm", "probe")
		}
	}
	p.SwitchC = git("switch", "-q", "-c", "fence-probe")
	p.TmpWrite = errText(os.WriteFile(filepath.Join(tmp, "probe"), []byte("x"), 0o600))
	p.UnixSocket = fenceDial("unix", filepath.Join(tmp, "p.sock"))
	p.Loopback = fenceDial("tcp", "127.0.0.1:0")
	p.HomeWrite = errText(os.WriteFile(filepath.Join(os.Getenv(envFakeFenceHome), "probe"), []byte("x"), 0o600))
	raw, _ := json.Marshal(p)
	_ = os.WriteFile(filepath.Join(tmp, "fence.json"), raw, 0o600)
}

// fenceDial listens on addr and connects to it: "" when both work.
func fenceDial(network, addr string) string {
	ln, err := net.Listen(network, addr)
	if err != nil {
		return err.Error()
	}
	defer ln.Close()
	go func() {
		if c, err := ln.Accept(); err == nil {
			c.Close()
		}
	}()
	c, err := net.DialTimeout(network, ln.Addr().String(), 5*time.Second)
	if err != nil {
		return err.Error()
	}
	return errText(c.Close())
}

// fakeOpenCodeRun is the one-shot OpenCode command for difficulty judges.
func fakeOpenCodeRun() {
	if os.Getenv("FAKE_OPENCODE_SAVE_SESSION") != "" {
		wd, _ := os.Getwd()
		raw, _ := json.Marshal([]map[string]string{{"id": "ses_judge", "directory": wd}})
		_ = os.WriteFile(os.Getenv("FAKE_OPENCODE_SESSIONS"), raw, 0600)
	}
	if path := os.Getenv("FAKE_OPENCODE_RUN_RECORD"); path != "" {
		socket := os.Getenv("AGENT_NET_SOCKET")
		_, err := os.Stat(socket)
		wd, _ := os.Getwd()
		rec := struct {
			Args         []string `json:"args"`
			Socket       string   `json:"socket"`
			SocketExists bool     `json:"socket_exists"`
			PWD          string   `json:"pwd"`
			Dir          string   `json:"dir"`
		}{os.Args[1:], socket, err == nil, os.Getenv("PWD"), wd}
		raw, _ := json.Marshal(rec)
		_ = os.WriteFile(path, raw, 0600)
	}
	if marker := os.Getenv("FAKE_DIFFICULTY_MARKER"); marker != "" {
		_ = os.WriteFile(marker, []byte("started"), 0600)
	}
	if gate := os.Getenv("FAKE_DIFFICULTY_GATE"); gate != "" {
		for {
			if _, err := os.Stat(gate); err == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	if path := os.Getenv("FAKE_OPENCODE_PROMPT_FILE"); path != "" && len(os.Args) > 3 {
		_ = os.WriteFile(path, []byte(os.Args[len(os.Args)-1]), 0o600)
	}
	fmt.Println("> build · deepseek-v4.1-flash")
	fmt.Print("\x1b[32m")
	if path := os.Getenv(envFakeOpenCodeReply); path != "" {
		if raw, err := os.ReadFile(path); err == nil {
			fmt.Print(string(raw))
		}
	}
	fmt.Print("\x1b[0m\n")
	os.Exit(0)
}

// fakeOpenCodeSession lists the sessions in FAKE_OPENCODE_SESSIONS (a JSON
// file, with "{dir}" standing for the current folder) and appends each
// deleted ID to FAKE_OPENCODE_DELETED.
func fakeOpenCodeSession() {
	switch os.Args[2] {
	case "list":
		raw := []byte("[]")
		if path := os.Getenv("FAKE_OPENCODE_SESSIONS"); path != "" {
			if b, err := os.ReadFile(path); err == nil {
				wd, _ := os.Getwd()
				raw = []byte(strings.ReplaceAll(string(b), "{dir}", wd))
			}
		}
		fmt.Println(string(raw))
	case "delete":
		if path := os.Getenv("FAKE_OPENCODE_DELETED"); path != "" && len(os.Args) > 3 {
			f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
			if err == nil {
				fmt.Fprintln(f, os.Args[3])
				f.Close()
			}
		}
	}
	os.Exit(0)
}

func argValue(args []string, name string) string {
	for i, a := range args {
		if a == name && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func isSourceDir(dir string) bool {
	if dir == "" {
		return true
	}
	if _, err := os.Stat(filepath.Join(dir, "fakeharnesses_test.go")); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(dir, "daemon.go")); err == nil {
		return true
	}
	return false
}

func fakeHarnessDir(cwd, envHome string) string {
	if !isSourceDir(cwd) && cwd != "" {
		return cwd
	}
	if envHome != "" {
		if home := os.Getenv(envHome); home != "" {
			if f, err := os.CreateTemp(home, ".probe*"); err == nil {
				name := f.Name()
				f.Close()
				os.Remove(name)
				return home
			}
		}
	}
	if pids := os.Getenv(envFakePids); pids != "" {
		return pids
	}
	return os.TempDir()
}

func writeRecord(dir, name string, rec any) {
	if isSourceDir(dir) {
		dir = fakeHarnessDir(dir, "")
	}
	b, _ := json.Marshal(rec)
	os.WriteFile(filepath.Join(dir, name), b, 0o644)
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
	MCPClient  string            `json:"mcp_client"`
	Args       []string          `json:"args"`
	Cwd        string            `json:"cwd"`
	Settings   map[string]any    `json:"settings"`
	Env        map[string]string `json:"env"`
	CommandEnv map[string]string `json:"command_env"`
	Pid        int               `json:"pid"`
	Pgid       int               `json:"pgid"`
	ChildPid   int               `json:"child_pid"`
	ID         string            `json:"id"`
	Session    string            `json:"session"`
}

type fakeClaudeInit struct {
	Args []string          `json:"args"`
	Cwd  string            `json:"cwd"`
	Env  map[string]string `json:"env"`
}

// envFakeMCPBlocked makes the fake `claude --bg` leave a job blocked on the MCP
// approval prompt, as Claude Code 2.1.319 does.
const envFakeMCPBlocked = "FAKE_CLAUDE_MCP_BLOCKED"

// envFakeHangAfterBackground makes the fake `claude --bg` print that the job
// is backgrounded and then wait, as a client does when the daemon is cut off.
// Its value is a file that the fake creates once it has printed.
const envFakeHangAfterBackground = "FAKE_CLAUDE_HANG_AFTER_BG"

// fakeClaudeCLI is `claude --bg ...` and `claude stop <id>`.
func fakeClaudeCLI() {
	dir := os.Getenv(envFakeClaudeSessions)
	if os.Args[1] == "agents" {
		if f, err := os.OpenFile(os.Getenv("FAKE_CLAUDE_AGENTS_LOG"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600); err == nil {
			fmt.Fprintln(f, strings.Join(os.Args[1:], " "))
			f.Close()
		}
		raw, err := os.ReadFile(os.Getenv("FAKE_CLAUDE_AGENTS_FILE"))
		if err != nil {
			os.Exit(1)
		}
		fmt.Print(string(raw))
		return
	}
	if os.Args[1] == "--help" {
		if os.Getenv("FAKE_CLAUDE_NO_TOOLS_FLAG") != "" {
			fmt.Println("--bg --strict-mcp-config")
		} else {
			fmt.Println("--bg --strict-mcp-config --tools --setting-sources")
		}
		return
	}
	if os.Args[1] == "--version" {
		fmt.Println("2.1.289")
		return
	}
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
	if os.Getenv(envFakeMCPBlocked) != "" {
		if pending := fakeClaudeMCPPending(); len(pending) > 0 {
			// Claude Code stops on "new MCP server needs approval": the job
			// exists and is alive, and it never writes a session file.
			id := "blockedjob"
			job := filepath.Join(filepath.Dir(dir), "jobs", id)
			os.MkdirAll(job, 0700)
			detail := "1 new MCP server needs approval"
			if len(pending) > 1 {
				detail = fmt.Sprintf("%d new MCP servers need approval", len(pending))
			}
			body, _ := json.Marshal(map[string]string{"state": "blocked", "detail": detail})
			os.WriteFile(filepath.Join(job, "state.json"), body, 0600)
			fmt.Printf("backgrounded · %s · %s\n  claude stop %s      stop this session\n", id, argValue(os.Args, "--name"), id)
			return
		}
	}
	if mode := os.Getenv("FAKE_CLAUDE_RESUME_FAILURE"); mode != "" && argValue(os.Args, "--resume") != "" {
		if mode == "cli-missing" {
			fmt.Fprintln(os.Stderr, "source session not found")
			os.Exit(1)
		}
		id := "failed-copy"
		job := filepath.Join(filepath.Dir(dir), "jobs", id)
		os.MkdirAll(job, 0700)
		detail := "source session not found"
		if mode == "crash" {
			detail = "worker crashed"
		} else if mode == "other" {
			detail = "permission denied"
		}
		body, _ := json.Marshal(map[string]string{"state": "failed", "detail": detail})
		os.WriteFile(filepath.Join(job, "state.json"), body, 0600)
		fmt.Println("claude stop", id)
		return
	}
	cwd, _ := os.Getwd()
	init, _ := json.Marshal(fakeClaudeInit{Args: os.Args[1:], Cwd: cwd, Env: map[string]string{
		"CLAUDE_CODE_DISABLE_MOUSE": os.Getenv("CLAUDE_CODE_DISABLE_MOUSE"),
		"DISABLE_AUTOUPDATER":       os.Getenv("DISABLE_AUTOUPDATER")}})
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
	exited := make(chan struct{})
	go func() {
		cmd.Wait()
		close(exited)
	}()
	var sf claudeSessionFile
	// The daemon owns the start deadline, so the fake must not give up on a
	// loaded machine before the daemon does. It fails here only when the
	// session process itself is gone without writing its file.
	deadline := time.Now().Add(scaled(2 * time.Minute))
	sawExit := false
	for time.Now().Before(deadline) {
		if raw, err := os.ReadFile(filepath.Join(dir, strconv.Itoa(spid)+".json")); err == nil && json.Unmarshal(raw, &sf) == nil && (sf.JobID != "" || os.Getenv("FAKE_CLAUDE_NO_JOB_ID") != "" && len(sf.SessionID) >= 8) {
			job := sf.JobID
			if job == "" {
				job = sf.SessionID[:8]
			}
			if gate := os.Getenv(envFakeClaudeReturnGate); gate != "" {
				for deadline := time.Now().Add(scaled(30 * time.Second)); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
					if _, err := os.Stat(gate); err == nil {
						break
					}
				}
			}

			fmt.Printf("backgrounded · %s · %s (idle - send a prompt to start)\n  claude attach %s    open in this terminal\n  claude stop %s      stop this session\n",
				job, argValue(os.Args, "--name"), job, job)
			if marker := os.Getenv(envFakeHangAfterBackground); marker != "" {
				// The job is up, but the client does not return, so a
				// shutdown kills the client and leaves the job behind. The
				// marker file says the output was written.
				os.WriteFile(marker, nil, 0o600)
				<-time.After(time.Hour)
			}
			return
		}
		select {
		case <-exited:
			if sawExit {
				fmt.Println("the session did not start")
				os.Exit(1)
			}
			// The session may have written its file just before it exited.
			sawExit = true
		case <-time.After(10 * time.Millisecond):
		}
	}
	fmt.Println("the session did not start")
	os.Exit(1)
}

// fakeClaudeMCPPending lists the .mcp.json servers of the launch folder that
// have no answer, the way Claude Code decides to block a background job. The
// answer comes from the launch --settings JSON and from the folder's
// .claude/settings.local.json, and enableAllProjectMcpServers approves the rest
// (checked on Claude Code 2.1.319 with `claude --bg`).
func fakeClaudeMCPPending() []string {
	cwd, _ := os.Getwd()
	servers := claudeMCPServers(cwd)
	if len(servers) == 0 {
		return nil
	}
	enabled, disabled := map[string]bool{}, map[string]bool{}
	enableAll := false
	take := func(list []string, into map[string]bool) {
		for _, name := range list {
			into[name] = true
		}
	}
	if doc, err := readClaudeLocalSettings(claudeLocalSettingsFile(cwd)); err == nil {
		yes, _ := claudeSettingsList(doc, claudeMCPEnabledKey)
		no, _ := claudeSettingsList(doc, claudeMCPDisabledKey)
		take(yes, enabled)
		take(no, disabled)
		if raw, ok := doc[claudeEnableAllKey]; ok {
			var on bool
			if json.Unmarshal(raw, &on) == nil && on {
				enableAll = true
			}
		}
	}
	var inline struct {
		Enabled   []string `json:"enabledMcpjsonServers"`
		Disabled  []string `json:"disabledMcpjsonServers"`
		EnableAll bool     `json:"enableAllProjectMcpServers"`
	}
	if json.Unmarshal([]byte(argValue(os.Args, "--settings")), &inline) == nil {
		take(inline.Enabled, enabled)
		take(inline.Disabled, disabled)
		enableAll = enableAll || inline.EnableAll
	}
	var pending []string
	for _, s := range servers {
		if disabled[s.Name] || enabled[s.Name] || enableAll {
			continue
		}
		pending = append(pending, s.Name)
	}
	return pending
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
	if resumed := argValue(in.Args, "--resume"); resumed != "" && os.Getenv("FAKE_CLAUDE_NEW_RESUME_ID") == "" {
		session = resumed // the same conversation, a new background job
	}
	// A test can name the copy's conversation, so a record with that native
	// ID can stand for a new conversation a harness started first.
	if copyID := os.Getenv("FAKE_CLAUDE_COPY_ID"); copyID != "" && argValue(in.Args, "--resume") != "" {
		session = copyID
	}
	sock := filepath.Join(os.Getenv(envFakeClaudeSockets), "fc-"+id+".sock")
	var ln net.Listener
	if os.Getenv(envFakeClaudeNoInbox) == "" {
		os.Remove(sock)
		var err error
		if ln, err = net.Listen("unix", sock); err != nil {
			os.Exit(1)
		}
		defer os.Remove(sock)
	}
	if gate := os.Getenv(envFakeResumeSettleGate); gate != "" && argValue(in.Args, "--resume") != "" {
		// A cold `claude --bg --resume` names the job after the resumed
		// conversation, but its first session file shows a fresh
		// conversation ID until the transcript is loaded (Claude Code
		// 2.1.322, seen on 2026-11-12 right after its daemon started).
		id = session[:8]
		writeFakeClaudeSessionFile(dir, map[string]any{"pid": os.Getpid(), "sessionId": randHex(4) + "-0000-4000-8000-" + randHex(6),
			"jobId": id, "cwd": in.Cwd, "kind": "bg", "status": "idle", "messagingSocketPath": sock})
		for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if _, err := os.Stat(gate); err == nil {
				break
			}
		}
	}

	rec := fakeClaudeRecord{Args: in.Args, Cwd: in.Cwd, CommandEnv: in.Env, Pid: os.Getpid(), ID: id, Session: session, Env: map[string]string{}}
	rec.Pgid, _ = syscall.Getpgid(0)
	json.Unmarshal([]byte(argValue(in.Args, "--settings")), &rec.Settings)
	for _, k := range []string{"AGENT_NET_SOCKET", "AGENT_NET_WORKER", "FOO_FROM_COMMAND", "CLAUDE_CODE_DISABLE_MOUSE", "DISABLE_AUTOUPDATER"} {
		rec.Env[k] = os.Getenv(k)
	}
	sleeper := exec.Command("sleep", "600")
	if sleeper.Start() == nil {
		rec.ChildPid = sleeper.Process.Pid
		go sleeper.Wait()
	}
	rec.MCPClient = "mcp"
	if strings.Contains(argValue(in.Args, "--mcp-config"), `"agent-net-cto"`) {
		rec.MCPClient = "cto-mcp"
	}
	mcp := exec.Command(os.Args[0], "-test.run=XXX")
	mcp.Env = append(os.Environ(), envFakeClaudeMCP+"="+session, "FAKE_CLAUDE_MCP_CLIENT="+rec.MCPClient)
	mcp.Env = workerEnv(mcp.Env, map[string]string{envFakeClaudeSession: ""})
	mcpIn, _ := mcp.StdinPipe()
	if mcp.Start() != nil {
		os.Exit(1)
	}
	go mcp.Wait()
	writeRecord(in.Cwd, "fake-claude.json", rec)
	if argValue(in.Args, "--resume") != "" {
		// A worker is stopped right after its hand-back, before its turn ends.
		// Claude Code (2.1.318) closes that turn when the session is resumed:
		// it writes this placeholder to the transcript, seen on a real worker.
		fakeClaudeTranscriptLine(dir, in.Cwd, session, fakeClaudeNoResponseLine)
	}
	sf := map[string]any{"pid": os.Getpid(), "sessionId": session, "jobId": id, "cwd": in.Cwd, "kind": "bg",
		"name": argValue(in.Args, "--name"), "status": "idle", "messagingSocketPath": sock}
	if os.Getenv("FAKE_CLAUDE_NO_JOB_ID") != "" {
		sf["jobId"] = ""
	}
	job := filepath.Join(filepath.Dir(dir), "jobs", id)
	os.MkdirAll(job, 0700)
	os.WriteFile(filepath.Join(job, "state.json"), []byte(`{"state":"working","detail":null}`), 0600)
	writeFakeClaudeSessionFile(dir, sf)

	var mu sync.Mutex
	transcribe := os.Getenv(envFakeClaudeTranscribe) != ""
	userTurn := func(text string) {
		line, _ := json.Marshal(map[string]any{"type": "user", "timestamp": time.Now().UTC().Format(time.RFC3339Nano),
			"message": map[string]string{"role": "user", "content": text}})
		fakeClaudeTranscriptLine(dir, in.Cwd, session, string(line))
	}
	if first := argAfterDashes(in.Args); transcribe && first != "" {
		userTurn(first)
	}
	if ln == nil {
		select {} // a session with no inbox runs until it is stopped
	}
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
				var frame struct {
					Body string `json:"body"`
				}
				if transcribe && json.Unmarshal(sc.Bytes(), &frame) == nil && !fakeClaudeDropInbox() {
					userTurn(frame.Body)
				}
				mu.Unlock()
			}
		}()
	}
}

// fakeClaudeDropInbox reports whether the session drops this inbox line, and
// counts the drop.
func fakeClaudeDropInbox() bool {
	path := os.Getenv(envFakeClaudeDropFile)
	if path == "" {
		return false
	}
	raw, err := os.ReadFile(path)
	n, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || n <= 0 {
		return false
	}
	os.WriteFile(path, []byte(strconv.Itoa(n-1)), 0o600)
	return true
}

// argAfterDashes is the prompt after "--" in a Claude command line.
func argAfterDashes(args []string) string {
	for i, a := range args {
		if a == "--" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// writeFakeClaudeSessionFile replaces the session file of this process in one
// rename, so a reader never sees half a file.
func writeFakeClaudeSessionFile(dir string, sf map[string]any) {
	b, _ := json.Marshal(sf)
	tmp := filepath.Join(dir, "."+strconv.Itoa(os.Getpid())+".tmp")
	os.WriteFile(tmp, b, 0o600)
	os.Rename(tmp, filepath.Join(dir, strconv.Itoa(os.Getpid())+".json"))
}

// fakeClaudeNoResponseLine is the placeholder entry Claude Code writes to the
// transcript when it closes an unfinished turn, copied from a real transcript
// (usage trimmed). It is not an error: isApiErrorMessage is false.
const fakeClaudeNoResponseLine = `{"parentUuid":"cc000a9a","isSidechain":false,"type":"assistant","uuid":"8a3dd78c","timestamp":"2026-11-04T01:11:39.571Z","message":{"id":"c6da3859","model":"<synthetic>","role":"assistant","stop_reason":"stop_sequence","stop_sequence":"","type":"message","usage":{"input_tokens":0,"output_tokens":0},"content":[{"type":"text","text":"No response requested."}]},"isApiErrorMessage":false,"sessionKind":"bg","userType":"external","entrypoint":"cli","version":"2.1.318"}`

// fakeClaudeTranscriptLine appends one line to the transcript of a session,
// where Claude Code keeps it: <projects>/<cwd folder>/<session>.jsonl, next to
// the sessions folder.
func fakeClaudeTranscriptLine(sessionsDir, cwd, session, line string) {
	dir := filepath.Join(filepath.Dir(sessionsDir), "projects", ClaudeTranscriptDir(cwd))
	os.MkdirAll(dir, 0o700)
	f, err := os.OpenFile(filepath.Join(dir, session+".jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	f.WriteString(line + "\n")
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
			"client": os.Getenv("FAKE_CLAUDE_MCP_CLIENT"), "harness": "claude", "native_id": session, "cwd": cwd}})
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
		for sc.Scan() { // Record the hello result for restart and adoption tests.
			if os.Getenv("FAKE_CLAUDE_MCP_CLIENT") == "cto-mcp" {
				var response struct {
					ID string `json:"id"`
				}
				if json.Unmarshal(sc.Bytes(), &response) == nil && response.ID == strconv.Itoa(next) {
					// Write then rename: a test that polls this file must never
					// read a half-written answer.
					tmp := filepath.Join(cwd, "fake-cto-hello.json.tmp")
					if os.WriteFile(tmp, append([]byte(nil), sc.Bytes()...), 0600) == nil {
						os.Rename(tmp, filepath.Join(cwd, "fake-cto-hello.json"))
					}
				}
			}
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
	if strings.Contains(body, "FAKE:newbranch") {
		// Work on a new branch made from main, which does not hold the
		// worktree's own commits, as a worker does when it starts another task.
		exec.Command("git", "-C", cwd, "checkout", "-q", "-b", "chore/pin", "main").Run()
	}
	if strings.Contains(body, "FAKE:edit") {
		fakeEdit(cwd)
	}
	if strings.Contains(body, "FAKE:dirty") {
		// Work left uncommitted, and a file that .gitignore may match.
		os.WriteFile(filepath.Join(cwd, "dirty.txt"), []byte("dirty\n"), 0o644)
		os.WriteFile(filepath.Join(cwd, "ignored.txt"), []byte("ignored\n"), 0o644)
	}
	status := ""
	if strings.Contains(body, "FAKE:blocked") {
		status = "blocked"
	}
	call := map[string]any{"report": "finished the task in " + cwd + " (task was " + strconv.Itoa(len(body)) + " bytes)", "status": status}
	if strings.Contains(body, "FAKE:partial") {
		call["complete"] = false
	}
	if strings.Contains(body, "FAKE:textno") {
		// The worker says it is not done in the report text only: the
		// complete field stays absent, as with a Codex report.
		call["report"] = call["report"].(string) + "\ncomplete: no"
	}
	if strings.Contains(body, "FAKE:firstno") {
		// The marker is the first line of the report, then the explanation.
		call["report"] = "complete: no\n" + call["report"].(string)
	}
	send("worker_handback", call)
}

// fsClaudeAdapter is the Claude adapter of the tests: it reads the session
// files of a folder, and delivers by writing a line to the inbox socket of the
// session. The real adapter (package claude) does the same with Claude's frame.
type fsClaudeAdapter struct {
	dir string

	mu        sync.Mutex
	delivered []protocol.Message // every message written to an inbox
}

// deliveredTo returns the messages delivered to the session with this
// gateway ID.
func (a *fsClaudeAdapter) deliveredTo(session string) []protocol.Message {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []protocol.Message
	for _, m := range a.delivered {
		if m.To == session {
			out = append(out, m)
		}
	}
	return out
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
			Status              string `json:"status"`
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
		status := sf.Status
		switch status {
		case "idle", "busy", "shell", "exited":
		default:
			status = "unknown"
		}
		infos = append(infos, protocol.SessionInfo{NativeID: sf.SessionID, PID: sf.PID, Cwd: sf.Cwd, Name: sf.Name,
			Status: status, Delivery: "native-claude", Mode: mode})
	}
	h.SetSessions("claude", infos)
}

func (a *fsClaudeAdapter) Deliver(ctx context.Context, target protocol.SessionInfo, m protocol.Message) Result {
	return a.deliverBody(target, m, m.Body)
}

// deliverBody writes body to the inbox of the target session and records m.
func (a *fsClaudeAdapter) deliverBody(target protocol.SessionInfo, m protocol.Message, body string) Result {
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
		b, _ := json.Marshal(map[string]string{"body": body})
		c.Write(append(b, '\n'))
		a.mu.Lock()
		a.delivered = append(a.delivered, m)
		a.mu.Unlock()
		return Result{Delivered: true}
	}
	return Result{Detail: "no such Claude session"}
}

// DeliverCTO frames the text with its message ID, as the real adapter's
// BuildCTOFrame does, so the transcript shows which message a turn took up.
func (a *fsClaudeAdapter) DeliverCTO(ctx context.Context, target protocol.SessionInfo, id, text string) Result {
	body := fmt.Sprintf("<cross-session-message from-name=\"owner\" from-mode=\"prompting\" msg-id=\"%s\">\n%s\n</cross-session-message>", id, text)
	return a.deliverBody(target, protocol.Message{ID: id, Body: text}, body)
}

// DeliverCTOSystem frames agent-net's own prompt with its label, as the real
// adapter's BuildCTOAgentFrame does.
func (a *fsClaudeAdapter) DeliverCTOSystem(ctx context.Context, target protocol.SessionInfo, id, label, text string) Result {
	body := fmt.Sprintf("<cross-session-message from-name=\"agent-net\" from-mode=\"prompting\" msg-id=\"%s\">\n[agent-net: %s]\n\n%s\n</cross-session-message>", id, label, text)
	return a.deliverBody(target, protocol.Message{ID: id, Body: text}, body)
}

// setFakeClaudeStatus records what the fake Claude session file says about a
// turn, as Claude Code does: busy while the model works and idle when it ends.
func setFakeClaudeStatus(t *testing.T, sessions string, pid int, status string) {
	t.Helper()
	path := filepath.Join(sessions, strconv.Itoa(pid)+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var sf map[string]any
	if err := json.Unmarshal(raw, &sf); err != nil {
		t.Fatal(err)
	}
	sf["status"] = status
	b, err := json.Marshal(sf)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// useFakeClaude points a test at the fake Claude and returns the folders it
// uses: the session files and the log of `claude stop`.
func useFakeClaude(t *testing.T) (sessions, stopLog string) {
	t.Helper()
	base := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(base, "claude-config"))
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

// fakeHarnessLauncher applies node-specific settings only to the fake child.
// exec preserves its PID, process group and signal behavior.
func fakeHarnessLauncher(t *testing.T, name string, env map[string]string) string {
	t.Helper()
	var keys []string
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var script strings.Builder
	script.WriteString("#!/bin/sh\n")
	for _, key := range keys {
		script.WriteString(key + "=" + shellQuote(env[key]) + " ")
	}
	script.WriteString("exec " + shellQuote(os.Args[0]) + " \"$@\"\n")
	bin := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(bin, []byte(script.String()), 0700); err != nil {
		t.Fatal(err)
	}
	return bin
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
	// The OS sandbox probes (envFakePiProbeHome, envFakePiProbeExt): the
	// error of each write, "" when the write worked.
	HomeWrite string `json:"home_write,omitempty"`
	ExtWrite  string `json:"ext_write,omitempty"`
	TmpWrite  string `json:"tmp_write,omitempty"`
	AgentDir  string `json:"agent_dir,omitempty"`
}

// The fake Pi tries these writes when the variables are set, so a test can
// see what the OS sandbox lets through.
const (
	envFakePiProbeHome = "FAKE_PI_PROBE_HOME" // a folder outside the sandbox's writable paths
	envFakePiProbeExt  = "FAKE_PI_PROBE_EXT"  // try to add a Pi extension
)

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
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
	dir := fakeHarnessDir(cwd, "PI_CODING_AGENT_DIR")
	fakeRegister()
	rec := fakePiRecord{Args: os.Args[1:], Cwd: cwd, Pid: os.Getpid(), Env: map[string]string{}}
	rec.Pgid, _ = syscall.Getpgid(0)
	for _, k := range []string{"AGENT_NET_WORKER", "AGENT_NET_SOCKET"} {
		rec.Env[k] = os.Getenv(k)
	}
	if fi, err := os.Stdin.Stat(); err == nil {
		rec.Stdin = fi.Mode().String()
	}
	if probeDir := os.Getenv(envFakePiProbeHome); probeDir != "" {
		rec.HomeWrite = errText(os.WriteFile(filepath.Join(probeDir, "probe"), []byte("x"), 0o600))
		rec.TmpWrite = errText(os.WriteFile(filepath.Join(os.Getenv("TMPDIR"), "fake-pi-probe"), []byte("x"), 0o600))
		rec.AgentDir = os.Getenv("PI_CODING_AGENT_DIR")
	}
	if os.Getenv(envFakePiProbeExt) != "" {
		rec.ExtWrite = errText(os.WriteFile(filepath.Join(os.Getenv("PI_CODING_AGENT_DIR"), "extensions", "evil.ts"), []byte("x"), 0o600))
	}
	child := exec.Command("sleep", "600")
	if child.Start() == nil {
		rec.ChildPid = child.Process.Pid
		go child.Wait()
	}
	writeRecord(dir, "fake-pi.json", rec)
	session := argValue(os.Args, "--session-id")
	fakePiSessionFile(session, dir)
	go fakeOCPlugin("pi", session, dir)
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
	Args             []string          `json:"args"`
	Cwd              string            `json:"cwd"`
	Env              map[string]string `json:"env"`
	Pid              int               `json:"pid"`
	Pgid             int               `json:"pgid"`
	ChildPid         int               `json:"child_pid"`
	Thread           map[string]any    `json:"thread"`
	Turns            []string          `json:"turns"`
	Answers          []any             `json:"answers"`
	Resumed          int               `json:"resumed"`
	CompletedOffline bool              `json:"completed_offline"`
	// TurnParams are the params of each turn/start; ResumeParams those of the
	// last thread/resume.
	TurnParams   []map[string]any `json:"turn_params"`
	ResumeParams map[string]any   `json:"resume_params"`
	// SkillRoots are the extra skill roots the daemon set on this server.
	SkillRoots []string `json:"skill_roots"`
}

// fakeCodexModels is `codex debug models`: the catalog that names the
// reasoning efforts of each model. gpt-fake takes low to high, not max.
func fakeCodexModels() {
	fmt.Print(`{"models":[{"slug":"gpt-fake","supported_reasoning_levels":[` +
		`{"effort":"low","description":"Low"},{"effort":"medium","description":"Medium"},{"effort":"high","description":"High"}]}]}`)
}

// fakeCodexExec is `codex exec` for the difficulty tests. The brief names one
// of the kinds (analyzer, analysis, judge) in an
// AGENT_NET_DIFFICULTY_KIND marker. FAKE_CODEX_EXEC_DIR holds one JSON array
// per kind; the fake takes the first reply and writes the rest back, so one
// test can make a retry. FAKE_CODEX_EXEC_HANG keeps it running past a test's
// time limit.
func fakeCodexExec() {
	cwd, _ := os.Getwd()
	stdin, _ := io.ReadAll(os.Stdin)
	text := string(stdin)
	kind := ""
	for _, k := range []string{"analyzer", "analysis", "judge"} {
		if strings.Contains(text, "AGENT_NET_DIFFICULTY_KIND="+k) {
			kind = k
			break
		}
	}
	dir := os.Getenv(envFakeCodexExec)
	out := ""
	if kind != "" && dir != "" {
		name := filepath.Join(dir, kind+".json")
		var list []string
		if raw, err := os.ReadFile(name); err == nil && json.Unmarshal(raw, &list) == nil && len(list) > 0 {
			out = list[0]
			rest, _ := json.Marshal(list[1:])
			os.WriteFile(name, rest, 0o644)
		}
	}
	if dir != "" {
		b, _ := json.Marshal(map[string]any{"args": os.Args[1:], "cwd": cwd, "stdin": text, "kind": kind})
		if f, err := os.OpenFile(filepath.Join(dir, "calls.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			f.Write(append(b, '\n'))
			f.Close()
		}
	}
	if os.Getenv(envFakeCodexExecHang) != "" || strings.HasPrefix(out, "HANG") {
		waitUntilKilled()
	}
	if path := argValue(os.Args, "-o"); path != "" {
		os.WriteFile(path, []byte(out), 0o644)
	}
	fmt.Print(out)
	os.Exit(0)
}

// waitUntilKilled keeps a hanging fake running until its parent kills it. The
// daemon kills a run that passes its time limit, so the fake never has to end
// on its own. It waits on a pipe whose write end nothing writes to.
func waitUntilKilled() {
	r, w, err := os.Pipe()
	if err != nil {
		panic("fake codex: " + err.Error())
	}
	io.Copy(io.Discard, r)
	// The write end must stay open while the copy waits, or the read ends.
	runtime.KeepAlive(w)
}

// fakeCodexThreadsFile is where a fake app server keeps the turns of its threads.
// It is never in the fake's folder: for a worker that folder is its worktree,
// and a file there is listed in the worker's diff. The run's fake folder holds
// it instead, under a name made from the fake's folder, so two fakes never
// share a file. The reaper skips names that are not pids.
func fakeCodexThreadsFile(folder string) string {
	root := os.Getenv(envFakePids)
	if root == "" {
		root = os.TempDir()
	}
	sum := sha256.Sum256([]byte(folder))
	return filepath.Join(root, "fake-codex-threads-"+hex.EncodeToString(sum[:8])+".json")
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
	fakeRegister()
	cwd, _ := os.Getwd()
	dir := fakeHarnessDir(cwd, "CODEX_HOME")
	rec := fakeCodexRecord{Args: os.Args[1:], Cwd: cwd, Pid: os.Getpid(), Env: map[string]string{}}
	rec.Pgid, _ = syscall.Getpgid(0)
	child := exec.Command("sleep", "600")
	if child.Start() == nil {
		rec.ChildPid = child.Process.Pid
		go child.Wait()
	}
	var mu sync.Mutex
	// threadTurns keeps each thread's turns, as user text and answer, for thread/read.
	// Codex keeps a thread on disk, so the turns are read back from the file a
	// restarted server finds, not only from this process's memory.
	threadTurns := map[string][][2]string{}
	threadsFile := fakeCodexThreadsFile(cwd)
	if raw, err := os.ReadFile(threadsFile); err == nil {
		_ = json.Unmarshal(raw, &threadTurns)
	}
	save := func() {
		mu.Lock()
		defer mu.Unlock()
		writeRecord(dir, "fake-codex.json", rec)
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
				completed := rec.CompletedOffline
				mu.Unlock()
				save()
				status := "active"
				turns := []any{}
				if completed {
					status = "idle"
					turns = []any{
						map[string]any{"id": "turn-previous", "status": "completed", "items": []any{map[string]any{"type": "agentMessage", "text": "old turn"}}},
						map[string]any{"id": "turn-offline", "status": "completed", "items": []any{map[string]any{"type": "agentMessage", "text": "intermediate"}, map[string]any{"type": "agentMessage", "phase": "final_answer", "text": "finished while daemon was down"}}},
					}
				}
				write(map[string]any{"id": m.ID, "result": map[string]any{"thread": map[string]any{"id": thread, "status": map[string]any{"type": status}, "turns": turns}}})
				if completed {
					continue
				}
				// A surviving turn can complete after a new subscriber resumes it.
				go func(thread string) {
					for {
						if _, err := os.Stat(filepath.Join(dir, "fake-codex-finish")); err == nil {
							write(map[string]any{"method": "item/completed", "params": map[string]any{"threadId": thread, "item": map[string]any{"type": "agentMessage", "phase": "final_answer", "text": "finished after restart"}}})
							write(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": thread, "turn": map[string]any{"status": "completed"}}})
							return
						}
						select {
						case <-r.Context().Done():
							return
						case <-time.After(10 * time.Millisecond):
						}
					}
				}(thread)
			case "skills/extraRoots/set":
				roots, _ := p["extraRoots"].([]any)
				mu.Lock()
				rec.SkillRoots = nil
				for _, root := range roots {
					if text, ok := root.(string); ok {
						rec.SkillRoots = append(rec.SkillRoots, text)
					}
				}
				mu.Unlock()
				save()
				write(map[string]any{"id": m.ID, "result": map[string]any{}})
			case "thread/read":
				if fakeReadsHang() {
					continue
				}
				id, _ := p["threadId"].(string)
				mu.Lock()
				turns := []any{}
				for _, t := range threadTurns[id] {
					turns = append(turns, map[string]any{"id": "turn-read", "status": "completed", "items": []any{
						map[string]any{"type": "userMessage", "content": []any{map[string]any{"type": "text", "text": t[0]}}},
						map[string]any{"type": "agentMessage", "phase": "final_answer", "text": t[1]}}})
				}
				mu.Unlock()
				write(map[string]any{"id": m.ID, "result": map[string]any{"thread": map[string]any{"id": id, "turns": turns}}})
			case "turn/start", "thread/queue/add":
				// thread/queue/add is how agent-net sends a message to a thread it
				// drives: the user message item carries the message's ID as clientId.
				text := ""
				if in, ok := p["input"].([]any); ok && len(in) > 0 {
					text, _ = in[0].(map[string]any)["text"].(string)
				}
				clientID, _ := p["clientUserMessageId"].(string)
				mu.Lock()
				rec.Turns = append(rec.Turns, text)
				rec.TurnParams = append(rec.TurnParams, p)
				mu.Unlock()
				save()
				turn := "turn-" + randHex(3)
				if m.Method == "turn/start" {
					write(map[string]any{"id": m.ID, "result": map[string]any{"turn": map[string]any{"id": turn, "status": "inProgress"}}})
				} else {
					write(map[string]any{"id": m.ID, "result": map[string]any{}})
				}
				go func() {
					write(map[string]any{"method": "turn/started", "params": map[string]any{"threadId": thread, "turn": map[string]any{"id": turn, "status": "inProgress"}}})
					if clientID != "" && !strings.Contains(text, "FAKE:untaken") {
						write(map[string]any{"method": "item/started", "params": map[string]any{"threadId": thread, "turnId": turn,
							"item": map[string]any{"type": "userMessage", "clientId": clientID, "content": []any{map[string]any{"type": "text", "text": text}}}}})
					}
					if strings.Contains(text, "FAKE:compact") {
						write(map[string]any{"method": "item/completed", "params": map[string]any{"threadId": thread, "turnId": turn,
							"item": map[string]any{"type": "contextCompaction", "id": "c1"}}})
					}
					if strings.Contains(text, "FAKE:offline") {
						// The server keeps working even when its subscriber goes away.
						for {
							if _, err := os.Stat(filepath.Join(dir, "fake-codex-offline-finish")); err == nil {
								mu.Lock()
								rec.CompletedOffline = true
								mu.Unlock()
								save()
								return
							}
							time.Sleep(10 * time.Millisecond)
						}
					}
					if strings.Contains(text, "FAKE:wait") {
						finish := "fake-codex-finish"
						if strings.Contains(text, "FAKE:wait-second") {
							finish = "fake-codex-finish-second"
						}
						for {
							if _, err := os.Stat(filepath.Join(dir, finish)); err == nil {
								break
							}
							select {
							case <-r.Context().Done():
								return
							case <-time.After(10 * time.Millisecond):
							}
						}
					}
					if strings.Contains(text, "FAKE:slow") {
						<-r.Context().Done()
						return
					}
					if strings.Contains(text, "FAKE:edit") {
						fakeEdit(dir)
					}
					if strings.Contains(text, "FAKE:approve") {
						command := "rm -rf build"
						if strings.Contains(text, "FAKE:approve-status") {
							command = "git status"
						}
						write(map[string]any{"id": "srv-1", "method": "item/commandExecution/requestApproval",
							"params": map[string]any{"threadId": thread, "turnId": turn, "itemId": "i1", "command": command, "cwd": cwd, "reason": "outside the sandbox"}})
						select {
						case a := <-answers:
							var v any
							json.Unmarshal(a, &v)
							mu.Lock()
							rec.Answers = append(rec.Answers, v)
							mu.Unlock()
							save()
						case <-time.After(scaled(3 * time.Second)):
						}
					}
					if strings.Contains(text, "FAKE:hold-codex") {
						os.WriteFile(filepath.Join(dir, "fake-codex-held"), nil, 0600)
						for {
							if _, err := os.Stat(filepath.Join(dir, "fake-codex-release")); err == nil {
								break
							}
							time.Sleep(10 * time.Millisecond)
						}
						mu.Lock()
						rec.CompletedOffline = true
						mu.Unlock()
						save()
					}
					status, errObj := "completed", any(nil)
					if strings.Contains(text, "FAKE:fail") {
						status, errObj = "failed", map[string]any{"message": "model overloaded"}
					}
					report := "codex done in " + cwd + " (" + strconv.Itoa(len(text)) + " bytes)"
					if strings.Contains(text, "FAKE:partial") {
						report += "\ncomplete: no"
					}
					if strings.Contains(text, "Turn each phrase in the JSON array below") {
						report = `{"backups":"daily backup operations","ops for backups":"ops for backup operations"}`
					}
					mu.Lock()
					threadTurns[thread] = append(threadTurns[thread], [2]string{text, report})
					raw, _ := json.Marshal(threadTurns)
					_ = os.WriteFile(threadsFile, raw, 0o600)
					mu.Unlock()
					write(map[string]any{"method": "item/completed", "params": map[string]any{"threadId": thread, "turnId": turn,
						"item": map[string]any{"type": "agentMessage", "id": "m1", "phase": "final_answer", "text": report}}})
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

// The fake `claude --bg` reports a session process that is gone at once,
// instead of waiting out a timer while the daemon still waits for it.
func TestFakeClaudeReportsASessionThatExitsAtOnce(t *testing.T) {
	dir := t.TempDir()
	sessions := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(sessions, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envFakeClaude, "1")
	t.Setenv(envFakeClaudeSessions, sessions)
	// A folder that does not exist makes the session process exit before it
	// writes its session file.
	t.Setenv(envFakeClaudeSockets, filepath.Join(dir, "missing"))
	cmd := exec.Command(os.Args[0], "--bg", "--name", "test")
	cmd.Dir = dir
	out := make(chan []byte, 1)
	go func() {
		b, _ := cmd.CombinedOutput()
		out <- b
	}()
	select {
	case b := <-out:
		if !strings.Contains(string(b), "the session did not start") {
			t.Fatalf("the fake claude said %q", b)
		}
	case <-time.After(2 * time.Second):
		cmd.Process.Kill()
		t.Fatal("the fake claude kept waiting after its session process exited")
	}
}
