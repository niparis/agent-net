package daemon

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
)

// codexRolloutContext reads the last token count from a Codex rollout file.
// Codex writes an event_msg token_count after each step, with the last step's
// input and the model's context window. The sale size keeps the read small on
// a long thread.
func codexRolloutContext(path string) (tokens, limit int64) {
	if path == "" {
		return 0, 0
	}
	f, err := os.Open(path)
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return 0, 0
	}
	const tail = 4 << 20
	start := fi.Size() - tail
	if start < 0 {
		start = 0
	}
	if _, err := f.Seek(start, 0); err != nil {
		return 0, 0
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	first := start > 0
	for sc.Scan() {
		line := sc.Bytes()
		if first {
			// The first line may be cut in half.
			first = false
			if i := bytes.IndexByte(line, '\n'); i >= 0 {
				line = line[i+1:]
			} else {
				continue
			}
		}
		if !bytes.Contains(line, []byte("token_count")) {
			continue
		}
		var e struct {
			Type    string `json:"type"`
			Payload struct {
				Type string `json:"type"`
				Info struct {
					Last struct {
						Input int64 `json:"input_tokens"`
					} `json:"last_token_usage"`
					ContextWindow int64 `json:"model_context_window"`
				} `json:"info"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &e) != nil || e.Type != "event_msg" || e.Payload.Type != "token_count" {
			continue
		}
		if e.Payload.Info.Last.Input > 0 {
			tokens = e.Payload.Info.Last.Input
		}
		if e.Payload.Info.ContextWindow > 0 {
			limit = e.Payload.Info.ContextWindow
		}
	}
	return tokens, limit
}
