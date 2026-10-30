package daemon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
)

func TestFakeOpenCodePromptContract(t *testing.T) {
	dir := t.TempDir()
	port, err := freePort()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "serve", "--hostname", "127.0.0.1", "--port", strconv.Itoa(port))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Dir = dir
	cmd.Env = workerEnv(os.Environ(), map[string]string{"OPENCODE_SERVER_PASSWORD": "test-password", "AGENT_NET_CTO": "1", "XDG_DATA_HOME": filepath.Join(dir, "data")})
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); cmd.Wait() })
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	call := func(method, path, body string) (*http.Response, error) {
		req, err := http.NewRequest(method, base+path, bytes.NewBufferString(body))
		if err != nil {
			return nil, err
		}
		req.SetBasicAuth("opencode", "test-password")
		req.Header.Set("Content-Type", "application/json")
		return http.DefaultClient.Do(req)
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
}
