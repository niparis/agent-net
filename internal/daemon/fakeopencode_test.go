package daemon

import (
	"bufio"
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
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The fake `opencode serve` of the worker tests. The test binary runs it when
// it is started as `<binary> serve ...` with OPENCODE_SERVER_PASSWORD set. It
// does what the real one does for a worker:
//
//   - an HTTP server with basic auth: GET /api/info, POST /api/session;
//   - the agent-net plugin: once a session is created it says hello on the
//     daemon socket as an opencode session, and answers every message it
//     gets with a reply, like a model that uses agent_reply;
//   - a child process in its process group, to prove that the group is killed.
//
// It writes fake-opencode.json in its working folder (the project): the
// arguments, the working folder and the environment it saw. Behaviour is
// chosen by the message body ("FAKE:..." commands) and by these variables:
//
//	FAKE_OC_NOPLUGIN=1     never register a session (the plugin is missing)
//	FAKE_OC_EXIT=1         exit at once
//	FAKE_OC_IGNORE_TERM=1  ignore SIGTERM
//	FAKE_OC_WRONG_SESSION=1  say hello with another session ID than the API gave

type fakeOCRecord struct {
	Args        []string          `json:"args"`
	Cwd         string            `json:"cwd"`
	Env         map[string]string `json:"env"`
	Pid         int               `json:"pid"`
	Pgid        int               `json:"pgid"`
	ChildPid    int               `json:"child_pid"`
	SessionID   string            `json:"session_id"`
	SessionBody map[string]any    `json:"session_body"`
}

func runFakeOpenCode() {
	if os.Getenv("FAKE_OC_EXIT") != "" {
		os.Exit(3)
	}
	if os.Getenv("FAKE_OC_IGNORE_TERM") != "" {
		signal.Ignore(syscall.SIGTERM)
	}
	cwd, _ := os.Getwd()
	rec := fakeOCRecord{Args: os.Args[1:], Cwd: cwd, Pid: os.Getpid(), Env: map[string]string{}}
	rec.Pgid, _ = syscall.Getpgid(0)
	for _, k := range []string{"AGENT_NET_WORKER", "AGENT_NET_WORKER_SESSION_FILE", "AGENT_NET_SOCKET", "XDG_STATE_HOME", "OPENCODE_SERVER_PASSWORD"} {
		rec.Env[k] = os.Getenv(k)
	}
	child := exec.Command("sleep", "600")
	if child.Start() == nil {
		rec.ChildPid = child.Process.Pid
		go child.Wait()
	}
	var mu sync.Mutex
	write := func() {
		mu.Lock()
		defer mu.Unlock()
		b, _ := json.Marshal(rec)
		os.WriteFile(filepath.Join(cwd, "fake-opencode.json"), b, 0o644)
	}
	write()

	var host, port string = "127.0.0.1", ""
	for i, a := range os.Args {
		switch a {
		case "--hostname":
			host = os.Args[i+1]
		case "--port":
			port = os.Args[i+1]
		}
	}
	auth := func(r *http.Request) bool {
		u, p, ok := r.BasicAuth()
		return ok && u == "opencode" && p == os.Getenv("OPENCODE_SERVER_PASSWORD")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/info", func(w http.ResponseWriter, r *http.Request) {
		if !auth(r) {
			http.Error(w, `{"_tag":"UnauthorizedError"}`, 401)
			return
		}
		fmt.Fprint(w, `{"version":"fake"}`)
	})
	mux.HandleFunc("GET /api/session/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !auth(r) {
			http.Error(w, `{"_tag":"UnauthorizedError"}`, 401)
			return
		}
		fmt.Fprint(w, `{"data":{"cost":0.0123,"tokens":{"input":1200,"output":340,"reasoning":0,"cache":{"read":0,"write":0}},"model":{"providerID":"fake","id":"m1"}}}`)
	})
	mux.HandleFunc("POST /api/session", func(w http.ResponseWriter, r *http.Request) {
		if !auth(r) {
			http.Error(w, `{"_tag":"UnauthorizedError"}`, 401)
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		id := "ses_fake_" + strconv.Itoa(os.Getpid())
		mu.Lock()
		rec.SessionID, rec.SessionBody = id, body
		mu.Unlock()
		write()
		if os.Getenv("FAKE_OC_NOPLUGIN") == "" {
			hello := id
			if os.Getenv("FAKE_OC_WRONG_SESSION") != "" {
				hello = "ses_other"
			}
			go fakeOCPlugin(hello, cwd)
		}
		fmt.Fprintf(w, `{"data":{"id":%q}}`, id)
	})
	if err := http.ListenAndServe(net.JoinHostPort(host, port), mux); err != nil {
		fmt.Fprintln(os.Stderr, "fake opencode:", err)
		os.Exit(1)
	}
}

