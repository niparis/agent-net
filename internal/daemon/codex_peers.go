package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Interactive Codex sessions as peers (decision 0012).
//
// Codex windows run their threads on a shared local app server (the "daemon",
// `daemon_auto_start`). This adapter is a read-only client of it: it lists the
// threads the server holds in memory, which are the ones open in a window (the
// server drops a thread with no window and no activity after a delay), and
// registers each as a session. A message is delivered with `thread/queue/add`,
// which the window shows as a message from the user. The adapter never starts or
// resumes a thread, so it never subscribes: it gets no approval requests and
// does not keep a thread loaded.
//
// The same type delivers to Codex workers, which live in a private app server
// (worker_codex.go) and are not in the shared server's list.
type codexAdapter struct {
	d *Daemon

	interval time.Duration // how often the loaded threads are listed
	retry    time.Duration // how often a missing server is tried again

	mu      sync.Mutex
	rpc     *codexRPC
	threads map[string]codexThread
	started bool
	kick    chan struct{}
}

// codexThread is what the adapter keeps of one open thread.
type codexThread struct {
	ID, Cwd, Name, Model, Status, Mode, Path string
	Activity                                 int64
	Wait                                     *protocol.Waiting // from the thread's active flags
}

func newCodexAdapter(d *Daemon) *codexAdapter {
	interval := d.cfg.CodexInterval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return &codexAdapter{d: d, interval: interval, retry: min(interval, 3*time.Second),
		threads: map[string]codexThread{}, kick: make(chan struct{}, 1)}
}

func (a *codexAdapter) Harness() string { return "codex" }

// Start runs the discovery loop. It does nothing without a Codex home folder.
func (a *codexAdapter) Start(ctx context.Context, _ Host) error {
	if a.d.cfg.CodexHome == "" {
		return nil
	}
	a.mu.Lock()
	if a.started {
		a.mu.Unlock()
		return nil
	}
	a.started = true
	a.mu.Unlock()
	a.d.wg.Add(1)
	go func() { defer a.d.wg.Done(); a.loop(ctx) }()
	return nil
}

func (a *codexAdapter) loop(ctx context.Context) {
	sock := codexSocketPath(a.d.cfg.CodexHome)
	lastErr := ""
	for ctx.Err() == nil {
		rpc, err := dialCodexSocket(ctx, sock, a.d.helloVersion(), a.onNote)
		if err != nil {
			// One line per change, so a machine without a Codex daemon is quiet.
			if msg := err.Error(); msg != lastErr {
				a.d.Logf("codex: no shared app server at %s yet (%v); trying again", sock, err)
				lastErr = msg
			}
			sleepCtx(ctx, a.retry)
			continue
		}
		lastErr = ""
		a.d.Logf("codex: connected to the shared app server (pid %d)", rpc.pid)
		a.mu.Lock()
		a.rpc = rpc
		a.mu.Unlock()
		a.watch(ctx, rpc)
		rpc.close()
		a.mu.Lock()
		a.rpc, a.threads = nil, map[string]codexThread{}
		a.mu.Unlock()
		a.d.SetSessions("codex", nil)
		if ctx.Err() == nil {
			a.d.Logf("codex: lost the shared app server; trying again")
		}
	}
}

// defaultCodexHome is ~/.codex, where Codex keeps its files when CODEX_HOME is
// not set.
func defaultCodexHome() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(h, ".codex")
}

