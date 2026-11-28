package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMacosSignNeedsOneBinary(t *testing.T) {
	var stderr bytes.Buffer
	if code := run(context.Background(), []string{"macos-sign"}, &stderr); code != 1 || !strings.Contains(stderr.String(), "usage: agent-net macos-sign <binary>") {
		t.Fatalf("code %d, stderr %q", code, stderr.String())
	}
}

func TestMacosSignLeavesTheBinaryAloneOffMacOS(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("checks the other systems")
	}
	t.Setenv("AGENT_NET_STATE_DIR", t.TempDir())
	bin := filepath.Join(t.TempDir(), "agent-net")
	os.WriteFile(bin, []byte("BINARY"), 0o755)
	var stderr bytes.Buffer
	if code := run(context.Background(), []string{"macos-sign", bin}, &stderr); code != 0 {
		t.Fatalf("code %d, stderr %q", code, stderr.String())
	}
	if b, _ := os.ReadFile(bin); string(b) != "BINARY" {
		t.Fatalf("binary changed: %q", b)
	}
}
