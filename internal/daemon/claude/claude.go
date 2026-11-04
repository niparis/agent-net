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

	mu sync.Mutex
	// healthWarning is set after healthBroken reaches two consecutive
	// broken delivery checks (health.go).
	healthWarning string
	healthBroken  int
	byNative      map[string]found          // native_id -> session
	bySock        map[string]string         // inbox socket -> native_id
	replies       map[replyKey]net.Listener // reply address -> its listener
	ctx           context.Context           // from Start; reply listeners run under it
	lastSweep     time.Time                 // when sweepReplies last ran
}

// replyKey is one reply address: a sender, and a conversation or "" for the
// sender's own socket (messages without a conversation, and older frames).
type replyKey struct{ sender, conv string }

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
		replies: map[replyKey]net.Listener{}, ctx: context.Background()}
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
	a.mu.Lock()
	a.ctx = ctx
	a.mu.Unlock()
	a.poll()
	a.reopenReplies()
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
				a.mu.Lock()
				due := time.Since(a.lastSweep) >= replySweepEvery
				a.mu.Unlock()
				if due {
					a.sweepReplies()
				}
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
	NameSource          string `json:"nameSource"`
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
		// The Claude name is a title for the TASK column, not the session's
		// network name (decision 0024). A name Claude derived from the folder
		// ("a2a-ocs-4f") says nothing the name does, so it is dropped.
		title := sf.Name
		if sf.NameSource == "derived" {
			title = ""
		}
		info := protocol.SessionInfo{Harness: "claude", NativeID: sf.SessionID, PID: sf.PID, Cwd: sf.Cwd,
			Name: title, Status: status, Delivery: "native-claude", Mode: modeOfKind(sf.Kind),
			Model: a.models.Model(sf.SessionID, sf.Cwd)}
		// Real activity, from the harness: the transcript or the session file.
		// Polling the same idle session does not move it.
		active := time.Time{}
		if fi, err := e.Info(); err == nil {
			active = fi.ModTime()
		}
		if t := a.models.LastWrite(sf.SessionID, sf.Cwd); t.After(active) {
			active = t
		}
		if !active.IsZero() {
			info.LastActive = active.Unix()
		}
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
	return BuildFrameFor(m, replySock, false)
}

// BuildFrameFor is BuildFrame for a session that may be a worker. A worker
// answers a request with its hand-back, so the frame names agent_handback
// instead of SendMessage.
func BuildFrameFor(m protocol.Message, replySock string, toWorker bool) ([]byte, error) {
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
	if m.FromCTO && m.FromHarness == "cto" {
		details = fmt.Sprintf("[agent-net] message %s %s Answer it directly with your SendMessage tool to this sender.", m.ID, protocol.CTONote)
	} else if m.FromHarness == "cto" {
		details = fmt.Sprintf("[agent-net] message %s %s Answer it directly with your SendMessage tool to this sender.", m.ID, protocol.CTOPeerNote)
	} else if m.FromHarness == protocol.OwnerHarness {
		// The person at the keyboard of a machine, not another agent.
		answering := ""
		if m.ParentID != "" {
			answering = ", answering your message " + m.ParentID
		}
		details = fmt.Sprintf("[agent-net] message %s from your user in person (%s, session %s)%s%s. This is your user speaking, not another agent. Do what they ask within your task, and answer it directly with your SendMessage tool to this sender.",
			m.ID, name, m.From, conv, answering)
	} else if m.FromHarness == "bot" && m.Type != protocol.TypeReply {
		details = fmt.Sprintf("[agent-net] message %s from %s (bot, session %s)%s. Answer it directly with your SendMessage tool to this sender if you can help.", m.ID, name, m.From, conv)
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
		if !m.OwnerApproved {
			details += " " + protocol.AuthorityNoteWithRule(m.RecipientTrust, m.FromWorker, m.RecipientMergeAuthority, protocol.MessageMergeText(m))
		}
	}
	if toWorker && m.Type != protocol.TypeReply {
		details = strings.ReplaceAll(details, "with your SendMessage tool to this sender", "with your agent_handback tool when the work is done (you are a worker)")
	}
	if m.Overruled {
		details += " [agent-net] " + protocol.OverruleNote
	}
	if m.Type == "reply" && m.FromWorker && m.FromHarness != protocol.OwnerHarness {
		details += " " + protocol.NoAuthorityNote
	}
	if m.Type == protocol.TypeRequest && m.OwnerApproved {
		details += " " + protocol.OwnerApprovalNote
	}
	// The gateway computes the graph relation from the sender's node to this
	// node (decision 0022). It is agent-net's own text, after the body.
	if m.Type == "request" && m.RecipientRelation != "" {
		details += " " + m.RecipientRelation
	}
	if m.OwnerInstruction != "" {
		details += "\n" + protocol.OwnerInstructionNoteWithRule(m.OwnerInstruction, m.RecipientMergeAuthority, protocol.MessageMergeText(m))
	}
	if m.ApprovalNote != "" {
		details += "\n[agent-net] " + m.ApprovalNote
	}
	var attrs []string
	if from != "" {
		attrs = append(attrs, fmt.Sprintf(`from="%s"`, from))
	}
	attrs = append(attrs, fmt.Sprintf(`from-name="%s"`, sanitizeName(name)), `from-mode="prompting"`)
	body := neutralize(m.Body)
	if m.From == "agent-net" && m.FromHarness == "agent-net" && m.Type == protocol.TypeNotification {
		body, details = "", m.Body
	}
	content := fmt.Sprintf("<cross-session-message %s>\n%s\n\n%s\n</cross-session-message>",
		strings.Join(attrs, " "), body, details)
	return json.Marshal(frame{Type: "user", MsgV: 1, MsgID: m.ID, From: from, Priority: "next",
		Message: frameMessage{Role: "user", Content: content}})
}

