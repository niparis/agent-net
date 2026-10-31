package daemon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestFakeOpenCodePromptContract(t *testing.T) {
	dir := t.TempDir()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	file, err := ln.(*net.TCPListener).File()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { file.Close() })
	cmd := exec.Command(os.Args[0], "serve", "--hostname", "127.0.0.1", "--port", "0")
	cmd.ExtraFiles = []*os.File{file}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Dir = dir
	cmd.Env = workerEnv(os.Environ(), map[string]string{"FAKE_OC_LISTENER_FD": "3", "OPENCODE_SERVER_PASSWORD": "test-password", "AGENT_NET_CTO": "1", "XDG_DATA_HOME": filepath.Join(dir, "data")})
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); cmd.Wait() })
	base := fmt.Sprintf("http://%s", ln.Addr())
	client := &http.Client{Timeout: time.Second}
	call := func(method, path, body string) (*http.Response, error) {
		req, err := http.NewRequest(method, base+path, bytes.NewBufferString(body))
		if err != nil {
			return nil, err
		}
		req.SetBasicAuth("opencode", "test-password")
		req.Header.Set("Content-Type", "application/json")
		return client.Do(req)
	}
	eventually(t, "fake server", func() bool {
		r, e := call("GET", "/api/info", "")
		if e != nil {
			return false
		}
		r.Body.Close()
		return r.StatusCode == 200
	})
	r, err := call("POST", "/api/session", `{"title":"CTO"}`)
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	json.NewDecoder(r.Body).Decode(&created)
	r.Body.Close()
	if created.Data.ID == "" {
		t.Fatal("missing id")
	}
	r, err = call("POST", "/api/session/"+created.Data.ID+"/prompt", `{"text":"hello","delivery":"queue"}`)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatalf("prompt HTTP %d", r.StatusCode)
	}
	r, err = call("GET", "/api/session/"+created.Data.ID+"/message", "")
	if err != nil {
		t.Fatal(err)
	}
	var messages struct {
		Data []struct{ Type, Text string } `json:"data"`
	}
	json.NewDecoder(r.Body).Decode(&messages)
	r.Body.Close()
	if len(messages.Data) != 1 || messages.Data[0].Text != "hello" {
		t.Fatalf("messages: %+v", messages.Data)
	}
	// POST /api/session/:id/compact, as OpenCode 2.2.0 documents it: a durable
	// compaction request with an optional delivery, answered with the inbox
	// item.
	r, err = call("POST", "/api/session/"+created.Data.ID+"/compact", `{"delivery":"steer"}`)
	if err != nil {
		t.Fatal(err)
	}
	var compacted struct {
		Data struct {
			ID        string                  `json:"id"`
			SessionID string                  `json:"sessionID"`
			Time      struct{ Created int64 } `json:"time"`
			Type      string                  `json:"type"`
			Payload   map[string]any          `json:"payload"`
			Delivery  string                  `json:"delivery"`
		} `json:"data"`
	}
	json.NewDecoder(r.Body).Decode(&compacted)
	r.Body.Close()
	if r.StatusCode != 200 || compacted.Data.Type != "compaction" || compacted.Data.Delivery != "steer" ||
		compacted.Data.SessionID != created.Data.ID || compacted.Data.ID == "" ||
		compacted.Data.Payload == nil || compacted.Data.Time.Created == 0 {
		t.Fatalf("compact HTTP %d: %+v", r.StatusCode, compacted.Data)
	}
	// The request is durable: the next message page starts with the compaction.
	r, err = call("GET", "/api/session/"+created.Data.ID+"/message", "")
	if err != nil {
		t.Fatal(err)
	}
	var after struct {
		Data []struct{ Type string } `json:"data"`
	}
	json.NewDecoder(r.Body).Decode(&after)
	r.Body.Close()
	if len(after.Data) == 0 || after.Data[0].Type != "compaction" {
		t.Fatalf("compaction not in the message log: %+v", after.Data)
	}
	// delivery is the documented enum: steer or queue.
	r, err = call("POST", "/api/session/"+created.Data.ID+"/compact", `{"delivery":"later"}`)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != 400 {
		t.Fatalf("unknown delivery: HTTP %d", r.StatusCode)
	}
}
