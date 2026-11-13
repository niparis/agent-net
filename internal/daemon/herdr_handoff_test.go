package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// fakeHerdrServer answers herdr API requests on the socket of the fake
// herdr's "agent-net" session, in the test process. It starts without
// agent-net's config, like a server that ran before v0.5.36. A live handoff
// runs the import program the way herdr does; from then on the server runs
// with the config that program set.
type fakeHerdrServer struct {
	mu          sync.Mutex
	attached    bool // a client is attached
	noHandoff   bool // the herdr has no live handoff
	methods     []string
	runningWith string // HERDR_CONFIG_PATH of the server process
	envLog      string // the fake herdr's record of each call's config path
}

// startFakeHerdrServer serves the fake herdr's agent-net session at log's
// socket, marks that session running, and shows the server's environment to
// the daemon in place of the test process's own.
func startFakeHerdrServer(t *testing.T, log string) *fakeHerdrServer {
	t.Helper()
	f := &fakeHerdrServer{envLog: fakeHerdrEnvLog(log)}
	socket := fakeOwnHerdrSocket(log)
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(log), "own-running"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(t, conn)
		}
	}()
	orig := processEnviron
	self := os.Getpid()
	processEnviron = func(pid int) []string {
		if pid != self {
			return orig(pid)
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.runningWith == "" {
			return nil
		}
		return []string{"HERDR_CONFIG_PATH=" + f.runningWith}
	}
	t.Cleanup(func() { processEnviron = orig })
	return f
}

func (f *fakeHerdrServer) serve(t *testing.T, conn net.Conn) {
	defer conn.Close()
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return // a connection that only asked for the server's pid
	}
	var req struct {
		Method string            `json:"method"`
		Params map[string]string `json:"params"`
	}
	if err := json.Unmarshal(line, &req); err != nil {
		t.Errorf("bad request %q", line)
		return
	}
	f.mu.Lock()
	f.methods = append(f.methods, req.Method)
	attached, handoff := f.attached, !f.noHandoff
	f.mu.Unlock()
	var result any
	switch req.Method {
	case "ping":
		result = map[string]any{"type": "pong", "capabilities": map[string]bool{"live_handoff": handoff}}
	case "client.window_title.clear":
		reason := "no_foreground_client"
		if attached {
			reason = "cleared"
		}
		result = map[string]any{"type": "client_window_title", "changed": attached, "reason": reason}
	case "server.live_handoff":
		// herdr runs the import program as the new server; the fake herdr
		// records the config it got.
		cmd := exec.Command(req.Params["import_exe"], "server", "--handoff-import", "/nonexistent/handoff.sock", "token")
		cmd.Env = herdrCleanEnv()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("import program failed: %v %s", err, out)
		}
		raw, _ := os.ReadFile(f.envLog)
		for _, line := range strings.Split(string(raw), "\n") {
			if config, ok := strings.CutPrefix(line, "server --handoff-import /nonexistent/handoff.sock token|"); ok {
				f.mu.Lock()
				f.runningWith = config
				f.mu.Unlock()
			}
		}
		result = map[string]string{"type": "ok"}
	default:
		t.Errorf("unexpected herdr request %s", req.Method)
		return
	}
	body, _ := json.Marshal(map[string]any{"id": "agent-net", "result": result})
	fmt.Fprintf(conn, "%s\n", body)
}

func (f *fakeHerdrServer) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.methods...)
}

func (f *fakeHerdrServer) set(change func(*fakeHerdrServer)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change(f)
}

func TestHerdrHandoffMissingConfig(t *testing.T) {
	state, _ := sidebarTestEnv(t, ownerHerdrConfig)
	_, log := fakeOpenHerdr(t)
	server := startFakeHerdrServer(t, log)
	d := &Daemon{}
	plan := protocol.EnterPlan{Argv: []string{"claude", "attach", "job"}}

	if p := d.openInHerdr("one", "garden", plan); !p.OpenedHerdr || strings.Contains(p.Note, "sidebar") {
		t.Fatalf("plan %+v", p)
	}
	want := []string{"ping", "client.window_title.clear", "server.live_handoff"}
	if got := server.requests(); !reflect.DeepEqual(got, want) {
		t.Fatalf("herdr requests %v, want %v", got, want)
	}
	config := filepath.Join(state, "herdr", "agent-net-config.toml")
	env := herdrEnvLines(t, log)
	if got := env["server --handoff-import /nonexistent/handoff.sock token"]; got != config {
		t.Fatalf("the new server ran with HERDR_CONFIG_PATH=%q, want %q", got, config)
	}
	if _, restarted := env["--session agent-net server"]; restarted {
		t.Fatal("agent-net started a new herdr server, which closes every pane")
	}

	if p := d.openInHerdr("two", "orchard", plan); !p.OpenedHerdr || strings.Contains(p.Note, "sidebar") {
		t.Fatalf("second plan %+v", p)
	}
	if got := server.requests(); len(got) != len(want) {
		t.Fatalf("a server with agent-net's config was asked again: %v", got)
	}
}

func TestHerdrHandoffWaitsForClients(t *testing.T) {
	sidebarTestEnv(t, ownerHerdrConfig)
	_, log := fakeOpenHerdr(t)
	server := startFakeHerdrServer(t, log)
	server.set(func(f *fakeHerdrServer) { f.attached = true })
	d := &Daemon{}
	plan := protocol.EnterPlan{Argv: []string{"claude", "attach", "job"}}

	note := fmt.Sprintf(herdrSidebarWaitNote, "prefix+b")
	if p := d.openInHerdr("one", "garden", plan); !p.OpenedHerdr || !strings.HasPrefix(p.Note, note+" ") {
		t.Fatalf("plan %+v, want the note %q", p, note)
	}
	if p := d.openInHerdr("two", "orchard", plan); strings.Contains(p.Note, "sidebar") {
		t.Fatalf("the owner was told twice: %q", p.Note)
	}
	for _, m := range server.requests() {
		if m == "server.live_handoff" {
			t.Fatal("handed off while a client was attached; that ends the client")
		}
	}

	server.set(func(f *fakeHerdrServer) { f.attached = false })
	if p := d.openInHerdr("three", "meadow", plan); strings.Contains(p.Note, "sidebar") {
		t.Fatalf("plan %+v", p)
	}
	if got := server.requests(); got[len(got)-1] != "server.live_handoff" {
		t.Fatalf("no handoff once the clients left: %v", got)
	}
}

func TestHerdrHandoffUnsupported(t *testing.T) {
	sidebarTestEnv(t, ownerHerdrConfig)
	_, log := fakeOpenHerdr(t)
	server := startFakeHerdrServer(t, log)
	server.set(func(f *fakeHerdrServer) { f.noHandoff = true })

	p := (&Daemon{}).openInHerdr("one", "garden", protocol.EnterPlan{Argv: []string{"claude", "attach", "job"}})
	if want := fmt.Sprintf(herdrSidebarRestartNote, "prefix+b"); !strings.HasPrefix(p.Note, want+" ") {
		t.Fatalf("note %q, want %q", p.Note, want)
	}
	if got := strings.Join(server.requests(), " "); got != "ping" {
		t.Fatalf("herdr requests %q", got)
	}
}
