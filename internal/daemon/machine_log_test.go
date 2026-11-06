package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestMachineLogFilesFilterLimitsRedaction(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 1, 2, 12, 0, 0, 0, time.Local)
	path := filepath.Join(dir, "agent-net-daemon.log")
	token := "sk-abcdefghijklmnopqrstuvwxyz123456"
	old := "daemon: 2026/01/02 09:00:00 old match\n"
	rotated := "daemon: 2026/01/02 11:10:00 match token=" + token + "\n"
	current := "daemon: 2026/01/02 11:20:00 skip\ndaemon: 2026/01/02 11:30:00 match newer\n"
	for name, text := range map[string]string{path + ".2": old, path + ".1": rotated, path: current} {
		if err := os.WriteFile(name, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	q := protocol.MachineLogRequest{Machine: "box", Log: "daemon", Since: "1h", Filter: "match", Limit: 1}
	res, err := readMachineLogFiles(context.Background(), dir, q, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Lines) != 1 || !strings.Contains(res.Lines[0], "newer") || !res.Truncated {
		t.Fatalf("limit/filter: %+v", res)
	}
	q.Limit = 10
	res, err = readMachineLogFiles(context.Background(), dir, q, now)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(res)
	if len(res.Lines) != 2 || strings.Contains(string(data), token) || !strings.Contains(string(data), "[REDACTED]") {
		t.Fatalf("redaction/window: %s", data)
	}
	t.Logf("sample: %s", data)
	q.Log = "../../secrets"
	if _, err = readMachineLogFiles(context.Background(), dir, q, now); err == nil {
		t.Fatal("unknown log accepted")
	}
	q.Log = "daemon"
	q.Filter = ""
	q.Limit = 1001
	if _, err = readMachineLogFiles(context.Background(), dir, q, now); err == nil {
		t.Fatal("over-limit accepted")
	}
	var short strings.Builder
	for i := 0; i < 1200; i++ {
		fmt.Fprintf(&short, "daemon: 2026/01/02 11:30:00 short %04d\n", i)
	}
	if err = os.WriteFile(path, []byte(short.String()), 0600); err != nil {
		t.Fatal(err)
	}
	q.Limit = 1000
	res, err = readMachineLogFiles(context.Background(), dir, q, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Lines) != 1000 || !res.Truncated || !strings.Contains(res.Lines[len(res.Lines)-1], "1199") {
		t.Fatalf("line cap: %+v", res)
	}
	var b strings.Builder
	for i := 0; i < 1200; i++ {
		fmt.Fprintf(&b, "daemon: 2026/01/02 11:30:00 line %04d %s\n", i, strings.Repeat("x", 400))
	}
	if err = os.WriteFile(path, []byte(b.String()), 0600); err != nil {
		t.Fatal(err)
	}
	q.Limit = 1000
	res, err = readMachineLogFiles(context.Background(), dir, q, now)
	if err != nil {
		t.Fatal(err)
	}
	data, _ = json.Marshal(res)
	if len(res.Lines) > 1000 || len(data) > 256*1024 || !res.Truncated || !strings.Contains(res.Lines[len(res.Lines)-1], "1199") {
		t.Fatalf("caps: lines=%d bytes=%d truncated=%v", len(res.Lines), len(data), res.Truncated)
	}
}

func TestMachineLogRedactsMultilineAndOversized(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	path := filepath.Join(dir, "agent-net-gateway.log")
	stamp := "ts=" + now.UTC().Format(time.RFC3339Nano) + " "
	text := stamp + "event=begin -----BEGIN PRIVATE KEY-----\nvery-secret-key-material\n-----END PRIVATE KEY-----\n" + stamp + "event=run --token fake-secret-token-value\n"
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	q := protocol.MachineLogRequest{Machine: "box", Log: "gateway"}
	res, err := readMachineLogFiles(context.Background(), dir, q, now)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(res)
	if strings.Contains(string(raw), "very-secret-key-material") || strings.Contains(string(raw), "fake-secret-token-value") {
		t.Fatalf("secret leaked: %s", raw)
	}
	if err = os.WriteFile(path, []byte(stamp+strings.Repeat("x", maxLogLine+1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = readMachineLogFiles(context.Background(), dir, q, now); err == nil {
		t.Fatal("oversized line accepted")
	}
}

func TestMachineLogRelayAgentAndCTO(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	text := "ts=" + now.UTC().Format(time.RFC3339Nano) + " event=diagnostic token=sk-abcdefghijklmnopqrstuvwxyz123456\n"
	if err := os.WriteFile(filepath.Join(dir, "agent-net-gateway.log"), []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	g := startGateway(t, t.TempDir(), "")
	caller := startNode(t, g, nodeOpts{detect: fakeInventory, cmdline: "pi"})
	target := startNode(t, g, nodeOpts{detect: fakeInventory, cfg: func(c *Config) {
		c.testLogRead = func(ctx context.Context, q protocol.MachineLogRequest) (protocol.MachineLogResult, error) {
			return readMachineLogFiles(ctx, dir, q, time.Now())
		}
	}})
	agent, _ := helloPi(t, caller, "log-peer", "/fixture", true)
	args := map[string]any{"machine": target.cfg.Hostname, "log": "gateway", "filter": "diagnostic"}
	raw := agent.mustCall("machine_log", args)
	if !strings.Contains(string(raw), "[REDACTED]") || strings.Contains(string(raw), "abcdefghijklmnopqrstuvwxyz123456") {
		t.Fatalf("agent result: %s", raw)
	}
	encoded, _ := json.Marshal(args)
	value, err := caller.d.runCTOToolRaw(context.Background(), "agent_machine_log", encoded)
	if err != nil {
		t.Fatal(err)
	}
	cto, _ := json.Marshal(value)
	var agentResult, ctoResult protocol.MachineLogResult
	if json.Unmarshal(raw, &agentResult) != nil || json.Unmarshal(cto, &ctoResult) != nil || strings.Join(agentResult.Lines, "\n") != strings.Join(ctoResult.Lines, "\n") {
		t.Fatalf("CTO %s differs from agent %s", cto, raw)
	}
	if _, err = agent.call("machine_log", map[string]any{"machine": target.cfg.Hostname, "log": "../../private"}); err == nil {
		t.Fatal("invalid log accepted")
	}
	t.Logf("sample: %s", raw)
}

func TestMachineLogJournalFixedCommand(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux journal reader")
	}
	dir := t.TempDir()
	output := filepath.Join(dir, "args")
	script := "#!/bin/sh\nprintf '%s\n' \"$@\" > '" + output + "'\nprintf '%s\n' 'fixture api_key=sample-secret-value'\n"
	if err := os.WriteFile(filepath.Join(dir, "journalctl"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	d := &Daemon{}
	result, err := d.readMachineLog(context.Background(), protocol.MachineLogRequest{Machine: "box", Log: "daemon", Since: "2h"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Lines) != 1 || strings.Contains(result.Lines[0], "sample-secret-value") {
		t.Fatalf("journal output: %+v", result)
	}
	args, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "--unit=agent-net-daemon\n") || !strings.Contains(string(args), "--since=") {
		t.Fatalf("journal args: %s", args)
	}
	if _, err := d.readMachineLog(context.Background(), protocol.MachineLogRequest{Machine: "box", Log: "daemon;bad"}); err == nil {
		t.Fatal("unsafe log accepted")
	}
}

func TestMachineLogScanRejectsInputCutInsideLine(t *testing.T) {
	q := protocol.MachineLogRequest{Machine: "box", Log: "daemon", Limit: 200}
	c := newLogCollector(q, time.Time{})
	input := strings.Repeat("x\r\n", 20) + "sk-abcdefghijklmnopqrstuvwxyz123456"
	if err := scanLogLimit(context.Background(), strings.NewReader(input), c, false, 70); err == nil {
		t.Fatal("input cut inside a line was accepted")
	}
}

func TestMachineLogDoesNotReturnPartialLastLine(t *testing.T) {
	q := protocol.MachineLogRequest{Machine: "box", Log: "daemon", Limit: 200}
	c := newLogCollector(q, time.Time{})
	if err := scanLogLimit(context.Background(), strings.NewReader("safe\nsk-short"), c, false, 100); err != nil {
		t.Fatal(err)
	}
	if len(c.result.Lines) != 1 || c.result.Lines[0] != "safe" {
		t.Fatalf("partial log line returned: %v", c.result.Lines)
	}
}
