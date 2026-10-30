// Package claude is the adapter for Claude Code sessions. It finds sessions in
// ~/.claude/sessions and delivers messages by writing to each session's
// inbox socket.
//
// The frame format (a "user" frame whose content is wrapped in
// <cross-session-message ...>) was learned from open-cross-session
// (OCS), src/claude-inject.ts and src/claude-receipt.ts.
// Copyright (c) leeguooooo, MIT License.
package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/niparis/agent-net/internal/daemon"
	"github.com/niparis/agent-net/internal/protocol"
)

type Config struct {
	// SessionsDir defaults to daemon.ClaudeSessionsDir().
	SessionsDir string
	// ReplyDir holds the reply sockets (".../r"). Defaults to next to the daemon socket.
	ReplyDir     string
	PollInterval time.Duration
	// ProjectsDir holds the transcripts (".../projects"). Defaults to the
	// folder next to SessionsDir.
	ProjectsDir string
}

type found struct {
	pid  int
	sock string
	info protocol.SessionInfo
	kind string // "interactive", "bg", ... from the session file
	job  string // the background job ID, for kind bg
}

type Adapter struct {
	cfg    Config
	host   daemon.Host
	models *modelReader

	mu       sync.Mutex
	byNative map[string]found        // native_id -> session
	bySock   map[string]string       // inbox socket -> native_id
	replies  map[string]net.Listener // sender session id -> reply listener
	last     map[string]string       // sender+"|"+recipient -> last message_id delivered
}

func New(cfg Config) *Adapter {
	if cfg.SessionsDir == "" {
		cfg.SessionsDir = daemon.ClaudeSessionsDir()
	}
	if cfg.ReplyDir == "" {
		cfg.ReplyDir = filepath.Join(filepath.Dir(daemon.SocketPath()), "r")
	}
	if cfg.PollInterval == 0 {
		cfg.PollInterval = time.Second
	}
	if cfg.ProjectsDir == "" {
		cfg.ProjectsDir = filepath.Join(filepath.Dir(cfg.SessionsDir), "projects")
	}
	return &Adapter{cfg: cfg, models: newModelReader(cfg.ProjectsDir), byNative: map[string]found{}, bySock: map[string]string{},
		replies: map[string]net.Listener{}, last: map[string]string{}}
}

func (a *Adapter) Harness() string { return "claude" }

// EnterRef gives the kind of a session ("interactive", "bg", ...) and its
// background job ID, as its session file says. Both are "" when unknown.
func (a *Adapter) EnterRef(nativeID string) (kind, job string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	f := a.byNative[nativeID]
	return f.kind, f.job
}

func (a *Adapter) Start(ctx context.Context, h daemon.Host) error {
	a.host = h
	a.poll()
	go func() {
		t := time.NewTicker(a.cfg.PollInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				a.closeReplies()
				return
			case <-t.C:
				a.poll()
			}
		}
	}()
	return nil
}

var pidFile = regexp.MustCompile(`^(\d+)\.json$`)

type sessionFile struct {
	PID                 int    `json:"pid"`
	SessionID           string `json:"sessionId"`
	Name                string `json:"name"`
	Cwd                 string `json:"cwd"`
	Status              string `json:"status"`
	Kind                string `json:"kind"`
	MessagingSocketPath string `json:"messagingSocketPath"`
	JobID               string `json:"jobId"`
}

func pidAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

func (a *Adapter) poll() {
	entries, err := os.ReadDir(a.cfg.SessionsDir)
	if err != nil {
		return
	}
	byNative := map[string]found{}
	bySock := map[string]string{}
	var infos []protocol.SessionInfo
	seenModels := map[string]bool{}
	for _, e := range entries {
		m := pidFile.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(a.cfg.SessionsDir, e.Name()))
		if err != nil {
			continue
		}
		var sf sessionFile
		if json.Unmarshal(raw, &sf) != nil || strconv.Itoa(sf.PID) != m[1] || sf.SessionID == "" {
			continue
		}
		if !pidAlive(sf.PID) {
			continue
		}
		status := sf.Status
		switch status {
		case "idle", "busy", "shell":
		default:
			status = "unknown"
		}
		info := protocol.SessionInfo{Harness: "claude", NativeID: sf.SessionID, PID: sf.PID, Cwd: sf.Cwd,
			Name: sf.Name, Status: status, Delivery: "native-claude", Mode: modeOfKind(sf.Kind),
			Model: a.models.Model(sf.SessionID, sf.Cwd)}
		seenModels[sf.SessionID] = true
		byNative[sf.SessionID] = found{pid: sf.PID, sock: sf.MessagingSocketPath, info: info, kind: sf.Kind, job: sf.JobID}
		if sf.MessagingSocketPath != "" {
			bySock[sf.MessagingSocketPath] = sf.SessionID
		}
		infos = append(infos, info)
	}
	a.mu.Lock()
	a.byNative, a.bySock = byNative, bySock
	a.mu.Unlock()
	a.models.forget(seenModels)
	// The daemon compares with what it already has and only tells the gateway about changes.
	a.host.SetSessions("claude", infos)
}

