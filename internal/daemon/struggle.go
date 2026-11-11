package daemon

// The struggle detector (decision 0132): at run end, read the run's own tool
// record once and count the signs that the worker fought its instructions or
// its environment. No model call is made, and no setting controls it.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/daemon/opencodeapi"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/redact"
)

// struggleEnvErrors is the one table of environment problems the owner named.
// A tool result that holds one of these texts counts as an environment error.
var struggleEnvErrors = []string{
	"packed-refs.lock",
	"permission denied",
	"operation not permitted",
	"command not found",
	"address already in use",
	"context deadline exceeded",
}

// struggleEditTools are the tool names that change the worktree. A shell
// command that clearly writes a file counts too, so exploration is measured
// from the first change and not from the first tool the harness happens to
// name.
var struggleEditTools = map[string]bool{
	"Edit": true, "Write": true, "MultiEdit": true, "NotebookEdit": true,
	"apply_patch": true, "edit": true, "write": true, "patch": true,
}

var struggleWriteMarkers = []string{"apply_patch", "sed -i", "tee ", "cat >", " > ", ">>"}

// struggleEnvError reports whether text holds a known environment problem.
func struggleEnvError(text string) bool {
	text = strings.ToLower(text)
	for _, marker := range struggleEnvErrors {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

// struggleEditCall tells whether a tool call changes the worktree.
func struggleEditCall(name, command string) bool {
	if struggleEditTools[name] {
		return true
	}
	command = strings.ToLower(command)
	for _, marker := range struggleWriteMarkers {
		if strings.Contains(command, marker) {
			return true
		}
	}
	return false
}

// struggleCall is one tool call of a run, in order.
type struggleCall struct {
	id       string
	label    string
	command  bool
	edit     bool
	failed   bool
	envError bool
}

// struggleCollector keeps the calls of one run as its trace is read.
type struggleCollector struct {
	calls []struggleCall
	index map[string]int
}

func newStruggleCollector() *struggleCollector {
	return &struggleCollector{index: map[string]int{}}
}

func (c *struggleCollector) add(id, label string, command, edit bool) {
	c.index[id] = len(c.calls)
	c.calls = append(c.calls, struggleCall{id: id, label: label, command: command, edit: edit})
}

func (c *struggleCollector) result(id string, failed bool, text string) {
	i, ok := c.index[id]
	if !ok {
		return
	}
	c.calls[i].failed = failed
	if failed && struggleEnvError(text) {
		c.calls[i].envError = true
	}
}

// counts turns the calls into the run's counts.
func (c *struggleCollector) counts() protocol.Struggle {
	type group struct {
		runs, failed int
	}
	groups := map[string]*group{}
	var order []string
	var out protocol.Struggle
	failedBefore := map[string]bool{}
	explored, exploredSet, env := 0, false, 0
	for _, call := range c.calls {
		if call.failed {
			out.Failed++
		}
		if call.envError {
			env++
		}
		if !exploredSet {
			if call.edit {
				exploredSet = true
			} else {
				explored++
			}
		}
		if call.command && call.label != "" {
			g := groups[call.label]
			if g == nil {
				g = &group{}
				groups[call.label] = g
				order = append(order, call.label)
			}
			g.runs++
			if call.failed {
				g.failed++
			}
			if failedBefore[call.label] {
				out.Retries++
			}
			if call.failed {
				failedBefore[call.label] = true
			}
		}
	}
	if exploredSet {
		out.Exploration = explored
	}
	out.EnvErrors = env
	var worst []string
	for _, label := range order {
		g := groups[label]
		if g.runs >= protocol.StruggleRepeatRuns {
			out.Repeats++
		}
		if g.failed > 0 || g.runs >= protocol.StruggleRepeatRuns {
			worst = append(worst, label)
		}
	}
	sort.SliceStable(worst, func(i, j int) bool {
		a, b := groups[worst[i]], groups[worst[j]]
		if a.failed != b.failed {
			return a.failed > b.failed
		}
		if a.runs != b.runs {
			return a.runs > b.runs
		}
		return worst[i] < worst[j]
	})
	for _, label := range worst {
		if len(out.Top) >= protocol.StruggleTop {
			break
		}
		g := groups[label]
		text := label
		if g.runs > 1 {
			text += fmt.Sprintf(" x%d", g.runs)
		}
		if g.failed > 0 {
			text += fmt.Sprintf(" (%d failed)", g.failed)
		}
		out.Top = append(out.Top, shortStruggleText(text, 48))
	}
	return out
}

// shortStruggleText keeps a top offender short, one line, with secrets masked.
func shortStruggleText(text string, max int) string {
	text = strings.Join(strings.Fields(redact.Secrets(text)), " ")
	return redact.Cut(text, max)
}

// normalizeCommand makes "the same command" compare by its words, not by
// spacing the model happened to use.
func normalizeCommand(command string) string {
	return shortStruggleText(command, 160)
}

// toolInputString reads one string field of a tool input.
func toolInputString(input map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, ok := input[key]; ok {
			switch value := v.(type) {
			case string:
				if value != "" {
					return value
				}
			case []any:
				var parts []string
				for _, item := range value {
					if text, ok := item.(string); ok {
						parts = append(parts, text)
					}
				}
				if len(parts) > 0 {
					return strings.Join(parts, " ")
				}
			}
		}
	}
	return ""
}

// struggleToolCall reads the command and the edit nature of one tool call.
func struggleToolCall(name string, input map[string]any) (label string, command, edit bool) {
	cmd := toolInputString(input, "command", "cmd")
	if cmd != "" {
		label, command = normalizeCommand(cmd), true
	} else {
		label = name
		if one := toolInputString(input, "file_path", "path", "pattern", "query", "url"); one != "" {
			label += " " + shortStruggleText(one, 60)
		}
	}
	return label, command, struggleEditCall(name, cmd)
}

// scanTraceTail reads a trace from its end, at most max bytes, one line at a
// time, and reports whether only the end was read.
func scanTraceTail(path string, max int64, onLine func([]byte)) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return false, err
	}
	size := fi.Size()
	start := int64(0)
	if size > max {
		start, size = size-max, max
	}
	if start > 0 {
		if _, err := f.Seek(start, io.SeekStart); err != nil {
			return false, err
		}
	}
	r := bufio.NewReaderSize(f, 1<<16)
	capped := start > 0
	first := capped
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			if first {
				// The first line of a tail read starts in the middle of one.
				first = false
			} else {
				onLine(line)
			}
		}
		if err != nil {
			if err != io.EOF {
				return capped, err
			}
			return capped, nil
		}
	}
}

