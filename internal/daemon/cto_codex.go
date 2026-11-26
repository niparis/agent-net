package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/niparis/agent-net/internal/protocol"
)

// The CTO on Codex (decision 0171, phase 3). The daemon starts a private
// `codex app-server` for the CTO in <state>/cto/codex, as it does for a Codex
// worker, and drives one thread in it:
//
//   - The MCP server is the cto role server (`agent-net cto-mcp`), set with -c
//     on the app server. Its tools are allowed without asking. The owner's own
//     agent-net server is turned off, so the CTO has only its role identity.
//     The app server's pid is a root of the cto-mcp process-tree check.
//   - The thread runs with approval "never" and full access (decision 0157),
//     set on thread/start and thread/resume. A request for approval that still
//     arrives is accepted and logged: the CTO has full power, and nobody would
//     answer it.
//   - The CTO's skills reach the server through skills/extraRoots/set.
//   - Messages go in with thread/queue/add and carry their ID, so the delivery
//     confirm sees the user message item that takes them (codex_delivery.go).
//   - Codex compacts on its own. Its compaction item marks the start block due,
//     and the next turn carries it again (start_context.go).

// ctoCodexMCP is the CTO's MCP server name in Codex. Codex names a tool
// mcp__<server>__<tool>, as Claude Code does.
const ctoCodexMCP = "agent-net-cto"

// codexCTOState is the CTO's private Codex app server. The fields are guarded
// by cto.mu, except pid, which the process-tree check reads, and the turn
// state, which the read loop writes under nmu. cto.mu is held only to read and
// publish them, never across a call to the app server (B12): start serializes
// the start of the server and the load of its thread instead, and a caller
// that is cancelled leaves it.
type codexCTOState struct {
	start  ctoDispatchLock
	pid    atomic.Int64
	cmd    *exec.Cmd
	exited chan struct{}
	run    *ctoProcess
	addr   string
	token  string
	rpc    *codexRPC
	thread string // the thread loaded in this server

	nmu   sync.Mutex
	busy  bool
	taken map[string]bool // queued message ID -> a turn took it
}

type codexCTOHarness struct{}

func (codexCTOHarness) name() string { return "codex" }
func (codexCTOHarness) send(ctx context.Context, d *Daemon, text string) (any, error) {
	return d.codexCTOSend(ctx, text)
}
func (codexCTOHarness) sendSystem(ctx context.Context, d *Daemon, label, text string) (any, error) {
	return d.codexCTOSend(ctx, ctoSystemPromptText(label, text))
}
func (codexCTOHarness) newConversation(ctx context.Context, d *Daemon) (any, error) {
	return d.codexCTONew(ctx)
}
func (codexCTOHarness) events(ctx context.Context, d *Daemon, index int) (protocol.CTOEvents, error) {
	return d.codexCTOEvents(ctx, index)
}

func (d *Daemon) codexCTODir() string { return filepath.Join(d.cfg.StateDir, string(roleCTO), "codex") }

// codexCTOArgs is the app server command line. The values after -c are TOML;
// a JSON string is a TOML basic string.
func (d *Daemon) codexCTOArgs(addr, tokenPath string) []string {
	q := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	server := "mcp_servers." + ctoCodexMCP
	args := []string{"app-server", "--listen", "ws://" + addr, "--ws-auth", "capability-token", "--ws-token-file", tokenPath,
		"-c", server + ".command=" + q(d.cfg.Exe),
		"-c", server + `.args=["cto-mcp"]`,
		"-c", server + ".env={AGENT_NET_SOCKET=" + q(d.cfg.SocketPath) + `,AGENT_NET_CTO="1"}`,
		"-c", server + `.default_tools_approval_mode="approve"`}
	if codexConfigDefinesAgentNet(codexHome(d.cfg)) {
		args = append(args, "-c", "mcp_servers.agent-net.enabled=false")
	}
	return args
}