// fakeEdit writes and commits a file in dir, as a worker would.
func fakeEdit(dir string) {
	os.WriteFile(filepath.Join(dir, "work.txt"), []byte("hello\nworld\n"), 0o644)
	env := append(os.Environ(), "GIT_AUTHOR_NAME=w", "GIT_AUTHOR_EMAIL=w@example.com", "GIT_COMMITTER_NAME=w", "GIT_COMMITTER_EMAIL=w@example.com",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	for _, args := range [][]string{{"add", "work.txt"}, {"commit", "-q", "-m", "work"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir, cmd.Env = dir, env
		cmd.Run()
	}
}

// fakeOCPlugin is the agent-net plugin inside the fake OpenCode: one connection
// to the daemon for the session, which it opens again when it drops.
func fakeOCPlugin(sessionID, cwd string) {
	for {
		fakeOCPluginOnce(sessionID, cwd)
		time.Sleep(50 * time.Millisecond)
	}
}

func fakeOCPluginOnce(sessionID, cwd string) {
	c, err := net.Dial("unix", os.Getenv("AGENT_NET_SOCKET"))
	if err != nil {
		return
	}
	defer c.Close()
	var wmu sync.Mutex
	next := 0
	nextID := func() string {
		wmu.Lock()
		defer wmu.Unlock()
		next++
		return strconv.Itoa(next)
	}
	send := func(method string, params any) {
		b, _ := json.Marshal(map[string]any{"id": nextID(), "method": method, "params": params})
		wmu.Lock()
		c.Write(append(b, '\n'))
		wmu.Unlock()
	}
	send("hello", map[string]any{"client": "opencode", "harness": "opencode", "native_id": sessionID, "cwd": cwd, "status": "idle"})
	sc := bufio.NewScanner(c)
	sc.Buffer(make([]byte, 0, 1<<16), 1<<20)
	results := map[string]chan string{}
	var rmu sync.Mutex
	for sc.Scan() {
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Message protocol.Message `json:"message"`
			} `json:"params"`
			Result json.RawMessage `json:"result"`
			Error  string          `json:"error"`
		}
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		if len(m.ID) > 0 {
			var id string
			json.Unmarshal(m.ID, &id)
			rmu.Lock()
			ch := results[id]
			rmu.Unlock()
			if ch != nil {
				text := string(m.Result)
				if m.Error != "" {
					text = "error: " + m.Error
				}
				ch <- text
			}
			continue
		}
		if m.Method != "deliver" {
			continue
		}
		msg := m.Params.Message
		send("delivered", map[string]any{"message_id": msg.ID, "ok": true})
		body := msg.Body
		switch {
		case strings.Contains(body, "FAKE:ignore"):
		case strings.Contains(body, "FAKE:start"):
			// A worker asking for another worker. It reports what the daemon said.
			id := nextID()
			ch := make(chan string, 1)
			rmu.Lock()
			results[id] = ch
			rmu.Unlock()
			b, _ := json.Marshal(map[string]any{"id": id, "method": "worker_start",
				"params": protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "child"}})
			wmu.Lock()
			c.Write(append(b, '\n'))
			wmu.Unlock()
			go func() {
				select {
				case text := <-ch:
					send("reply", map[string]any{"message_id": msg.ID, "body": "start result: " + text})
				case <-time.After(5 * time.Second):
					send("reply", map[string]any{"message_id": msg.ID, "body": "start result: timeout"})
				}
			}()
		case strings.Contains(body, "FAKE:handback"):
			// What a model does at the end of its task: edit, then hand back.
			if strings.Contains(body, "FAKE:edit") {
				fakeEdit(cwd)
			}
			status := ""
			if strings.Contains(body, "FAKE:blocked") {
				status = "blocked"
			}
			send("worker_handback", map[string]any{"report": "finished the task in " + cwd, "status": status})
		case strings.Contains(body, "FAKE:edit"):
			fakeEdit(cwd)
			send("reply", map[string]any{"message_id": msg.ID, "body": "edited"})
		default:
			send("reply", map[string]any{"message_id": msg.ID, "body": "ok from worker (task was " + strconv.Itoa(len(body)) + " bytes)"})
		}
	}
}