// BuildCTOFrame carries the owner's prompt without a peer reply address.
// The CTO's response belongs in its transcript, not in a network message.
func BuildCTOFrame(id, text string) ([]byte, error) {
	content := fmt.Sprintf("<cross-session-message from-name=\"owner\" from-mode=\"prompting\">\n%s\n\n[agent-net] This is the owner speaking. Please answer in this conversation.\n</cross-session-message>", neutralize(text))
	return json.Marshal(frame{Type: "user", MsgV: 1, MsgID: id, Priority: "next", Message: frameMessage{Role: "user", Content: content}})
}

// DeliverCTO delivers only to a session the daemon selected as its CTO.
func (a *Adapter) DeliverCTO(ctx context.Context, target protocol.SessionInfo, id, text string) daemon.Result {
	if warning := a.deliveryWarning(); warning != "" {
		return daemon.Result{Detail: warning}
	}
	a.mu.Lock()
	f, ok := a.byNative[target.NativeID]
	a.mu.Unlock()
	if !ok || f.sock == "" {
		return daemon.Result{Detail: "Claude session is gone or has no inbox socket"}
	}
	if err := checkSocket(f.sock); err != nil {
		return daemon.Result{Detail: "inbox socket refused: " + err.Error()}
	}
	line, err := BuildCTOFrame(id, text)
	if err != nil {
		return daemon.Result{Detail: err.Error()}
	}
	conn, err := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "unix", f.sock)
	if err != nil {
		return daemon.Result{Detail: "connect to inbox failed: " + err.Error()}
	}
	defer conn.Close()
	conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Write(append(line, '\n')); err != nil {
		return daemon.Result{Uncertain: true, Detail: "write failed after connecting; the message may have arrived: " + err.Error()}
	}
	return daemon.Result{Delivered: true}
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
	if warning := a.deliveryWarning(); warning != "" {
		return daemon.Result{Detail: warning}
	}
	a.mu.Lock()
	f, ok := a.byNative[target.NativeID]
	a.mu.Unlock()
	if !ok || f.sock == "" {
		return daemon.Result{Detail: "Claude session is gone or has no inbox socket"}
	}
	if err := checkSocket(f.sock); err != nil {
		return daemon.Result{Detail: "inbox socket refused: " + err.Error()}
	}
	// The record that matches Claude's answer to this message is written
	// before the session can see the message (W4.4), and before its reply
	// socket exists, so a sweep never closes that socket.
	book := a.host.Replies()
	if err := book.SaveDelivery(daemon.ClaudeDelivery{MessageID: m.ID, Recipient: target.SessionID, Sender: m.From,
		ConversationID: m.ConversationID, Type: m.Type, DeliveredAt: time.Now(), ExpiresAt: m.ExpiresAt}); err != nil {
		a.host.Logf("claude: cannot record delivery %s for reply matching: %v", m.ID, err)
	}
	forget := func() {
		if err := book.ForgetDelivery(m.ID); err != nil {
			a.host.Logf("claude: cannot forget undelivered %s: %v", m.ID, err)
		}
	}
	replySock, err := a.replySocket(m.From, m.ConversationID)
	if err != nil {
		a.host.Logf("claude: no reply socket for %s, sending without a reply address: %v", m.From, err)
	}
	line, err := BuildFrameFor(m, replySock, target.Worker != nil)
	if err != nil {
		forget()
		return daemon.Result{Detail: err.Error()}
	}
	conn, err := net.DialTimeout("unix", f.sock, 2*time.Second)
	if err != nil {
		forget()
		return daemon.Result{Detail: "connect to inbox failed: " + err.Error()}
	}
	defer conn.Close()
	conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Write(append(line, '\n')); err != nil {
		return daemon.Result{Uncertain: true, Detail: "write failed after connecting; the message may have arrived: " + err.Error()}
	}
	return daemon.Result{Delivered: true}
}