// codexCTOConn starts, or keeps, the CTO's app server and returns its
// connection. With load, it also loads the CTO's thread: the stored one,
// resumed, or a new one (fresh).
func (d *Daemon) codexCTOConn(ctx context.Context, model string, load bool) (rpc *codexRPC, thread string, fresh bool, err error) {
	c := &d.codexCTO
	if err = c.start.lock(ctx); err != nil {
		return nil, "", false, err
	}
	defer c.start.unlock()
	d.cto.mu.Lock()
	if c.cmd != nil {
		select {
		case <-c.exited:
			d.stopCodexCTOLocked("crash")
		default:
			if c.rpc == nil {
				d.stopCodexCTOLocked("start_failed")
			} else if c.rpc.isDead() {
				d.stopCodexCTOLocked("connection_lost")
			}
		}
	}
	rpc, thread = c.rpc, c.thread
	d.cto.mu.Unlock()
	if rpc == nil {
		if rpc, err = d.spawnCodexCTO(ctx); err != nil {
			return nil, "", false, err
		}
	}
	if !load || thread != "" {
		return rpc, thread, false, nil
	}
	if thread, fresh, err = d.loadCodexCTOThread(ctx, rpc, model); err != nil {
		return nil, "", false, err
	}
	d.cto.mu.Lock()
	same := c.rpc == rpc
	if same {
		c.thread = thread
	}
	d.cto.mu.Unlock()
	if !same {
		return nil, "", false, errors.New("the CTO's Codex server stopped while its thread loaded")
	}
	d.setCodexSkillRoots(ctx, rpc, skillNode{Agent: "cto"})
	return rpc, thread, fresh, nil
}

