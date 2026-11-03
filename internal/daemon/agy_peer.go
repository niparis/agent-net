package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/niparis/agent-net/internal/protocol"
)

// agyAdapter discovers only processes which reported SessionStart through a
// hook. The hook's kernel-reported process ancestry identifies the particular
// agy process even when several sessions have the same working directory.
type agyAdapter struct {
	d         *Daemon
	summaries string
	kick      chan struct{}
	refreshMu sync.Mutex
	mu        sync.Mutex
	peers     map[string]protocol.SessionInfo
	pending   map[string][]protocol.Message
}

func newAgyAdapter(d *Daemon) *agyAdapter {
	return &agyAdapter{d: d, summaries: d.cfg.AgySummaries, kick: make(chan struct{}, 1), peers: map[string]protocol.SessionInfo{}, pending: map[string][]protocol.Message{}}
}
func (a *agyAdapter) Harness() string { return "agy" }
func (a *agyAdapter) Start(ctx context.Context, _ Host) error {
	a.d.wg.Add(1)
	go func() {
		defer a.d.wg.Done()
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				a.refresh()
			case <-a.kick:
				a.refresh()
			}
		}
	}()
	return nil
}
func (a *agyAdapter) wake() {
	select {
	case a.kick <- struct{}{}:
	default:
	}
}
func agyProcess(pid int) bool {
	args := processArgs(pid)
	if len(args) == 0 {
		return false
	}
	if filepath.Base(args[0]) == "agy" {
		return true
	}
	// agy may be launched by node with its script as argv[1].
	return len(args) > 1 && (filepath.Base(args[0]) == "node" || filepath.Base(args[0]) == "nodejs") && filepath.Base(args[1]) == "agy"
}
func agyAncestor(pid int) int {
	for p, depth := pid, 0; p > 1 && depth < 64; depth++ {
		if agyProcess(p) {
			return p
		}
		parent, err := processParentPID(p)
		if err != nil {
			break
		}
		p = parent
	}
	return 0
}
func agyWorkspace(pid int, paths []string) string {
	cwd := agyProcessCwd(pid)
	if cwd == "" {
		return ""
	}
	cwd = filepath.Clean(cwd)
	for _, p := range paths {
		if !filepath.IsAbs(p) {
			continue
		}
		clean := filepath.Clean(p)
		if resolved, err := filepath.EvalSymlinks(clean); err == nil {
			clean = resolved
		}
		if clean == cwd {
			return cwd
		}
	}
	return ""
}
func (a *agyAdapter) has(id string, pid int) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	p, ok := a.peers[id]
	return ok && p.PID == pid
}