// ---- Claude Code ----

// readClaudeStruggle counts the signs in a Claude Code transcript. An
// assistant tool_use block is a call; the matching user tool_result says
// whether it failed.
func readClaudeStruggle(path string) protocol.Struggle {
	c := newStruggleCollector()
	capped, _ := scanTraceTail(path, protocol.StruggleTraceMaxBytes, func(line []byte) {
		claudeStruggleLine(line, c)
	})
	st := c.counts()
	st.Trace, st.Capped = "transcript", capped
	return st
}

func claudeStruggleLine(line []byte, c *struggleCollector) {
	var e struct {
		Type    string `json:"type"`
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &e) != nil || (e.Type != "assistant" && e.Type != "user") || len(e.Message.Content) == 0 {
		return
	}
	var blocks []struct {
		Type      string          `json:"type"`
		ID        string          `json:"id"`
		ToolUseID string          `json:"tool_use_id"`
		Name      string          `json:"name"`
		Input     json.RawMessage `json:"input"`
		Content   json.RawMessage `json:"content"`
		IsError   bool            `json:"is_error"`
	}
	if json.Unmarshal(e.Message.Content, &blocks) != nil {
		return
	}
	for _, b := range blocks {
		switch b.Type {
		case "tool_use":
			var input map[string]any
			_ = json.Unmarshal(b.Input, &input)
			label, command, edit := struggleToolCall(b.Name, input)
			c.add(b.ID, label, command, edit)
		case "tool_result":
			c.result(b.ToolUseID, b.IsError, struggleResultText(b.Content))
		}
	}
}

