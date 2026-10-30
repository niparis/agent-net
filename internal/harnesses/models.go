package harnesses

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"github.com/niparis/agent-net/internal/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Model lists for the harnesses (decision 0002, W17). OpenCode prints its list
// with `opencode models`. Pi prints a table with `pi --list-models`. Codex's
// app server answers the JSON-RPC method model/list on its stdio transport.
// Claude Code has no list command: its aliases and the full names we know are
// a small table below.
//
// A harness whose list cannot be read reports no list, and a loose model name
// for it is passed through as the caller gave it.

// claudeModels are the values Claude Code accepts after --model. The aliases
// are documented in its help text. The full names are the families we have
// seen; a name that is missing still passes through, so this table only makes
// loose names resolve to a full id.
var claudeModels = []string{
	"fable", "haiku", "opus", "sonnet",
	"claude-fable-5",
	"claude-haiku-4-5", "claude-haiku-4-5-20251001",
	"claude-haiku-5", "claude-haiku-5-5",
	"claude-opus-4-7", "claude-opus-4-8",
	"claude-opus-5", "claude-opus-5-5",
	"claude-sonnet-4-5", "claude-sonnet-4-5-20250929",
	"claude-sonnet-5", "claude-sonnet-5-5", "claude-sonnet-5-20260801",
}

// ClaudeModels returns the model names of Claude Code, sorted.
func ClaudeModels() []string {
	out := append([]string(nil), claudeModels...)
	sort.Strings(out)
	return out
}

// ParsePiModels reads the table of `pi --list-models`:
//
//	provider  model        context  max-out  thinking  images
//	openai    gpt-5        400K     128K     yes       yes
//
// The result is "provider/model", sorted, without duplicates and at most
// MaxModels long. It is nil when no model is found.
func ParsePiModels(out string) []string {
	seen := map[string]bool{}
	var models []string
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 3 || f[0] == "provider" || f[1] == "model" {
			continue
		}
		id := f[0] + "/" + f[1]
		if len(id) > maxModelLen || strings.ContainsAny(id, " \t") || seen[id] {
			continue
		}
		seen[id] = true
		models = append(models, id)
	}
	sort.Strings(models)
	if len(models) > MaxModels {
		models = models[:MaxModels]
	}
	if len(models) == 0 {
		return nil
	}
	return models
}

// codexModel is one entry of the app server's model/list result. id is the
// name Codex takes in thread/start; model is kept as a fallback for an older
// server.
type codexModel struct {
	ID            string `json:"id"`
	Model         string `json:"model"`
	DefaultEffort string `json:"defaultReasoningEffort"`
}

type codexModelList struct {
	Data       []codexModel `json:"data"`
	NextCursor string       `json:"nextCursor"`
}

type codexRPCResult struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// codexModels asks `codex app-server` (stdio) for its model list. It says
// initialize, then model/list, and follows nextCursor. Any failure gives no
// list. The caller's context bounds the whole call.
func codexModels(ctx context.Context, path, searchPATH string) ([]string, map[string]string) {
	cmd := exec.CommandContext(ctx, path, "app-server")
	cmd.Env = append(os.Environ(), "PATH="+filepath.Dir(path)+string(os.PathListSeparator)+searchPATH)
	cmd.WaitDelay = 500 * time.Millisecond // as in runProgram
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil
	}
	if err := cmd.Start(); err != nil {
		return nil, nil
	}
	defer func() {
		stdin.Close()
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		cmd.Wait()
	}()
	enc := json.NewEncoder(stdin)
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	enc.Encode(map[string]any{"id": 1, "method": "initialize", "params": map[string]any{
		"clientInfo":   map[string]any{"name": "agent-net", "version": "model-list"},
		"capabilities": map[string]any{"experimentalApi": true},
	}})
	if _, ok := readCodexAnswer(sc, 1); !ok {
		return nil, nil
	}
	enc.Encode(map[string]any{"method": "initialized"})

	seen := map[string]bool{}
	var models []string
	efforts := map[string]string{}
	cursor := ""
	nextID := 2
	for page := 0; page < 20 && len(models) < MaxModels; page++ {
		params := map[string]any{"includeHidden": true, "limit": 200}
		if cursor != "" {
			params["cursor"] = cursor
		}
		reqID := nextID
		nextID++
		if err := enc.Encode(map[string]any{"id": reqID, "method": "model/list", "params": params}); err != nil {
			break
		}
		res, ok := readCodexAnswer(sc, reqID)
		if !ok {
			break
		}
		var list codexModelList
		if json.Unmarshal(res, &list) != nil {
			break
		}
		for _, m := range list.Data {
			id := strings.TrimSpace(m.ID)
			if id == "" {
				id = strings.TrimSpace(m.Model)
			}
			if id == "" || len(id) > maxModelLen || strings.ContainsAny(id, " \t\n\r") || seen[id] {
				continue
			}
			seen[id] = true
			models = append(models, id)
			if _, level := protocol.SplitModelLevel("m:" + m.DefaultEffort); level != "" {
				efforts[id] = level
			}
		}
		cursor = list.NextCursor
		if cursor == "" {
			break
		}
	}
	sort.Strings(models)
	if len(models) > MaxModels {
		models = models[:MaxModels]
	}
	if len(models) == 0 {
		return nil, nil
	}
	return models, efforts
}

// readCodexAnswer reads lines until the answer with the wanted id. Server
// notifications and answers to initialize are skipped.
func readCodexAnswer(sc *bufio.Scanner, id int) (json.RawMessage, bool) {
	for sc.Scan() {
		var m codexRPCResult
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		if m.ID != id {
			continue
		}
		if m.Error != nil {
			return nil, false
		}
		if len(m.Result) == 0 {
			continue
		}
		return m.Result, true
	}
	return nil, false
}