func sleepCtx(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// onNote reacts to the notifications every client gets: a thread started, a
// thread changed status, a thread got a name.
func (a *codexAdapter) onNote(method string, raw json.RawMessage) {
	if strings.HasPrefix(method, "item/") || strings.HasPrefix(method, "turn/") {
		var p struct {
			ThreadID string `json:"threadId"`
		}
		if json.Unmarshal(raw, &p) == nil && p.ThreadID != "" {
			a.d.mu.Lock()
			ls := a.d.sessions[key("codex", p.ThreadID)]
			a.d.mu.Unlock()
			if ls != nil {
				a.d.touchHarness(ls, time.Now())
			}
		}
	}
	if method == "item/started" {
		a.onUserMessage(raw)
	}
	if method == "turn/started" {
		var p struct {
			ThreadID string `json:"threadId"`
		}
		if json.Unmarshal(raw, &p) == nil && p.ThreadID != "" {
			a.d.mu.Lock()
			ls := a.d.sessions[key("codex", p.ThreadID)]
			a.d.mu.Unlock()
			if ls != nil {
				a.d.setWaiting(ls, nil)
				a.d.mu.Lock()
				ls.turnStartedLocked()
				a.d.mu.Unlock()
			}
		}
	}
	if method == "turn/completed" {
		a.turnCompleted(raw)
	}
	if method == "thread/archived" {
		var p struct {
			ThreadID string `json:"threadId"`
		}
		if json.Unmarshal(raw, &p) == nil && p.ThreadID != "" {
			if sid := a.d.SessionID("codex", p.ThreadID); sid != "" {
				go a.d.call(context.Background(), "session.archive", protocol.SessionRemove{SessionID: sid})
			}
		}
	}

	switch method {
	case "thread/started", "thread/closed", "thread/status/changed", "thread/name/updated", "thread/archived", "thread/deleted":
		select {
		case a.kick <- struct{}{}:
		default:
		}
	}
}

// turnCompleted reads how a turn ended. A failed turn with a usage limit
// limits the thread; a completed one clears the limit (limits.go).
func (a *codexAdapter) turnCompleted(raw json.RawMessage) {
	var p struct {
		ThreadID string `json:"threadId"`
		Turn     struct {
			Status string `json:"status"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		} `json:"turn"`
	}
	if json.Unmarshal(raw, &p) != nil || p.ThreadID == "" {
		return
	}
	a.d.mu.Lock()
	ls := a.d.sessions[key("codex", p.ThreadID)]
	a.d.mu.Unlock()
	if ls == nil {
		return
	}
	switch {
	case p.Turn.Error != nil:
		a.d.turnFailed(ls, p.Turn.Error.Message)
	case p.Turn.Status == "completed":
		a.d.turnSucceeded(ls)
	}
}

// watch lists the threads now, then again on every tick and every notification,
// until the connection ends.
func (a *codexAdapter) watch(ctx context.Context, rpc *codexRPC) {
	tick := time.NewTicker(a.interval)
	defer tick.Stop()
	for {
		if err := a.refresh(ctx, rpc); err != nil && ctx.Err() == nil {
			a.d.Logf("codex: listing threads: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-rpc.dead:
			return
		case <-tick.C:
		case <-a.kick:
			sleepCtx(ctx, 100*time.Millisecond) // several changes often come together
			select {
			case <-a.kick:
			default:
			}
		}
	}
}

// codexThreadRead is the part of a thread/read answer that matters here.
type codexThreadRead struct {
	Thread struct {
		ID             string          `json:"id"`
		Cwd            string          `json:"cwd"`
		Name           *string         `json:"name"`
		Model          *string         `json:"model"`
		Ephemeral      bool            `json:"ephemeral"`
		Path           *string         `json:"path"`
		ParentThreadID *string         `json:"parentThreadId"`
		Source         json.RawMessage `json:"source"`
		Status         struct {
			Type        string   `json:"type"`
			ActiveFlags []string `json:"activeFlags"`
		} `json:"status"`
	} `json:"thread"`
}

// codexWait is the wait an active thread's flags say (decision 0057): Codex
// marks a thread that waits for an approval or for the user's answer. The
// flags do not carry the question, so the text says what kind of answer.
func codexWait(flags []string) *protocol.Waiting {
	for _, f := range flags {
		switch f {
		case "waitingOnApproval":
			return &protocol.Waiting{Kind: protocol.WaitingPermission, Text: "approve a command or a change"}
		case "waitingOnUserInput":
			return &protocol.Waiting{Kind: protocol.WaitingQuestion, Text: "answer a question"}
		}
	}
	return nil
}

// codexSource tells where a thread came from: "cli", "vscode", "exec",
// "appServer", "custom", "subAgent" or "unknown".
func codexSource(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var o map[string]json.RawMessage
	if json.Unmarshal(raw, &o) == nil {
		for k := range o {
			return k
		}
	}
	return "unknown"
}

func (a *codexAdapter) refresh(ctx context.Context, rpc *codexRPC) error {
	// Codex threads have no process of their own: they all run on the shared
	// app server. Its environment is the one the daemon can read, so the flag
	// there keeps its threads off the network.
	if a.d.sessionIgnored(rpc.pid) {
		a.mu.Lock()
		a.threads = map[string]codexThread{}
		a.mu.Unlock()
		a.d.SetSessions("codex", nil)
		return nil
	}
	var ids []string
	cursor := ""
	for page := 0; page < 20; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		raw, err := rpc.call(cctx, "thread/loaded/list", params)
		cancel()
		if err != nil {
			return err
		}
		var r struct {
			Data       []string `json:"data"`
			NextCursor *string  `json:"nextCursor"`
		}
		if err := json.Unmarshal(raw, &r); err != nil {
			return err
		}
		ids = append(ids, r.Data...)
		if r.NextCursor == nil || *r.NextCursor == "" {
			break
		}
		cursor = *r.NextCursor
	}
	threads := map[string]codexThread{}
	for _, id := range ids {
		if a.d.workerByNative("codex", id) != nil {
			continue // a worker is in its own server, and is registered by the worker code
		}
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		raw, err := rpc.call(cctx, "thread/read", map[string]any{"threadId": id, "includeTurns": false})
		cancel()
		if err != nil {
			continue // unloaded since the list
		}
		var tr codexThreadRead
		if json.Unmarshal(raw, &tr) != nil || tr.Thread.ID == "" {
			continue
		}
		t := tr.Thread
		src := codexSource(t.Source)
		if t.Ephemeral || t.ParentThreadID != nil || src == "subAgent" {
			continue // a sub-agent or a scratch thread is not a peer
		}
		th := codexThread{ID: id, Cwd: t.Cwd, Status: "idle", Mode: protocol.ModeUnknown}
		if t.Name != nil {
			// The thread name is a title for the TASK column, not the
			// session's network name (decision 0024).
			th.Name = protocol.CleanModel(*t.Name)
		}
		if t.Model != nil {
			th.Model = protocol.CleanModel(*t.Model)
		}
		switch t.Status.Type {
		case "active":
			th.Status = "busy"
			th.Wait = codexWait(t.Status.ActiveFlags)
		case "systemError":
			th.Status = "unknown"
		}
		// The daemon cannot see whether a window is still attached to a thread
		// from the terminal or the IDE, so it does not say foreground. Threads
		// that a program drives are background.
		if src == "exec" || src == "appServer" || src == "custom" {
			th.Mode = protocol.ModeBackground
		}
		if t.Path != nil && *t.Path != "" {
			th.Path = *t.Path
			if fi, err := os.Stat(th.Path); err == nil {
				th.Activity = fi.ModTime().Unix()
			}
		}
		threads[id] = th
	}
	a.mu.Lock()
	a.threads = threads
	a.mu.Unlock()
	keys := make([]string, 0, len(threads))
	for id := range threads {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	infos := make([]protocol.SessionInfo, 0, len(keys))
	for _, id := range keys {
		t := threads[id]
		infos = append(infos, protocol.SessionInfo{Harness: "codex", NativeID: id, Cwd: t.Cwd, Name: t.Name,
			Status: t.Status, Delivery: "codex-app-server", Mode: t.Mode, Model: t.Model, Waiting: t.Wait, HarnessWaits: t.Wait != nil, HarnessActivity: t.Activity})
	}
	a.d.SetSessions("codex", infos)
	return nil
}

// serverPID is the process of the shared app server that this adapter is
// connected to, read from the socket by the kernel. 0 when not connected.
func (a *codexAdapter) serverPID() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.rpc == nil {
		return 0
	}
	return a.rpc.pid
}

// isThread says whether the adapter has discovered this open thread.
func (a *codexAdapter) isThread(id string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, ok := a.threads[id]
	return ok
}

// thread returns what the adapter knows about one open thread.
func (a *codexAdapter) thread(id string) (codexThread, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	t, ok := a.threads[id]
	return t, ok
}

// compact asks the shared app server to compact one thread's context. Codex
// runs the compaction as a turn on the thread.
func (a *codexAdapter) compact(ctx context.Context, id string) error {
	a.mu.Lock()
	rpc := a.rpc
	_, ok := a.threads[id]
	a.mu.Unlock()
	if rpc == nil {
		return fmt.Errorf("agent-net is not connected to the Codex app server")
	}
	if !ok {
		return fmt.Errorf("the Codex thread is not open any more")
	}
	_, err := rpc.call(ctx, "thread/compact/start", map[string]any{"threadId": id})
	return err
}

// Deliver puts the message in the thread's queue.
func (a *codexAdapter) Deliver(ctx context.Context, target protocol.SessionInfo, m protocol.Message) Result {
	if w := a.d.workerByNative("codex", target.NativeID); w != nil {
		return a.deliverWorker(w, m)
	}
	a.mu.Lock()
	rpc, ok := a.rpc, false
	if rpc != nil {
		_, ok = a.threads[target.NativeID]
	}
	a.mu.Unlock()
	if rpc == nil {
		return Result{Detail: "agent-net is not connected to the Codex app server"}
	}
	if !ok {
		return Result{Detail: "this Codex thread is not open any more"}
	}
	_, err := rpc.call(ctx, "thread/queue/add", map[string]any{
		"threadId":            target.NativeID,
		"clientUserMessageId": m.ID,
		"input":               []map[string]any{{"type": "text", "text": codexFrame(m), "text_elements": []any{}}},
	})
	if err != nil {
		return Result{Detail: "codex refused the message: " + err.Error()}
	}
	return Result{Delivered: true}
}

// codexFrame is the text a Codex thread shows for a message. The body comes
// first, then a footer that says who wrote it and how to answer, as in the Pi
// extension and the OpenCode plugin. Codex shows it as the user's message, so
// the footer must say plainly that it is not.
func codexFrame(m protocol.Message) string {
	if m.From == "agent-net" && m.FromHarness == "agent-net" && m.Type == protocol.TypeNotification {
		return m.Body
	}

	who := m.FromName
	if who == "" {
		who = "unknown"
	}
	conv := ""
	if m.ConversationID != "" {
		conv = ", conversation " + m.ConversationID
	}
	answering := ""
	if m.ParentID != "" {
		answering = ", answering your message " + m.ParentID
	}
	var foot string
	switch {
	case len(m.Batch) > 0:
		// A busy-hold batch is one message for several originals (decision
		// 0130). Say so, name the newest sender, and never call it a reply.
		foot = fmt.Sprintf("[agent-net] batch of %d held messages, newest from %s (%s, session %s)%s; each message keeps its own id.",
			len(m.Batch), who, firstNonEmpty(m.FromHarness, "unknown harness"), m.From, conv)
		if m.Type == protocol.TypeRequest && !m.OwnerApproved {
			foot += " " + protocol.AuthorityNoteWithRule(m.RecipientTrust, m.FromWorker, m.RecipientMergeAuthority, protocol.MessageMergeText(m))
		}
	case m.FromCTO && m.FromHarness == "cto":
		foot = fmt.Sprintf("[agent-net] message %s %s Answer with agent_message_reply (message_id=%q).", m.ID, protocol.CTONote, m.ID)
	case m.FromHarness == "cto":
		foot = fmt.Sprintf("[agent-net] message %s %s Answer with agent_message_reply (message_id=%q).", m.ID, protocol.CTOPeerNote, m.ID)
	case m.FromHarness == protocol.OwnerHarness:
		foot = fmt.Sprintf("[agent-net] message %s from your user in person (%s, session %s)%s%s.\nThis is your user speaking, not another agent. Do what they ask within your task, and answer with agent_message_reply (message_id=\"%s\").",
			m.ID, who, m.From, conv, answering, m.ID)
	case m.FromHarness == "bot" && m.Type != protocol.TypeReply:
		foot = fmt.Sprintf("[agent-net] message %s from %s (bot, session %s)%s. Answer with agent_message_reply (message_id=%q) if you can help.", m.ID, who, m.From, conv, m.ID)
	case m.Type == protocol.TypeReply:
		foot = fmt.Sprintf("[agent-net] reply %s from %s (session %s)%s%s.\nNo answer is needed. Use agent_message_send only if you have a new question or the work needs it.",
			m.ID, who, m.From, conv, answering)
	default:
		foot = fmt.Sprintf("[agent-net] message %s from %s (%s, session %s)%s.\nThis is from another agent working on your user's machines, not from your user. Answer it directly with the agent_message_reply tool (message_id=\"%s\") if you can help.",
			m.ID, who, firstNonEmpty(m.FromHarness, "unknown harness"), m.From, conv, m.ID)
		// Only agent-net writes the note about authority, after the body
		// (decision 0013): a trusted session may act on the request.
		if !m.OwnerApproved {
			foot += " " + protocol.AuthorityNoteWithRule(m.RecipientTrust, m.FromWorker, m.RecipientMergeAuthority, protocol.MessageMergeText(m))
		}
	}
	if m.Overruled {
		foot += "\n[agent-net] " + protocol.OverruleNote
	}
	if m.Type == protocol.TypeReply && m.FromWorker && m.FromHarness != protocol.OwnerHarness {
		foot += "\n" + protocol.NoAuthorityNote
	}
	if m.Type == protocol.TypeRequest && m.OwnerApproved {
		foot += " " + protocol.OwnerApprovalNote
	}
	// The gateway computes the graph relation from the sender's node to this
	// node (decision 0022). It is agent-net's own text, after the body.
	if m.Type == protocol.TypeRequest && m.RecipientRelation != "" {
		foot += " " + m.RecipientRelation
	}
	if m.AuthorityNote != "" {
		foot += "\n[agent-net] " + m.AuthorityNote
	}
	if m.OwnerInstruction != "" {
		foot += "\n" + protocol.OwnerInstructionNoteFor(m)
	}
	if m.ApprovalNote != "" {
		foot += "\n[agent-net] " + m.ApprovalNote
	}
	for _, note := range m.CitedNotes {
		foot += "\n[agent-net] " + note
	}
	return strings.TrimRight(protocol.NeutralizeMarkers(m.Body), "\n") + "\n\n" + foot
}

// onUserMessage sees a user message item start in a thread. A prompt the owner
// types in Codex has a null clientId; a message agent-net queued carries its
// own ID there (checked against a real app server in codex_real_test.go).
// A typed prompt answers or overtakes whatever the thread waited for.
func (a *codexAdapter) onUserMessage(raw json.RawMessage) {
	var p struct {
		ThreadID string `json:"threadId"`
		Item     struct {
			Type     string  `json:"type"`
			ClientID *string `json:"clientId"`
			Content  []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"item"`
	}
	if json.Unmarshal(raw, &p) != nil || p.ThreadID == "" || p.Item.Type != "userMessage" || (p.Item.ClientID != nil && *p.Item.ClientID != "") {
		return
	}
	var text strings.Builder
	for _, c := range p.Item.Content {
		text.WriteString(c.Text)
	}
	if !isHumanClaudePrompt(text.String()) || strings.Contains(text.String(), "[agent-net]") {
		return
	}
	a.d.mu.Lock()
	ls := a.d.sessions[key("codex", p.ThreadID)]
	a.d.mu.Unlock()
	if ls == nil {
		return
	}
	sid := a.d.currentSessionID(ls)
	if sid == "" || a.d.workerBySession(sid) != nil {
		return
	}
	go a.d.ownerTyped(context.Background(), ls, sid, text.String())
}