// summariesData tolerates databases from different agy versions with missing
// optional columns. It never creates a database or changes agy's files.
func (a *agyAdapter) summariesData() map[string]agySummary {
	result := map[string]agySummary{}
	if a.summaries == "" {
		return result
	}
	if _, err := os.Stat(a.summaries); err != nil {
		return result
	}
	u := url.URL{Scheme: "file", Path: a.summaries}
	db, err := sql.Open("sqlite", u.String()+"?mode=ro&_pragma=busy_timeout(100)")
	if err != nil {
		return result
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	columns := map[string]bool{}
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(conversation_summaries)`)
	if err != nil {
		return result
	}
	for rows.Next() {
		var n, notnull, pk int
		var name, typ string
		var def any
		if rows.Scan(&n, &name, &typ, &notnull, &def, &pk) == nil {
			columns[name] = true
		}
	}
	rows.Close()
	if !columns["conversation_id"] {
		return result
	}
	title, parent := "''", "''"
	if columns["title"] {
		title = "COALESCE(title,'')"
	}
	if columns["parent_conversation_id"] {
		parent = "COALESCE(parent_conversation_id,'')"
	}
	rows, err = db.QueryContext(ctx, `SELECT conversation_id, `+title+`, `+parent+` FROM conversation_summaries`)
	if err != nil {
		return result
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var s agySummary
		if rows.Scan(&id, &s.title, &s.parent) == nil {
			result[id] = s
		}
	}
	return result
}

type agySummary struct{ title, parent string }

func (a *agyAdapter) refresh() {
	a.refreshMu.Lock()
	defer a.refreshMu.Unlock()
	d := a.d
	d.mu.Lock()
	events := make(map[string]antigravityEvent, len(d.agyEvents))
	for k, v := range d.agyEvents {
		events[k] = v
	}
	d.mu.Unlock()
	summaries := a.summariesData()
	var infos []protocol.SessionInfo
	for id, e := range events {
		if !e.Started || e.PID <= 1 || !agyProcess(e.PID) || e.ProcessStart == "" || agyProcessStart(e.PID) != e.ProcessStart || agyWorkspace(e.PID, e.WorkspacePaths) == "" || d.agyWorkerConversation(id) || d.workerForPeer(e.PID) != nil || summaries[id].parent != "" {
			continue
		}
		status := "unknown"
		if e.Event == "PreInvocation" {
			status = "busy"
		} else if e.Event == "Stop" && e.FullyIdle {
			status = "idle"
		}
		cwd := agyWorkspace(e.PID, e.WorkspacePaths)
		name := strings.TrimSpace(summaries[id].title)
		if name == "" {
			name = filepath.Base(cwd)
		}
		infos = append(infos, protocol.SessionInfo{Harness: "agy", NativeID: id, PID: e.PID, Cwd: cwd, Name: name, Status: status, Delivery: "agy-hook", LastActive: e.Time.Unix()})
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].NativeID < infos[j].NativeID })
	peers := map[string]protocol.SessionInfo{}
	for _, info := range infos {
		peers[info.NativeID] = info
	}
	a.mu.Lock()
	a.peers = peers
	a.mu.Unlock()
	d.SetSessions("agy", infos)
}

func (a *agyAdapter) Deliver(ctx context.Context, target protocol.SessionInfo, m protocol.Message) Result {
	a.mu.Lock()
	peer, ok := a.peers[target.NativeID]
	if !ok || peer.PID != target.PID {
		a.mu.Unlock()
		return Result{Detail: "this Antigravity session is no longer open"}
	}
	for _, q := range a.pending[target.NativeID] {
		if q.ID == m.ID {
			a.mu.Unlock()
			return Result{Queued: true, Detail: "waiting for the session's next turn"}
		}
	}
	// Queue busy and unknown sessions atomically with their hooks.
	a.d.mu.Lock()
	event := a.d.agyEvents[target.NativeID]
	a.d.mu.Unlock()
	if event.Event != "Stop" || !event.FullyIdle {
		a.pending[target.NativeID] = append(a.pending[target.NativeID], m)
		a.mu.Unlock()
		return Result{Queued: true, Detail: "waiting for the session's next turn"}
	}
	a.mu.Unlock()
	if event.Event == "Stop" && event.FullyIdle {
		if sent, uncertain := a.typeInHerdr(peer, codexFrame(m)); sent || uncertain {
			if uncertain {
				return Result{Delivered: true, Detail: "unconfirmed"}
			}
			return Result{Delivered: true, Detail: "typed into herdr"}
		}
	}
	a.mu.Lock()
	for _, q := range a.pending[target.NativeID] {
		if q.ID == m.ID {
			a.mu.Unlock()
			return Result{Queued: true, Detail: "waiting for the session's next turn"}
		}
	}
	a.pending[target.NativeID] = append(a.pending[target.NativeID], m)
	a.mu.Unlock()
	return Result{Queued: true, Detail: "waiting for the session's next turn"}
}

func (a *agyAdapter) typeInHerdr(info protocol.SessionInfo, text string) (sent, uncertain bool) {
	// Terminal control characters could be interpreted as keys by the pane.
	// A hook can deliver them as data without typing them into a terminal.
	for _, r := range text {
		if r != '\n' && unicode.IsControl(r) {
			return false, false
		}
	}
	h, found := findHerdrPane(info.PID)
	if !found {
		h, found = findHerdrServer(info.PID)
	}
	bin := a.d.herdrBinary(h.bin)
	if bin == "" {
		return false, false
	}
	var panes []herdrPaneInfo
	var socket string
	var ok bool
	if found {
		panes, socket, ok = herdrPanesFromServer(bin, h)
	} else {
		var err error
		panes, err = herdrPanes(bin, "")
		ok = err == nil
	}
	if !ok {
		return false, false
	}
	chain := pidChain(info.PID)
	for _, pane := range panes {
		if !herdrIDRe.MatchString(pane.PaneID) {
			continue
		}
		reported := pane.Session != nil && pane.Session.Agent == "agy" && pane.Session.Kind == "id" && pane.Session.Value == info.NativeID
		if !reported && !herdrHolds(bin, socket, pane.PaneID, chain) {
			continue
		}
		a.d.mu.Lock()
		event := a.d.agyEvents[info.NativeID]
		a.d.mu.Unlock()
		if event.Event != "Stop" || !event.FullyIdle || event.PID != info.PID || agyProcessStart(info.PID) != event.ProcessStart {
			return false, false
		}
		if _, err := herdrRun(bin, socket, "pane", "send-text", pane.PaneID, text); err != nil {
			return false, true
		}
		if _, err := herdrRun(bin, socket, "pane", "send-keys", pane.PaneID, "enter"); err != nil {
			return false, true
		}
		return true, false
	}
	return false, false
}

// takeHook runs under a.mu so delivery cannot be lost between a hook and queueing.
func (a *agyAdapter) takeHook(e antigravityEvent, pid int) map[string]any {
	if e.Event != "Stop" && e.Event != "PreInvocation" {
		return map[string]any{}
	}
	peer, ok := a.peers[e.ConversationID]
	if !ok || !pidChain(pid)[peer.PID] || a.d.agyWorkerConversation(e.ConversationID) {
		return map[string]any{}
	}
	var frames []string
	var delivered, remaining []protocol.Message
	// The local client has a 4 MiB response limit. Bound the JSON-escaped
	// output, leaving the rest for the next hook in their original order.
	size := 0
	for _, m := range a.pending[e.ConversationID] {
		if !m.ExpiresAt.IsZero() && time.Now().After(m.ExpiresAt) {
			continue
		}
		frame := codexFrame(m)
		encoded, _ := json.Marshal(frame)
		if (len(frames) >= 16 || size+len(encoded)+16 > 900*1024) && len(frames) > 0 {
			remaining = append(remaining, m)
			continue
		}
		if len(remaining) > 0 {
			remaining = append(remaining, m)
			continue
		}
		size += len(encoded) + 16
		frames = append(frames, frame)
		delivered = append(delivered, m)
	}
	if len(remaining) == 0 {
		delete(a.pending, e.ConversationID)
	} else {
		a.pending[e.ConversationID] = remaining
	}

	if len(frames) == 0 {
		return map[string]any{}
	}
	// Bound the local receipt write as well as the answer size: a busy state
	// database must not cause the hook client to time out before reading it.
	writeCtx, cancelWrite := context.WithTimeout(context.Background(), 500*time.Millisecond)
	for _, m := range delivered {
		if _, err := a.d.st.db.ExecContext(writeCtx, `INSERT OR IGNORE INTO delivered (message_id, at) VALUES (?,?)`, m.ID, time.Now().Unix()); err != nil {
			a.d.Logf("mark delivered: %v", err)
		}
	}
	cancelWrite()
	// The hook does not wait for the network receipt.
	a.d.goBackground(func() {
		for _, m := range delivered {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			a.d.sendUpdate(ctx, m.ID, protocol.StateDelivered, "Antigravity hook")
			cancel()
		}
	})
	text := strings.Join(frames, "\n\n")
	if e.Event == "Stop" {
		// The continuation immediately starts another turn; do not type an
		// idle message into its pane during the gap before PreInvocation.
		a.d.mu.Lock()
		current := a.d.agyEvents[e.ConversationID]
		current.FullyIdle = false
		a.d.agyEvents[e.ConversationID] = current
		a.d.mu.Unlock()
		return map[string]any{"decision": "continue", "reason": text}
	}
	return map[string]any{"injectSteps": []map[string]string{{"userMessage": text}}}
}

func (lc *localConn) helloInteractiveAgy(p helloParams, pid int) (any, error) {
	d := lc.d
	lc.mu.Lock()
	lc.client, lc.peerPID, lc.agyNativeID = "mcp", pid, p.NativeID
	lc.mu.Unlock()
	if lc.bindInteractiveAgy() {
		lc.mu.Lock()
		ls := lc.ls
		lc.mu.Unlock()
		return d.mcpHelloResult(ls, "agy", p.Version), nil
	}
	return map[string]any{"node_id": d.cfg.NodeID, "version": d.helloVersion()}, nil
}
func (lc *localConn) bindInteractiveAgy() bool {
	lc.mu.Lock()
	id, pid, previous := lc.agyNativeID, lc.peerPID, lc.ls
	lc.mu.Unlock()
	if id == "" {
		return false
	}
	a, ok := lc.d.adapters["agy"].(*agyAdapter)
	var peer protocol.SessionInfo
	if ok {
		a.mu.Lock()
		peer, ok = a.peers[id]
		a.mu.Unlock()
	}
	if ok && agyProcess(peer.PID) && pidChain(pid)[peer.PID] && !lc.d.agyWorkerConversation(id) {
		start := agyProcessStart(peer.PID)
		lc.d.mu.Lock()
		ls := lc.d.sessions[key("agy", id)]
		event := lc.d.agyEvents[id]
		if ls != nil && (ls.info.PID != peer.PID || event.ProcessStart == "" || start != event.ProcessStart) {
			ls = nil
		}
		lc.d.mu.Unlock()
		if ls != nil {
			if ls != previous {
				lc.bind("mcp", ls)
				lc.d.noteMCPHello(id)
			}
			return true
		}
	}
	lc.mu.Lock()
	lc.ls = nil
	lc.mu.Unlock()
	return false
}