// spawnCodexCTO starts the app server and connects to it. c.start is held.
func (d *Daemon) spawnCodexCTO(ctx context.Context) (*codexRPC, error) {
	c := &d.codexCTO
	bin, err := d.harnessBinary("codex")
	if err != nil {
		return nil, err
	}
	dir := d.codexCTODir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	token := protocol.NewToken()
	tokenPath := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenPath, []byte(token), 0o600); err != nil {
		return nil, err
	}
	logf, err := os.OpenFile(filepath.Join(dir, "codex.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	defer logf.Close()
	env := map[string]string{"AGENT_NET_CTO": "1", "AGENT_NET_SOCKET": d.cfg.SocketPath, "AGENT_NET_WORKER": "", "AGENT_NET_WORKER_SESSION_FILE": ""}
	if d.cfg.CodexHome != "" {
		env["CODEX_HOME"] = d.cfg.CodexHome
	}
	for attempt := 0; attempt < 3; attempt++ {
		port, err := d.workerFreePort()
		if err != nil {
			return nil, err
		}
		addr := "127.0.0.1:" + strconv.Itoa(port)
		cmd := exec.Command(bin, d.codexCTOArgs(addr, tokenPath)...)
		cmd.Dir = dir
		cmd.Env = workerEnv(os.Environ(), env)
		cmd.Stdout, cmd.Stderr = logf, logf
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := cmd.Start(); err != nil {
			return nil, fmt.Errorf("start CTO Codex: %w", err)
		}
		run := &ctoProcess{}
		exited := make(chan struct{})
		d.cto.mu.Lock()
		c.cmd, c.exited, c.run, c.addr, c.token = cmd, exited, run, addr, token
		c.pid.Store(int64(cmd.Process.Pid))
		d.cto.mu.Unlock()
		go d.waitCodexCTO(cmd, run, exited)
		up := func() bool {
			select {
			case <-exited:
				return false
			default:
				return true
			}
		}
		rpc, err := dialCodexWS(ctx, addr, token, d.helloVersion(), up, d.codexCTONote, d.codexCTOAnswer)
		d.cto.mu.Lock()
		if err == nil && c.cmd == cmd {
			c.rpc = rpc
			run.started.Store(true)
			d.cto.mu.Unlock()
			d.Logf("cto.start harness=codex")
			d.scheduleCTOPresence()
			return rpc, nil
		}
		if err == nil {
			// A switch stopped the server while it was dialled.
			d.cto.mu.Unlock()
			rpc.close()
			return nil, errors.New("the CTO's Codex server was stopped while it started")
		}
		if c.cmd == cmd {
			d.stopCodexCTOLocked("start_failed")
		}
		d.cto.mu.Unlock()
		if !errors.Is(err, errCodexExited) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("CTO Codex: %w", errCodexExited)
}

// waitCodexCTO reaps the app server and logs a crash.
func (d *Daemon) waitCodexCTO(cmd *exec.Cmd, run *ctoProcess, exited chan struct{}) {
	err := cmd.Wait()
	d.codexCTO.pid.CompareAndSwap(int64(cmd.Process.Pid), 0)
	if !run.expected.Load() {
		// Reap the MCP servers left in the private process group.
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if run.started.Load() {
			d.Logf("cto.crash harness=codex error=%v", err)
		}
	}
	close(exited)
	d.scheduleCTOPresence()
}

// stopCodexCTOLocked stops the app server and its MCP servers. The thread
// stays on disk, so the next start resumes it. The caller holds cto.mu.
func (d *Daemon) stopCodexCTOLocked(reason string) {
	c := &d.codexCTO
	if c.cmd == nil {
		return
	}
	intentional := false
	select {
	case <-c.exited:
	default:
		intentional = true
		c.run.expected.Store(true)
		_ = syscall.Kill(-c.cmd.Process.Pid, syscall.SIGKILL)
		<-c.exited
	}
	if c.rpc != nil {
		c.rpc.close()
	}
	if intentional && c.run.started.Load() {
		d.Logf("cto.stop harness=codex reason=%s", reason)
	}
	c.pid.Store(0)
	c.cmd, c.exited, c.run, c.rpc = nil, nil, nil, nil
	c.addr, c.token, c.thread = "", "", ""
	c.nmu.Lock()
	c.busy, c.taken = false, nil
	c.nmu.Unlock()
	d.scheduleCTOPresence()
}

// codexCTORunningLocked says whether the app server is alive. The caller
// holds cto.mu.
func (d *Daemon) codexCTORunningLocked() bool {
	c := &d.codexCTO
	if c.cmd == nil {
		return false
	}
	select {
	case <-c.exited:
		return false
	default:
		return true
	}
}

// codexCTOAnswer answers a request of the app server. With approval "never" none
// should come; one that does is accepted, since the CTO has full power and
// nobody watches its thread to answer.
func (d *Daemon) codexCTOAnswer(method string, _ json.RawMessage) any {
	var result any
	switch method {
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval":
		result = map[string]any{"decision": "accept"}
	case "execCommandApproval", "applyPatchApproval":
		result = map[string]any{"decision": "approved"}
	}
	d.Logf("cto.codex_request method=%s answered=%t", method, result != nil)
	return result
}

// codexCTONote reads the notifications of the CTO's thread: when a turn runs,
// which queued message a turn took, and a compaction.
func (d *Daemon) codexCTONote(method string, raw json.RawMessage) {
	c := &d.codexCTO
	var p struct {
		ThreadID string `json:"threadId"`
		Item     struct {
			Type     string  `json:"type"`
			ClientID *string `json:"clientId"`
		} `json:"item"`
	}
	if json.Unmarshal(raw, &p) != nil {
		return
	}
	switch method {
	case "turn/started":
		c.nmu.Lock()
		c.busy = true
		c.nmu.Unlock()
	case "turn/completed":
		c.nmu.Lock()
		c.busy = false
		c.nmu.Unlock()
	case "item/started":
		if p.Item.Type == "userMessage" && p.Item.ClientID != nil {
			c.nmu.Lock()
			if _, ok := c.taken[*p.Item.ClientID]; ok {
				c.taken[*p.Item.ClientID] = true
			}
			c.nmu.Unlock()
		}
	case "item/completed":
		if p.Item.Type == "contextCompaction" {
			d.noteCompacted("codex", p.ThreadID, d.ctoNowTime().Unix(), "codex")
			d.markStartContextDue(d.ctoSessionID())
		}
	case "thread/compacted":
		d.markStartContextDue(d.ctoSessionID())
	}
}

// codexCTOThreadParams are the thread/start and thread/resume fields of the
// CTO: its folder, full access with no approvals, its brief and start block,
// and its model.
func (d *Daemon) codexCTOThreadParams(ctx context.Context, model string) map[string]any {
	params := map[string]any{
		"cwd":                   d.codexCTODir(),
		"approvalPolicy":        "never",
		"sandbox":               "danger-full-access",
		"developerInstructions": d.ctoSystemPrompt(ctx),
	}
	name, effort := splitCodexModel(model)
	if name != "" {
		params["model"] = name
	}
	if effort != "" {
		params["config"] = map[string]string{"model_reasoning_effort": effort}
	}
	return params
}

// codexCTOModel is the model of the CTO's Codex thread; empty is Codex's own
// default.
func (d *Daemon) codexCTOModel(ctx context.Context) string {
	settings, err := d.ctoSettings(ctx, protocol.CTOSettingsRequest{})
	if err != nil {
		return ""
	}
	return settings.CodexModel
}

// loadCodexCTOThread resumes the stored thread in the server, or starts a new
// one when there is none or Codex lost it.
func (d *Daemon) loadCodexCTOThread(ctx context.Context, rpc *codexRPC, model string) (thread string, fresh bool, err error) {
	stored, err := d.ctoConversation("codex")
	if err != nil {
		return "", false, err
	}
	params := d.codexCTOThreadParams(ctx, model)
	if stored != "" {
		params["threadId"] = stored
		raw, err := rpc.call(ctx, "thread/resume", params)
		if err == nil {
			d.recordCodexCTOModelFromAnswer(ctx, raw)
			return stored, false, nil
		}
		d.Logf("cto.codex resume of %s failed, a new thread starts: %v", stored, err)
		delete(params, "threadId")
	}
	raw, err := rpc.call(ctx, "thread/start", params)
	if err != nil {
		return "", false, fmt.Errorf("codex refused the CTO thread: %w", err)
	}
	var started struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if json.Unmarshal(raw, &started) != nil || started.Thread.ID == "" {
		return "", false, fmt.Errorf("codex answered without a thread ID: %s", truncate(string(raw), 200))
	}
	tx, err := d.st.db.Begin()
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback()
	if err = setCTOConversationExec(tx, d.ctoNowTime(), "codex", started.Thread.ID); err != nil {
		return "", false, err
	}
	if _, err = tx.Exec(`UPDATE cto_actions SET session_id=? WHERE session_id='' AND harness='codex'`, started.Thread.ID); err != nil {
		return "", false, err
	}
	if err = tx.Commit(); err != nil {
		return "", false, err
	}
	d.recordCodexCTOModelFromAnswer(ctx, raw)
	return started.Thread.ID, true, nil
}

// codexThreadModel reads the model in a thread/start or thread/resume answer.
// The protocol names it at the top of the answer; the thread object carries it
// too, so the thread's own model is the fallback.
func codexThreadModel(raw json.RawMessage) string {
	var answer struct {
		Model  string `json:"model"`
		Thread struct {
			Model string `json:"model"`
		} `json:"thread"`
	}
	if json.Unmarshal(raw, &answer) != nil {
		return ""
	}
	if answer.Model != "" {
		return answer.Model
	}
	return answer.Thread.Model
}

// recordCodexCTOModelFromAnswer stores the model the app server reports when
// the saved Codex model is empty, so the CTO's record and the owner's screen
// name what the thread really runs on.
func (d *Daemon) recordCodexCTOModelFromAnswer(ctx context.Context, raw json.RawMessage) {
	if model := codexThreadModel(raw); model != "" {
		if err := d.recordCodexCTOModel(ctx, model); err != nil {
			d.Logf("cto.codex model record: %v", err)
		}
	}
}

// recordCodexCTOModel fills an empty Codex model with the one the app server
// reports. A model the owner chose is never replaced. The setting is updated
// too when the list's first entry gained the model, so the owner reads the
// same entry the CTO runs.
func (d *Daemon) recordCodexCTOModel(ctx context.Context, model string) error {
	model = strings.TrimSpace(model)
	if model == "" {
		return nil
	}
	perHarness, harness, err := d.localCTOModelSettings()
	if err != nil {
		return err
	}
	if perHarness.Codex != "" {
		return nil
	}
	tx, err := d.st.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE cto SET codex_model=? WHERE id=1`, model); err != nil {
		return err
	}
	var raw string
	if err = tx.QueryRow(`SELECT models FROM cto WHERE id=1`).Scan(&raw); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var models []protocol.RoleModel
	if raw != "" {
		models, err = protocol.ParseRoleModels(raw)
		if err != nil {
			return err
		}
		for i := range models {
			if models[i].Harness == "codex" && models[i].Model == "" {
				models[i].Model = model
				models[i].Provider = protocol.ProfileProvider("codex", model)
				if _, err = tx.Exec(`UPDATE cto SET models=? WHERE id=1`, protocol.FormatRoleModels(models)); err != nil {
					return err
				}
				break
			}
		}
	}
	if harness == "codex" {
		if _, err = tx.Exec(`UPDATE cto SET active_model=? WHERE id=1`, model); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if harness == "codex" && len(models) > 0 && models[0].Harness == "codex" && models[0].Model == model {
		if err := d.writeCTOModelsSetting(ctx, models); err != nil {
			d.Logf("cto.codex model setting: %v", err)
		}
	}
	return nil
}

// codexCTOSend queues one message in the CTO's thread and starts its delivery
// confirm.
func (d *Daemon) codexCTOSend(ctx context.Context, text string) (any, error) {
	rpc, thread, fresh, err := d.codexCTOConn(ctx, d.codexCTOModel(ctx), true)
	if err != nil {
		return nil, err
	}
	// A new thread has the start block in its developer instructions. After a
	// compaction the next turn carries it again.
	if d.takeStartContextDue(d.ctoSessionID()) && !fresh {
		text = prependStartBlock(d.ctoStartBlock(ctx), text)
	}
	msgID, _ := ctx.Value(ctoMessageIDKey{}).(string)
	if msgID == "" {
		msgID = protocol.NewToken()
	}
	c := &d.codexCTO
	c.nmu.Lock()
	if c.taken == nil {
		c.taken = map[string]bool{}
	}
	c.taken[msgID] = false
	c.nmu.Unlock()
	if err := codexQueueAdd(ctx, rpc, thread, msgID, text); err != nil {
		c.nmu.Lock()
		delete(c.taken, msgID)
		c.nmu.Unlock()
		return nil, fmt.Errorf("codex refused the message: %w", err)
	}
	if err := d.touchCTOConversation("codex"); err != nil {
		return nil, err
	}
	d.confirmCodexCTODelivery(rpc, thread, msgID, text)
	return map[string]string{"session_id": thread}, nil
}

// confirmCodexCTODelivery watches one queued message: the user message item
// that carries its ID is the turn that took it.
func (d *Daemon) confirmCodexCTODelivery(rpc *codexRPC, thread, msgID, text string) {
	c := &d.codexCTO
	same := func() bool {
		d.cto.mu.Lock()
		defer d.cto.mu.Unlock()
		return c.rpc == rpc && c.thread == thread
	}
	d.confirmDelivery("cto", deliveryWatch{
		label: "cto",
		msgID: msgID,
		gone:  func() bool { return !same() },
		busy: func(context.Context) bool {
			c.nmu.Lock()
			defer c.nmu.Unlock()
			return c.busy
		},
		taken: func(context.Context) bool {
			c.nmu.Lock()
			defer c.nmu.Unlock()
			return c.taken[msgID]
		},
		redeliver: func(ctx context.Context) bool {
			return same() && codexQueueAdd(ctx, rpc, thread, msgID, text) == nil
		},
		restart: func(ctx context.Context) {
			if _, err := rpc.call(ctx, "turn/start", map[string]any{"threadId": thread,
				"input": []map[string]any{{"type": "text", "text": text, "text_elements": []any{}}}}); err != nil {
				d.Logf("cto.delivery_unconfirmed msg=%s reason=restart_failed detail=%q", msgID, err.Error())
			}
		},
		done: func() {
			c.nmu.Lock()
			delete(c.taken, msgID)
			c.nmu.Unlock()
		},
	})
}

// codexCTONew ends the Codex conversation. The next message starts a new
// thread. The gateway call runs without cto.mu (B12).
func (d *Daemon) codexCTONew(ctx context.Context) (any, error) {
	data, err := d.call(ctx, "cto.graph_latest", nil)
	if err != nil {
		return nil, err
	}
	var latest struct {
		ID int64 `json:"id"`
	}
	if err = json.Unmarshal(data, &latest); err != nil {
		return nil, err
	}
	if err := d.endCTOConversation("codex", latest.ID); err != nil {
		return nil, err
	}
	d.cto.mu.Lock()
	d.codexCTO.thread = ""
	d.cto.mu.Unlock()
	return struct{}{}, nil
}

// codexCTOEvents reads the CTO's thread as the timeline: each user message and
// each answer. It starts the app server only when a thread is stored.
func (d *Daemon) codexCTOEvents(ctx context.Context, index int) (protocol.CTOEvents, error) {
	out := protocol.CTOEvents{Messages: []json.RawMessage{}, Actions: []protocol.CTOAction{}}
	out.Model = d.codexCTOModel(ctx)
	if err := d.st.db.QueryRow(`SELECT graph_after FROM cto WHERE id=1`).Scan(&out.GraphAfter); err != nil {
		return out, err
	}
	thread, err := d.ctoConversation("codex")
	if err != nil {
		return out, err
	}
	if out.Actions, err = d.ctoActions(thread, "codex"); err != nil {
		return out, err
	}
	if thread == "" {
		return out, nil
	}
	rpc, _, _, err := d.codexCTOConn(ctx, "", false)
	if err != nil {
		return out, err
	}
	return out, readCodexCTOThread(ctx, rpc, thread, index, &out)
}

// codexCTORunningEvents reads the thread of a Codex CTO whose app server is
// already up, for the save before an owner stop. It never starts the server
// and holds no lock while it waits for the answer.
func (d *Daemon) codexCTORunningEvents(ctx context.Context) (protocol.CTOEvents, error) {
	out := protocol.CTOEvents{Messages: []json.RawMessage{}, Actions: []protocol.CTOAction{}}
	d.cto.mu.Lock()
	running, rpc := d.codexCTORunningLocked(), d.codexCTO.rpc
	d.cto.mu.Unlock()
	if !running {
		return out, errCTONotRunning
	}
	if rpc == nil || rpc.isDead() {
		return out, errors.New("the CTO's Codex server has no connection to read its thread")
	}
	thread, err := d.ctoConversation("codex")
	if err != nil {
		return out, err
	}
	if out.Actions, err = d.ctoActions(thread, "codex"); err != nil {
		return out, err
	}
	if thread == "" {
		return out, nil
	}
	return out, readCodexCTOThread(ctx, rpc, thread, 0, &out)
}

// readCodexCTOThread fills out with the thread's messages from index on.
func readCodexCTOThread(ctx context.Context, rpc *codexRPC, thread string, index int, out *protocol.CTOEvents) error {
	raw, err := rpc.call(ctx, "thread/read", map[string]any{"threadId": thread, "includeTurns": true})
	if err != nil {
		return err
	}
	messages := codexThreadMessages(raw)
	out.Next = len(messages)
	if index < len(messages) {
		out.Messages = messages[index:]
	}
	return nil
}

// codexThreadMessages turns a thread/read answer into the timeline form the
// CTO screen reads: {"type":"user"|"assistant","text":...}.
func codexThreadMessages(raw json.RawMessage) []json.RawMessage {
	var read struct {
		Thread struct {
			Turns []struct {
				Items []struct {
					Type    string `json:"type"`
					Text    string `json:"text"`
					Content []struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"content"`
				} `json:"items"`
			} `json:"turns"`
		} `json:"thread"`
	}
	out := []json.RawMessage{}
	if json.Unmarshal(raw, &read) != nil {
		return out
	}
	for _, turn := range read.Thread.Turns {
		for _, item := range turn.Items {
			kind, text := "", ""
			switch item.Type {
			case "userMessage":
				kind = "user"
				for _, c := range item.Content {
					text += c.Text
				}
			case "agentMessage":
				kind, text = "assistant", item.Text
			}
			if kind == "" || strings.TrimSpace(text) == "" {
				continue
			}
			b, _ := json.Marshal(map[string]any{"type": kind, "text": text})
			out = append(out, b)
		}
	}
	return out
}