// ---- reply capture ----

// replySweepEvery is how often the adapter closes the reply sockets whose
// conversation has no row left in daemon.db (pruned by housekeeping).
const replySweepEvery = 10 * time.Minute

// maxSocketPath keeps a reply socket path under the Unix limit (104 bytes on
// macOS, with its final zero byte).
const maxSocketPath = 100

var replyID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// replySocket returns the reply address for a message from sender in conv.
// Each conversation has its own socket, so an answer tells which conversation
// it belongs to. The sender's own socket is kept for messages without a
// conversation and for frames delivered by an older daemon.
func (a *Adapter) replySocket(sender, conv string) (string, error) {
	if !replyID.MatchString(sender) {
		return "", fmt.Errorf("sender %q cannot name a socket", sender)
	}
	own, err := a.listenReply(replyKey{sender, ""})
	if err != nil || conv == "" {
		return own, err
	}
	if !replyID.MatchString(conv) || len(a.replyPath(replyKey{sender, conv})) > maxSocketPath {
		return own, nil
	}
	return a.listenReply(replyKey{sender, conv})
}

func (a *Adapter) replyPath(k replyKey) string {
	if k.conv == "" {
		return filepath.Join(a.cfg.ReplyDir, k.sender+".sock")
	}
	return filepath.Join(a.cfg.ReplyDir, k.sender+"."+k.conv+".sock")
}

func (a *Adapter) listenReply(k replyKey) (string, error) {
	path := a.replyPath(k)
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.replies[k]; ok {
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
	a.replies[k] = ln
	ctx := a.ctx
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go a.readReply(ctx, k, c)
		}
	}()
	return path, nil
}

// reopenReplies listens again on the reply address of every conversation
// that daemon.db still has, so an answer to a message delivered before a
// restart arrives.
func (a *Adapter) reopenReplies() {
	addrs, err := a.host.Replies().ReplyAddresses()
	if err != nil {
		a.host.Logf("claude: cannot read reply addresses: %v", err)
		return
	}
	for _, ad := range addrs {
		if _, err := a.replySocket(ad.Sender, ad.ConversationID); err != nil {
			a.host.Logf("claude: cannot reopen reply socket for %s in %s: %v", ad.Sender, ad.ConversationID, err)
		}
	}
}