// struggleResultText reads a Claude tool result: a string or a list of blocks.
func struggleResultText(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// ---- Codex ----

// codexRolloutPath finds the rollout file of a Codex thread under home.
func codexRolloutPath(home, id string) string {
	if home == "" || id == "" {
		return ""
	}
	var found string
	for _, sub := range []string{"sessions", "archived_sessions"} {
		_ = filepath.WalkDir(filepath.Join(home, sub), func(path string, e os.DirEntry, err error) error {
			if err != nil || found != "" || e.IsDir() {
				return nil
			}
			name := e.Name()
			if name == id+".jsonl" || strings.HasSuffix(name, "-"+id+".jsonl") {
				found = path
			}
			return nil
		})
		if found != "" {
			return found
		}
	}
	return ""
}

// readCodexStruggle counts the signs in a Codex rollout file. A
// function_call item is a call; its function_call_output carries the exit.
func readCodexStruggle(path string) protocol.Struggle {
	c := newStruggleCollector()
	capped, _ := scanTraceTail(path, protocol.StruggleTraceMaxBytes, func(line []byte) {
		codexStruggleLine(line, c)
	})
	st := c.counts()
	st.Trace, st.Capped = "rollout", capped
	return st
}

func codexStruggleLine(line []byte, c *struggleCollector) {
	var e struct {
		Type    string `json:"type"`
		Payload struct {
			Type      string          `json:"type"`
			ID        string          `json:"id"`
			CallID    string          `json:"call_id"`
			Name      string          `json:"name"`
			Command   []string        `json:"command"`
			Arguments json.RawMessage `json:"arguments"`
			Output    json.RawMessage `json:"output"`
			Success   *bool           `json:"success"`
			ExitCode  *int            `json:"exit_code"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &e) != nil || e.Type != "response_item" {
		return
	}
	p := e.Payload
	switch p.Type {
	case "function_call", "custom_tool_call", "local_shell_call":
		id := firstText(p.CallID, p.ID)
		input := map[string]any{}
		if len(p.Command) > 0 {
			input["command"] = strings.Join(p.Command, " ")
		}
		if len(p.Arguments) > 0 {
			var fromArgs map[string]any
			if json.Unmarshal(p.Arguments, &fromArgs) == nil {
				for k, v := range fromArgs {
					if _, exists := input[k]; !exists {
						input[k] = v
					}
				}
			}
		}
		name := p.Name
		if name == "" {
			name = "shell"
		}
		label, command, edit := struggleToolCall(name, input)
		c.add(id, label, command, edit)
	case "function_call_output", "custom_tool_call_output":
		failed, text := codexOutput(p.Output, p.Success, p.ExitCode)
		c.result(p.CallID, failed, text)
	}
}

// codexOutput tells whether a Codex tool output failed, and its text.
func codexOutput(raw json.RawMessage, success *bool, exit *int) (bool, string) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		if success != nil && !*success {
			return true, text
		}
		return false, text
	}
	var out struct {
		Output   string `json:"output"`
		Success  *bool  `json:"success"`
		ExitCode *int   `json:"exit_code"`
		Metadata struct {
			ExitCode *int `json:"exit_code"`
		} `json:"metadata"`
	}
	if json.Unmarshal(raw, &out) == nil {
		text = out.Output
		if out.Success != nil {
			success = out.Success
		}
		if out.ExitCode != nil {
			exit = out.ExitCode
		}
		if out.Metadata.ExitCode != nil {
			exit = out.Metadata.ExitCode
		}
	}
	if success != nil && !*success {
		return true, text
	}
	if exit != nil && *exit != 0 {
		return true, text
	}
	return false, text
}

func firstText(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// ---- OpenCode ----

// openCodeStruggle reads the worker's messages from its OpenCode server.
func (opencodeLauncher) struggle(ctx context.Context, d *Daemon, w *worker) protocol.Struggle {
	d.wmu.Lock()
	base, pass, version, identity, nativeID := w.apiBase, w.apiPass, w.apiVersion, w.apiIdentity, w.nativeID
	d.wmu.Unlock()
	if base == "" || nativeID == "" {
		return protocol.Struggle{Trace: protocol.StruggleNoTrace}
	}
	routes, err := d.discoverOpenCodeRoutes(ctx, base, pass, version, identity)
	if err != nil {
		return protocol.Struggle{Trace: protocol.StruggleNoTrace}
	}
	path, err := routes.Path(opencodeapi.Messages, nativeID)
	if err != nil {
		return protocol.Struggle{Trace: protocol.StruggleNoTrace}
	}
	req, err := http.NewRequestWithContext(ctx, opencodeapi.Messages.Method(), strings.TrimRight(base, "/")+path+"?order=asc", nil)
	if err != nil {
		return protocol.Struggle{Trace: protocol.StruggleNoTrace}
	}
	req.SetBasicAuth("opencode", pass)
	resp, err := openCodeHTTPClient(10 * time.Second).Do(req)
	if err != nil {
		return protocol.Struggle{Trace: protocol.StruggleNoTrace}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return protocol.Struggle{Trace: protocol.StruggleNoTrace}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, protocol.StruggleTraceMaxBytes))
	if err != nil {
		return protocol.Struggle{Trace: protocol.StruggleNoTrace}
	}
	st, ok := parseOpenCodeStruggle(body)
	if !ok {
		return protocol.Struggle{Trace: protocol.StruggleNoTrace}
	}
	st.Trace = "messages"
	return st
}

// parseOpenCodeStruggle counts the signs in a messages response. It accepts
// the parts shape OpenCode uses (a tool part with a state) and a flat
// tool call in a content list.
func parseOpenCodeStruggle(raw []byte) (protocol.Struggle, bool) {
	var doc struct {
		Data []struct {
			Type    string `json:"type"`
			Content []struct {
				Type  string         `json:"type"`
				ID    string         `json:"id"`
				Tool  string         `json:"tool"`
				Input map[string]any `json:"input"`
				State struct {
					Status   string         `json:"status"`
					Input    map[string]any `json:"input"`
					Output   string         `json:"output"`
					Error    string         `json:"error"`
					Metadata struct {
						ExitCode *int `json:"exitCode"`
					} `json:"metadata"`
				} `json:"state"`
			} `json:"content"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return protocol.Struggle{}, false
	}
	c := newStruggleCollector()
	n := 0
	for _, msg := range doc.Data {
		for _, part := range msg.Content {
			if part.Type != "tool" && part.Type != "tool_use" && part.Type != "tool-call" {
				continue
			}
			n++
			id := part.ID
			if id == "" {
				id = fmt.Sprintf("tool-%d", n)
			}
			input := part.Input
			if len(part.State.Input) > 0 {
				input = part.State.Input
			}
			label, command, edit := struggleToolCall(part.Tool, input)
			c.add(id, label, command, edit)
			failed := part.State.Status == "error" || part.State.Status == "failed" || part.State.Error != ""
			if part.State.Metadata.ExitCode != nil && *part.State.Metadata.ExitCode != 0 {
				failed = true
			}
			text := part.State.Output
			if text == "" {
				text = part.State.Error
			}
			c.result(id, failed, text)
		}
	}
	st := c.counts()
	return st, n > 0
}

// ---- reading a run ----

// readWorkerStruggle reads the run's tool record through its harness adapter.
// It never guesses: a harness without a reader, or a trace that cannot be
// read, is reported as "no trace".
func (d *Daemon) readWorkerStruggle(ctx context.Context, w *worker) protocol.Struggle {
	ln := d.launchers[w.harness]
	s, ok := ln.(struggler)
	if !ok {
		return protocol.Struggle{Trace: protocol.StruggleNoTrace}
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return s.struggle(ctx, d, w)
}

// noteWorkerStruggle reads the run's signs once and keeps them on the worker,
// so the notice, the caller's accept task and the full worker detail all show
// the same counts. The first caller wins; later ones reuse the result.
func (d *Daemon) noteWorkerStruggle(ctx context.Context, w *worker) protocol.Struggle {
	d.wmu.Lock()
	if w.struggleDone {
		st := w.struggle
		d.wmu.Unlock()
		return st
	}
	d.wmu.Unlock()
	st := d.readWorkerStruggle(ctx, w)
	d.wmu.Lock()
	w.struggle, w.struggleDone = st, true
	if w.info == nil {
		w.info = &protocol.WorkerInfo{ID: w.id, StartRef: w.startRef, Owner: w.owner, Project: w.project, StartedAt: w.startedAt.Unix(), Task: w.taskText, Title: w.title}
	}
	var updated *protocol.WorkerInfo
	if w.info != nil {
		cp := *w.info
		counts := st
		cp.Struggle = &counts
		w.info = &cp
		snap := cp
		updated = &snap
	}
	sessionID := w.sessionID
	d.wmu.Unlock()
	if updated != nil {
		if ls := d.setSessionWorker(sessionID, updated); ls != nil {
			go d.pushSession(ls)
		}
	}
	return st
}
