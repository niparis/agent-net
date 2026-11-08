package daemon

import (
	"context"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Variables of the fake shared server that forks. The server writes
// "<its pid> <child pid>" to the file the first variable names, then starts
// the ordinary test MCP server. With the stubborn variable it also ignores
// SIGTERM and stays alive after its input closes, like a server that hangs.
const (
	forkFileEnv = "AGENT_NET_TEST_MCP_FORK_FILE"
	stubbornEnv = "AGENT_NET_TEST_MCP_STUBBORN"
)

// runForkingHelper is called by the test binary when it acts as a hosted
// server. It does nothing unless the fork variable is set.
func runForkingHelper() {
	path := os.Getenv(forkFileEnv)
	if path == "" {
		return
	}
	if os.Getenv(stubbornEnv) != "" {
		signal.Ignore(syscall.SIGTERM)
	}
	child := exec.Command("sleep", "600")
	if os.Getenv(stubbornEnv) != "" {
		// A child that ignores SIGTERM too: sh -c 'trap "" TERM; sleep 600'.
		child = exec.Command("sh", "-c", `trap "" TERM; while :; do sleep 1; done`)
	}
	if err := child.Start(); err != nil {
		os.Exit(1)
	}
	tmp := path + ".tmp"
	os.WriteFile(tmp, []byte(strconv.Itoa(os.Getpid())+" "+strconv.Itoa(child.Process.Pid)), 0o600)
	os.Rename(tmp, path)
}

// holdIfStubborn keeps a stubborn fake server alive after its input closes.
func holdIfStubborn() {
	if os.Getenv(stubbornEnv) != "" {
		select {}
	}
}

func readForkPids(t *testing.T, path string) (int, int) {
	t.Helper()
	var parent, child int
	eventually(t, "fake server wrote its pids", func() bool {
		b, err := os.ReadFile(path)
		if err != nil {
			return false
		}
		f := strings.Fields(string(b))
		if len(f) != 2 {
			return false
		}
		parent, _ = strconv.Atoi(f[0])
		child, _ = strconv.Atoi(f[1])
		return parent > 0 && child > 0
	})
	return parent, child
}

// procGone is true when no live process has the pid. A zombie counts as gone.
func procGone(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return true
	}
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	return i >= 0 && len(s) > i+2 && s[i+2] == 'Z'
}

func killLeft(pids ...int) {
	for _, p := range pids {
		syscall.Kill(p, syscall.SIGKILL)
	}
}

func hostForkingServer(t *testing.T, stubborn bool) (*node, string) {
	t.Helper()
	useHelper(t)
	forkFile := filepath.Join(t.TempDir(), "pids")
	t.Setenv(forkFileEnv, forkFile)
	t.Setenv(stubbornEnv, "")
	if stubborn {
		t.Setenv(stubbornEnv, "1")
	}
	g := startGateway(t, t.TempDir(), "")
	a := startNode(t, g, nodeOpts{cmdline: "pi"})
	cli := cliClient(t, a)
	srv := stdioSpec("forker", forkFileEnv, stubbornEnv)
	cli.mustCall("mcp_add", protocol.MCPAdd{Server: srv})
	return a, forkFile
}

// Stopping the daemon ends the hosted server and everything it started.
func TestMCPHostStopEndsServerAndItsChild(t *testing.T) {
	a, forkFile := hostForkingServer(t, false)
	parent, child := readForkPids(t, forkFile)
	t.Cleanup(func() { killLeft(parent, child) })

	a.d.Stop()

	eventually(t, "server and its child are gone", func() bool { return procGone(parent) && procGone(child) })
}

// A server that ignores SIGTERM and its closed input is killed after a short wait.
func TestMCPHostStopKillsStubbornServer(t *testing.T) {
	a, forkFile := hostForkingServer(t, true)
	parent, child := readForkPids(t, forkFile)
	t.Cleanup(func() { killLeft(parent, child) })

	start := time.Now()
	a.d.Stop()
	if took := time.Since(start); took > scaled(15*time.Second) {
		t.Errorf("stop took %s", took)
	}
	eventually(t, "server and its child are gone", func() bool { return procGone(parent) && procGone(child) })
}

// A new daemon ends a server group that an earlier daemon left behind, and
// leaves other processes alone.
func TestMCPHostStartEndsLeftoverGroup(t *testing.T) {
	stateDir := t.TempDir()

	// A leftover group: leader and a child, started as the host starts servers.
	leader := exec.Command("sh", "-c", "sleep 600 & wait")
	leader.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := leader.Start(); err != nil {
		t.Fatal(err)
	}
	go leader.Wait()
	t.Cleanup(func() { killLeft(-leader.Process.Pid) })
	// A bystander that is not ours and has no record.
	other := exec.Command("sleep", "600")
	other.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	go other.Wait()
	t.Cleanup(func() { killLeft(other.Process.Pid) })
	// A record whose pid now belongs to another process (wrong start time).
	reused := exec.Command("sleep", "600")
	reused.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := reused.Start(); err != nil {
		t.Fatal(err)
	}
	go reused.Wait()
	t.Cleanup(func() { killLeft(reused.Process.Pid) })

	recDir := filepath.Join(stateDir, mcpPidDirName)
	if err := os.MkdirAll(recDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeMCPRecord(t, recDir, leader.Process.Pid, procStartID(leader.Process.Pid))
	writeMCPRecord(t, recDir, reused.Process.Pid, "not-the-start-time")

	d := &Daemon{cfg: Config{StateDir: stateDir}}
	d.log = log.New(io.Discard, "", 0)
	h := &mcpHost{d: d}
	h.start(context.Background())

	eventually(t, "leftover leader gone", func() bool { return procGone(leader.Process.Pid) })
	time.Sleep(100 * time.Millisecond)
	if procGone(other.Process.Pid) {
		t.Error("ended a process the host never started")
	}
	if procGone(reused.Process.Pid) {
		t.Error("ended a process whose pid was reused")
	}
	if entries, _ := os.ReadDir(recDir); len(entries) != 0 {
		t.Errorf("records left: %v", entries)
	}
}

func writeMCPRecord(t *testing.T, dir string, pid int, start string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, strconv.Itoa(pid)), []byte(strconv.Itoa(pid)+" "+start+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}
