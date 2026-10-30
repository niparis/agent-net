package daemon

import (
	"bufio"
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
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
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

// fakeOCFailed is set when the fake gets a message with FAKE:modelfail: the
// session then reports a failed run, as OpenCode 2.2 does when the provider
// refuses (outcome "failed", and an idle entry with the error in the messages).
var fakeOCFailed atomic.Bool
var fakeOCLocalFailed atomic.Bool

const fakeOCError = "AI.Error: Upstream request failed: Insufficient account funds. api_key=sk-abcdefghijklmnopqrstuvwxyz123456\nadd funds"

type fakeOCRecord struct {
	Args        []string          `json:"args"`
	Cwd         string            `json:"cwd"`
	Env         map[string]string `json:"env"`
	Pid         int               `json:"pid"`
	Pgid        int               `json:"pgid"`
	ChildPid    int               `json:"child_pid"`
	SessionID   string            `json:"session_id"`
	SessionBody map[string]any    `json:"session_body"`
	LocationDir string            `json:"location_dir"`
	Turns       []string          `json:"turns"`
}

func runFakeOpenCode() {
	if os.Getenv("FAKE_OC_EXIT") != "" {
		os.Exit(3)
	}
	if os.Getenv("FAKE_OC_IGNORE_TERM") != "" {
		signal.Ignore(syscall.SIGTERM)
	}
	fakeRegister()
	cwd, _ := os.Getwd()
	rec := fakeOCRecord{Args: os.Args[1:], Cwd: cwd, Pid: os.Getpid(), Env: map[string]string{}}
	rec.Pgid, _ = syscall.Getpgid(0)
	for _, k := range []string{"AGENT_NET_CTO", "AGENT_NET_WORKER", "AGENT_NET_WORKER_SESSION_FILE", "AGENT_NET_SOCKET", "XDG_STATE_HOME", "XDG_DATA_HOME", "OPENCODE_SERVER_PASSWORD"} {
		rec.Env[k] = os.Getenv(k)
	}
	child := exec.Command("sleep", "600")
	if child.Start() == nil {
		rec.ChildPid = child.Process.Pid
		go child.Wait()
	}
	var mu sync.Mutex
	var location atomic.Int64
	var pluginOnce sync.Once
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
	mux.HandleFunc("GET /api/model", func(w http.ResponseWriter, r *http.Request) {
		if !auth(r) {
			http.Error(w, `{"_tag":"UnauthorizedError"}`, 401)
			return
		}
		fmt.Fprint(w, `{"data":[{"providerID":"fake","id":"m1","modelID":"m1","limit":{"context":1000000}}]}`)
	})
	// OpenCode loads the plugin when a client first uses the project folder.
	mux.HandleFunc("GET /api/location", func(w http.ResponseWriter, r *http.Request) {
		if !auth(r) {
			http.Error(w, `{"_tag":"UnauthorizedError"}`, 401)
			return
		}
		mu.Lock()
		rec.LocationDir = r.Header.Get("x-opencode-directory")
		id := rec.SessionID
		mu.Unlock()
		// A resumed server has no POST /api/session: the daemon writes the
		// existing session ID to the file the plugin reads.
		if id == "" {
			if raw, err := os.ReadFile(os.Getenv("AGENT_NET_WORKER_SESSION_FILE")); err == nil {
				id = strings.TrimSpace(string(raw))
				mu.Lock()
				rec.SessionID = id
				mu.Unlock()
			}
		}
		write()
		if id != "" && os.Getenv("FAKE_OC_NOPLUGIN") == "" && os.Getenv("AGENT_NET_CTO") != "1" {
			hello := id
			if os.Getenv("FAKE_OC_WRONG_SESSION") != "" {
				hello = "ses_other"
			}
			location.Add(1)
			pluginOnce.Do(func() {
				go fakeOCPluginTracked("opencode", hello, cwd, &location, func(text string) {
					body, _ := json.Marshal(map[string]string{"text": text, "delivery": "queue"})
					req, err := http.NewRequest("POST", "http://"+net.JoinHostPort(host, port)+"/api/session/"+id+"/prompt", strings.NewReader(string(body)))
					if err != nil {
						return
					}
					req.SetBasicAuth("opencode", os.Getenv("OPENCODE_SERVER_PASSWORD"))
					resp, err := http.DefaultClient.Do(req)
					if err == nil {
						resp.Body.Close()
					}
				})
			})
		}
		fmt.Fprint(w, `{"data":{}}`)
	})
	mux.HandleFunc("GET /api/session/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !auth(r) {
			http.Error(w, `{"_tag":"UnauthorizedError"}`, 401)
			return
		}
		if fakeOCFailed.Load() || fakeOCLocalFailed.Load() {
			fmt.Fprint(w, `{"data":{"outcome":"failed","cost":0,"tokens":{"input":0,"output":0,"reasoning":0,"cache":{"read":0,"write":0}},"model":{"providerID":"fake","id":"m1"}}}`)
			return
		}
		fmt.Fprint(w, `{"data":{"cost":0.0123,"tokens":{"input":1200,"output":340,"reasoning":0,"cache":{"read":0,"write":0}},"model":{"providerID":"fake","id":"m1"}}}`)
	})
	mux.HandleFunc("GET /api/session/active", func(w http.ResponseWriter, r *http.Request) {
		if !auth(r) {
			http.Error(w, `{"_tag":"UnauthorizedError"}`, 401)
			return
		}
		id := ""
		if raw, err := os.ReadFile(filepath.Join(os.Getenv("XDG_DATA_HOME"), "fake-active")); err == nil {
			id = strings.TrimSpace(string(raw))
		}
		if id == "" {
			fmt.Fprint(w, `{"data":{}}`)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{id: map[string]any{"type": "running"}}})
	})
	mux.HandleFunc("POST /api/session/{id}/compact", func(w http.ResponseWriter, r *http.Request) {
		if !auth(r) {
			http.Error(w, `{"_tag":"UnauthorizedError"}`, 401)
			return
		}
		var body struct {
			ID       string `json:"id"`
			Delivery string `json:"delivery"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request", 400)
			return
		}
		if body.Delivery != "" && body.Delivery != "steer" && body.Delivery != "queue" {
			http.Error(w, "bad delivery", 400)
			return
		}
		path := filepath.Join(os.Getenv("XDG_DATA_HOME"), "fake-session-"+r.PathValue("id")+".json")
		mu.Lock()
		defer mu.Unlock()
		raw, err := os.ReadFile(path)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		var items []map[string]any
		if json.Unmarshal(raw, &items) != nil {
			http.Error(w, "bad session", 500)
			return
		}
		now := time.Now().UnixMilli()
		items = append([]map[string]any{{"id": "msg_cmp_" + protocol.NewToken(), "type": "compaction", "status": "completed",
			"reason": "manual", "summary": "summary", "time": map[string]any{"created": now}}}, items...)
		raw, _ = json.Marshal(items)
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		record := map[string]any{"session_id": r.PathValue("id"), "delivery": body.Delivery, "at": now}
		recPath := filepath.Join(cwd, "fake-compact.json")
		var calls []map[string]any
		if b, err := os.ReadFile(recPath); err == nil {
			json.Unmarshal(b, &calls)
		}
		calls = append(calls, record)
		b, _ := json.Marshal(calls)
		os.WriteFile(recPath, b, 0o644)
		delivery := body.Delivery
		if delivery == "" {
			delivery = "steer"
		}
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"id": "msg_cmp_" + protocol.NewToken(), "sessionID": r.PathValue("id"),
			"time": map[string]any{"created": now}, "type": "compaction", "payload": map[string]any{},
			"delivery": delivery,
		}})
	})
	mux.HandleFunc("GET /api/session/{id}/message", func(w http.ResponseWriter, r *http.Request) {
		if !auth(r) {
			http.Error(w, `{"_tag":"UnauthorizedError"}`, 401)
			return
		}
		if os.Getenv("AGENT_NET_CTO") == "1" {
			mu.Lock()
			defer mu.Unlock()
			data, err := os.ReadFile(filepath.Join(os.Getenv("XDG_DATA_HOME"), "fake-session-"+r.PathValue("id")+".json"))
			if err != nil {
				http.NotFound(w, r)
				return
			}
			var items []map[string]any
			if json.Unmarshal(data, &items) != nil {
				http.Error(w, "bad session", 500)
				return
			}
			if r.URL.Query().Get("order") == "asc" || r.URL.Query().Get("cursor") != "" {
				for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
					items[i], items[j] = items[j], items[i]
				}
			}
			start, _ := strconv.Atoi(r.URL.Query().Get("cursor"))
			if start > len(items) {
				http.Error(w, "invalid cursor", 400)
				return
			}
			end := start + 2
			if end > len(items) {
				end = len(items)
			}
			next := ""
			if end < len(items) {
				next = strconv.Itoa(end)
			}
			json.NewEncoder(w).Encode(map[string]any{"data": items[start:end], "cursor": map[string]any{"next": next}})
			return
		}
		items := []map[string]any{
			{"id": "msg_assistant", "type": "assistant", "tokens": map[string]any{
				"input": 80000, "output": 2000, "reasoning": 0, "cache": map[string]any{"read": 3000, "write": 1000},
			}},
			{"id": "msg_user", "type": "user", "text": "task"},
		}
		if fakeOCFailed.Load() || fakeOCLocalFailed.Load() {
			typ, message := "provider.upstream", fakeOCError
			if fakeOCLocalFailed.Load() {
				typ, message = "effect/sql/SqlError", "Failed to execute statement"
			}
			items = append([]map[string]any{{"id": "msg_idle", "type": "idle", "outcome": "failed",
				"error": map[string]any{"type": typ, "message": message}}}, items...)
		}
		json.NewEncoder(w).Encode(map[string]any{"data": items})
	})
	mux.HandleFunc("POST /api/session", func(w http.ResponseWriter, r *http.Request) {
		if !auth(r) {
			http.Error(w, `{"_tag":"UnauthorizedError"}`, 401)
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		id := "ses_fake_" + strconv.Itoa(os.Getpid())
		if os.Getenv("AGENT_NET_CTO") == "1" {
			id = "ses_" + protocol.NewToken()
			if err := os.MkdirAll(os.Getenv("XDG_DATA_HOME"), 0o700); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			if err := os.WriteFile(filepath.Join(os.Getenv("XDG_DATA_HOME"), "fake-session-"+id+".json"), []byte("[]"), 0o600); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
		}
		mu.Lock()
		rec.SessionID, rec.SessionBody = id, body
		mu.Unlock()
		write()
		fmt.Fprintf(w, `{"data":{"id":%q}}`, id)
	})
	mux.HandleFunc("POST /api/session/{id}/agent", func(w http.ResponseWriter, r *http.Request) {
		if !auth(r) {
			http.Error(w, "unauthorized", 401)
			return
		}
		var p struct {
			Agent string `json:"agent"`
		}
		if json.NewDecoder(r.Body).Decode(&p) != nil || p.Agent != "cto" {
			http.Error(w, "bad agent", 400)
			return
		}
		if _, err := os.Stat(filepath.Join(os.Getenv("XDG_DATA_HOME"), "fake-session-"+r.PathValue("id")+".json")); err != nil {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		if rec.SessionBody == nil {
			rec.SessionBody = map[string]any{}
		}
		rec.SessionBody["agent"] = p.Agent
		rec.SessionID = r.PathValue("id")
		mu.Unlock()
		write()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /api/session/{id}/model", func(w http.ResponseWriter, r *http.Request) {
		if !auth(r) {
			http.Error(w, "unauthorized", 401)
			return
		}
		var body struct {
			Model map[string]any `json:"model"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.Model["providerID"] == nil || body.Model["id"] == nil {
			http.Error(w, "invalid model", 400)
			return
		}
		if _, err := os.Stat(filepath.Join(os.Getenv("XDG_DATA_HOME"), "fake-session-"+r.PathValue("id")+".json")); err != nil {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		if rec.SessionBody == nil {
			rec.SessionBody = map[string]any{}
		}
		rec.SessionBody["model"] = body.Model
		rec.SessionID = r.PathValue("id")
		mu.Unlock()
		write()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /api/session/{id}/prompt", func(w http.ResponseWriter, r *http.Request) {
		if !auth(r) {
			http.Error(w, "unauthorized", 401)
			return
		}
		var prompt struct {
			Text     string `json:"text"`
			Delivery string `json:"delivery"`
		}
		if json.NewDecoder(r.Body).Decode(&prompt) != nil || prompt.Text == "" {
			http.Error(w, "invalid prompt", 400)
			return
		}
		mu.Lock()
		rec.SessionID = r.PathValue("id")
		rec.Turns = append(rec.Turns, prompt.Text)
		mu.Unlock()
		write()
		mu.Lock()
		defer mu.Unlock()
		path := filepath.Join(os.Getenv("XDG_DATA_HOME"), "fake-session-"+r.PathValue("id")+".json")
		raw, err := os.ReadFile(path)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		var items []map[string]any
		if json.Unmarshal(raw, &items) != nil {
			http.Error(w, "bad session", 500)
			return
		}
		item := map[string]any{"id": "msg_" + protocol.NewToken(), "type": "user", "text": prompt.Text}
		items = append([]map[string]any{item}, items...)
		if prompt.Text == "FAKE:ctofail" {
			items = append([]map[string]any{{"type": "idle", "outcome": "failed", "error": map[string]any{"message": "unknown model"}}}, items...)
		}
		raw, _ = json.Marshal(items)
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"data": item})
	})
	mux.HandleFunc("POST /fake/cto-tool", func(w http.ResponseWriter, r *http.Request) {
		if !auth(r) {
			http.Error(w, "unauthorized", 401)
			return
		}
		var q struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if json.NewDecoder(r.Body).Decode(&q) != nil {
			http.Error(w, "bad request", 400)
			return
		}
		var config struct {
			MCP struct {
				Servers map[string]struct {
					Command     []string          `json:"command"`
					Environment map[string]string `json:"environment"`
				} `json:"servers"`
			} `json:"mcp"`
		}
		data, err := os.ReadFile(filepath.Join(cwd, "opencode.json"))
		if err != nil || json.Unmarshal(data, &config) != nil {
			http.Error(w, "bad config", 500)
			return
		}
		entry := config.MCP.Servers["agent-net-cto"]
		if len(entry.Command) == 0 {
			http.Error(w, "no MCP server", 500)
			return
		}
		cmd := exec.Command(entry.Command[0], entry.Command[1:]...)
		cmd.Env = workerEnv(os.Environ(), entry.Environment)
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		sess, err := mcp.NewClient(&mcp.Implementation{Name: "fake-opencode", Version: "test"}, nil).Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		defer sess.Close()
		out, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: q.Name, Arguments: q.Arguments})
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		json.NewEncoder(w).Encode(out)
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
func fakeOCPlugin(client, sessionID, cwd string) {
	var location atomic.Int64
	location.Store(1 << 60)
	fakeOCPluginTracked(client, sessionID, cwd, &location, func(string) {})
}

