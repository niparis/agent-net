package main

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestUpgradeHelpExplainsUnorderableVersions(t *testing.T) {
	if os.Getenv("AGENT_NET_TEST_UPGRADE_HELP") == "1" {
		cmdUpgrade(context.Background(), []string{"--help"})
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestUpgradeHelpExplainsUnorderableVersions$")
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir(), "AGENT_NET_STATE_DIR="+t.TempDir(), "AGENT_NET_TEST_UPGRADE_HELP=1")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "versions that cannot be ordered") {
		t.Fatalf("upgrade help omits unorderable versions: %v: %s", err, out)
	}
}