// sweepReplies closes the conversation sockets that daemon.db no longer has.
func (a *Adapter) sweepReplies() {
	a.mu.Lock()
	a.lastSweep = time.Now()
	a.mu.Unlock()
	addrs, err := a.host.Replies().ReplyAddresses()
	if err != nil {
		a.host.Logf("claude: cannot read reply addresses: %v", err)
		return
	}
	keep := map[replyKey]bool{}
	for _, ad := range addrs {
		keep[replyKey{ad.Sender, ad.ConversationID}] = true
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for k, ln := range a.replies {
		if k.conv != "" && !keep[k] {
			ln.Close()
			delete(a.replies, k)
		}
	}
}

func (a *Adapter) closeReplies() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for k, ln := range a.replies {
		ln.Close()
		delete(a.replies, k)
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

func (a *Adapter) readReply(ctx context.Context, k replyKey, c net.Conn) {
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
		a.handleReply(ctx, k, in)
	}
}

func (a *Adapter) handleReply(ctx context.Context, k replyKey, in inbound) {
	sender := k.sender
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
	book := a.host.Replies()
	open, err := book.OpenRequests(replier, sender, k.conv, time.Now())
	if err != nil {
		a.host.Logf("claude: cannot read open requests for a reply from %s to %s: %v", replier, sender, err)
	}
	m := matchReply(k.conv, body, open)
	if p.ParentID = m.parent; p.ParentID == "" {
		p.ConversationID = m.conv
	}
	if p.ParentID == "" && p.ConversationID == "" {
		// Nothing tells where it belongs: a new conversation, as before.
		p.Type = protocol.TypeNotification
		if len(open) > 1 {
			a.host.Logf("claude: reply from %s to %s sent alone: %d open requests in several conversations, none named", replier, sender, len(open))
		}
	}
	if m.match == daemon.ReplyMatchConversation {
		a.host.Logf("claude: reply from %s to %s threaded to conversation %s: %d open requests, none named", replier, sender, m.conv, len(m.closes))
	}
	err = a.host.Send(ctx, p)
	if err != nil && p.ParentID == "" && p.ConversationID != "" {
		// An older gateway, or one that no longer has the conversation:
		// the answer must still arrive.
		a.host.Logf("claude: reply from %s to %s refused in conversation %s, sending it alone: %v", replier, sender, p.ConversationID, err)
		p.ConversationID, p.Type = "", protocol.TypeNotification
		err = a.host.Send(ctx, p)
	}
	if err != nil {
		a.host.Logf("claude: could not forward reply: %v", err)
		return
	}
	if err := book.CloseRequests(m.closes, m.match, time.Now()); err != nil {
		a.host.Logf("claude: cannot close answered requests %v: %v", m.closes, err)
	}
}

// replyMatch is where a Claude answer goes: the request it answers, or only
// its conversation, and the open requests it closes.
type replyMatch struct {
	parent, conv string
	match        string
	closes       []string
}

// matchReply picks the request a Claude answer belongs to (W4.4). open holds
// the open requests from the sender to the replier in conv (any conversation
// when conv is ""), newest first. One open request is the answer's parent.
// With several, a body that names exactly one of them chooses it; otherwise
// the answer goes to the conversation with no parent, and closes them all.
// With none, the answer stays in its conversation.
func matchReply(conv, body string, open []daemon.ClaudeDelivery) replyMatch {
	if len(open) == 1 {
		return replyMatch{parent: open[0].MessageID, conv: open[0].ConversationID, match: daemon.ReplyMatchExact, closes: []string{open[0].MessageID}}
	}
	var named []daemon.ClaudeDelivery
	for _, r := range open {
		if strings.Contains(body, r.MessageID) {
			named = append(named, r)
		}
	}
	if len(named) == 1 {
		r := named[0]
		return replyMatch{parent: r.MessageID, conv: r.ConversationID, match: daemon.ReplyMatchExact, closes: []string{r.MessageID}}
	}
	if len(open) == 0 {
		return replyMatch{conv: conv}
	}
	if conv == "" {
		// An older reply address without a conversation: use the one
		// conversation of the open requests, if they share one.
		conv = open[0].ConversationID
		for _, r := range open {
			if r.ConversationID != conv {
				return replyMatch{}
			}
		}
	}
	out := replyMatch{conv: conv, match: daemon.ReplyMatchConversation}
	for _, r := range open {
		out.closes = append(out.closes, r.MessageID)
	}
	return out
}