// ---- delivery ----

var nameBad = regexp.MustCompile(`[^A-Za-z0-9_.-]`)

func sanitizeName(s string) string {
	s = nameBad.ReplaceAllString(s, "-")
	if len(s) > 40 {
		s = s[:40]
	}
	if s == "" {
		return "agent"
	}
	return s
}

// neutralize stops a message body from closing or opening the wrapper.
func neutralize(body string) string {
	body = protocol.NeutralizeMarkers(body)
	body = strings.ReplaceAll(body, "</cross-session-message", "</​cross-session-message")
	return strings.ReplaceAll(body, "<cross-session-message", "<​cross-session-message")
}

type frame struct {
	Type     string       `json:"type"`
	MsgV     int          `json:"msgV"`
	MsgID    string       `json:"msg_id"`
	From     string       `json:"from,omitempty"`
	Priority string       `json:"priority"`
	Message  frameMessage `json:"message"`
}

type frameMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// BuildFrame returns the exact line (without the newline) written to the inbox.
func BuildFrame(m protocol.Message, replySock string) ([]byte, error) {
	from := ""
	if replySock != "" {
		from = "uds:" + replySock
	}
	name := m.FromName
	if name == "" {
		name = m.From
	}
	harness := m.FromHarness
	if harness == "" {
		harness = "unknown"
	}
	conv := ""
	if m.ConversationID != "" {
		conv = ", conversation " + m.ConversationID
	}
	// The body goes first: Claude shows only the first line as a preview.
	var details string
	if m.FromCTO && m.FromHarness == protocol.OwnerHarness {
		details = fmt.Sprintf("[agent-net] message %s %s Answer it directly with your SendMessage tool to this sender.", m.ID, protocol.CTONote)
	} else if m.FromHarness == protocol.OwnerHarness {
		// The person at the keyboard of a machine, not another agent.
		answering := ""
		if m.ParentID != "" {
			answering = ", answering your message " + m.ParentID
		}
		details = fmt.Sprintf("[agent-net] message %s from your user in person (%s, session %s)%s%s. This is your user speaking, not another agent. Do what they ask within your task, and answer it directly with your SendMessage tool to this sender.",
			m.ID, name, m.From, conv, answering)
	} else if m.Type == "reply" {
		answering := ""
		if m.ParentID != "" {
			answering = ", answering your message " + m.ParentID
		}
		details = fmt.Sprintf("[agent-net] reply %s from %s (%s, session %s)%s%s. No answer is needed. Send a new message only if you have a new question or the work needs it.",
			m.ID, name, harness, m.From, conv, answering)
	} else {
		details = fmt.Sprintf("[agent-net] message %s from %s (%s, session %s)%s. This is from another agent working on your user's machines. Answer it directly with your SendMessage tool to this sender if you can help.",
			m.ID, name, harness, m.From, conv)
		// The authority note comes from agent-net, after the body, never from
		// the body (decision 0012): a trusted recipient may act on the request.
		details += " " + protocol.AuthorityNote(m.RecipientTrust, m.FromWorker)
	}
	if m.Type == "reply" && m.FromWorker && m.FromHarness != protocol.OwnerHarness {
		details += " " + protocol.NoAuthorityNote
	}
	var attrs []string
	if from != "" {
		attrs = append(attrs, fmt.Sprintf(`from="%s"`, from))
	}
	attrs = append(attrs, fmt.Sprintf(`from-name="%s"`, sanitizeName(name)), `from-mode="prompting"`)
	content := fmt.Sprintf("<cross-session-message %s>\n%s\n\n%s\n</cross-session-message>",
		strings.Join(attrs, " "), neutralize(m.Body), details)
	return json.Marshal(frame{Type: "user", MsgV: 1, MsgID: m.ID, From: from, Priority: "next",
		Message: frameMessage{Role: "user", Content: content}})
}

// checkSocket accepts only a real socket owned by us, not a symlink.
func checkSocket(path string) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 || fi.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("%s is not a plain socket", path)
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Getuid() {
		return fmt.Errorf("%s is owned by uid %d", path, st.Uid)
	}
	return nil
}