func fakeOCPluginTracked(client, sessionID, cwd string, location *atomic.Int64, turn func(string)) {
	var previous int64
	for {
		previous = fakeOCPluginOnce(client, sessionID, cwd, location, previous, turn)
		time.Sleep(50 * time.Millisecond)
	}
}

func fakeOCPluginOnce(client, sessionID, cwd string, location *atomic.Int64, previous int64, turn func(string)) int64 {
	c, err := net.Dial("unix", os.Getenv("AGENT_NET_SOCKET"))
	if err != nil {
		return previous
	}
	defer c.Close()
	generation := location.Load()
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
	send("hello", map[string]any{"client": client, "harness": client, "native_id": sessionID, "cwd": cwd, "status": "idle"})
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
		if location.Load() <= previous && location.Load() != 1<<60 {
			continue
		}
		turn(body)
		switch {
		case strings.Contains(body, "FAKE:ignore"):
		case strings.Contains(body, "FAKE:localfail"):
			fakeOCLocalFailed.Store(true)
		case strings.Contains(body, "FAKE:modelfail"):
			// The provider refuses at once: no reply, no hand-back.
			fakeOCFailed.Store(true)
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
				case <-time.After(scaled(5 * time.Second)):
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
			call := map[string]any{"report": "finished the task in " + cwd, "status": status}
			if strings.Contains(body, "FAKE:partial") {
				call["complete"] = false
			}
			send("worker_handback", call)
		case strings.Contains(body, "FAKE:edit"):
			fakeEdit(cwd)
			send("reply", map[string]any{"message_id": msg.ID, "body": "edited"})
		default:
			send("reply", map[string]any{"message_id": msg.ID, "body": "ok from worker (task was " + strconv.Itoa(len(body)) + " bytes)"})
		}
	}
	return generation
}