// codexCTOEnterPlan is the command that shows the CTO's thread: the Codex
// client joins the private app server, as for a Codex worker. The token is in
// the environment, not on the command line.
func (d *Daemon) codexCTOEnterPlan(ctx context.Context, bin string) (protocol.EnterPlan, error) {
	plan := protocol.EnterPlan{Note: "Press ctrl+] to come back here; the CTO keeps running."}
	_, thread, _, err := d.codexCTOConn(ctx, d.codexCTOModel(ctx), true)
	if err != nil {
		return plan, err
	}
	d.cto.mu.Lock()
	addr, token := d.codexCTO.addr, d.codexCTO.token
	d.cto.mu.Unlock()
	plan.Argv = []string{bin, "resume", thread, "--remote", "ws://" + addr, "--remote-auth-token-env", "AGENT_NET_CODEX_TOKEN"}
	plan.Env = map[string]string{"AGENT_NET_CODEX_TOKEN": token}
	if d.cfg.CodexHome != "" {
		plan.Env["CODEX_HOME"] = d.cfg.CodexHome
	}
	plan.Dir = d.codexCTODir()
	return plan, nil
}

// codexCTOLocalWork says whether the Codex CTO is in a busy turn.
func (d *Daemon) codexCTOLocalWork() (bool, string) {
	d.cto.mu.Lock()
	running := d.codexCTORunningLocked()
	d.cto.mu.Unlock()
	if !running {
		return false, "no Codex server running"
	}
	c := &d.codexCTO
	c.nmu.Lock()
	defer c.nmu.Unlock()
	if c.busy {
		return true, "busy turn in Codex"
	}
	return false, "no busy turn in Codex"
}