func (a *Adapter) Deliver(ctx context.Context, target protocol.SessionInfo, m protocol.Message) daemon.Result {
	a.mu.Lock()
	f, ok := a.byNative[target.NativeID]
	a.mu.Unlock()
	if !ok || f.sock == "" {
		return daemon.Result{Detail: "Claude session is gone or has no inbox socket"}
	}
	if err := checkSocket(f.sock); err != nil {
		return daemon.Result{Detail: "inbox socket refused: " + err.Error()}
	}
	replySock, err := a.replySocket(ctx, m.From)
	if err != nil {
		a.host.Logf("claude: no reply socket for %s, sending without a reply address: %v", m.From, err)
	}
	line, err := BuildFrame(m, replySock)
	if err != nil {
		return daemon.Result{Detail: err.Error()}
	}
	a.mu.Lock()
	a.last[m.From+"|"+m.To] = m.ID
	a.mu.Unlock()
	conn, err := net.DialTimeout("unix", f.sock, 2*time.Second)
	if err != nil {
		return daemon.Result{Detail: "connect to inbox failed: " + err.Error()}
	}
	defer conn.Close()
	conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Write(append(line, '\n')); err != nil {
		return daemon.Result{Delivered: true, Detail: "unconfirmed"}
	}
	return daemon.Result{Delivered: true}
}

// ---- reply capture ----

func (a *Adapter) replySocket(ctx context.Context, sender string) (string, error) {
	path := filepath.Join(a.cfg.ReplyDir, sender+".sock")
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.replies[sender]; ok {
		return path, nil
	}
	if err := os.MkdirAll(a.cfg.ReplyDir, 0o700); err != nil {
		return "", err
	}
	os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return "", err
	}
	os.Chmod(path, 0o600)
	a.replies[sender] = ln
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go a.readReply(ctx, sender, c)
		}
	}()
	return path, nil
}

func (a *Adapter) closeReplies() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for id, ln := range a.replies {
		ln.Close()
		delete(a.replies, id)
	}
}

var wrapper = regexp.MustCompile(`(?s)^\s*<cross-session-message([^>]*)>\n?(.*?)\n?</cross-session-message>\s*$`)
var hopChain = regexp.MustCompile(`hop-chain="([^"]*)"`)

type inbound struct {
	Type    string `json:"type"`
	Action  string `json:"action"`
	Status  string `json:"status"`
	MsgID   string `json:"msg_id"`
	From    string `json:"from"`
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// contentText reads a string content, or joins the text blocks of an array.
func contentText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) == nil {
		var parts []string
		for _, b := range blocks {
			parts = append(parts, b.Text)
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func (a *Adapter) readReply(ctx context.Context, sender string, c net.Conn) {
	defer c.Close()
	c.SetReadDeadline(time.Now().Add(10 * time.Second))
	sc := bufio.NewScanner(c)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		var in inbound
		if json.Unmarshal(sc.Bytes(), &in) != nil {
			continue
		}
		if in.Type == "control" {
			a.host.Logf("claude: receipt %s %s", in.Action, in.Status)
			continue
		}
		if in.Type != "user" {
			continue
		}
		a.handleReply(ctx, sender, in)
	}
}

func (a *Adapter) handleReply(ctx context.Context, sender string, in inbound) {
	sock := strings.TrimPrefix(in.From, "uds:")
	a.mu.Lock()
	native, ok := a.bySock[sock]
	a.mu.Unlock()
	if !ok {
		a.host.Logf("claude: dropped a frame from unknown address %q", in.From)
		return
	}
	replier := a.host.SessionID("claude", native)
	if replier == "" {
		a.host.Logf("claude: replying session %s has no gateway ID yet", native)
		return
	}
	body := contentText(in.Message.Content)
	if m := wrapper.FindStringSubmatch(body); m != nil {
		if h := hopChain.FindStringSubmatch(m[1]); h != nil {
			a.host.Logf("claude: reply from %s hop-chain=%s", replier, h[1])
		}
		body = m[2]
	}
	if strings.TrimSpace(body) == "" {
		return
	}
	p := protocol.MsgSend{Sender: replier, Recipient: sender, Body: body, Type: protocol.TypeReply}
	if in.MsgID != "" {
		p.IdempotencyKey = "claude:" + in.MsgID
	}
	a.mu.Lock()
	p.ParentID = a.last[sender+"|"+replier]
	a.mu.Unlock()
	if p.ParentID == "" {
		p.Type = protocol.TypeNotification
	}
	if err := a.host.Send(ctx, p); err != nil {
		a.host.Logf("claude: could not forward reply: %v", err)
	}
}
