package daemon

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestRoleSetupProbeRealOpenCode(t *testing.T) {
	if os.Getenv("AGENT_NET_TEST_REAL_OPENCODE") != "1" {
		t.Skip("set AGENT_NET_TEST_REAL_OPENCODE=1 to run the real OpenCode check")
	}
	bin, err := exec.LookPath("opencode")
	if err != nil {
		t.Skip("opencode is not on PATH")
	}
	root := shortDir(t)
	for _, key := range []string{"HOME", "XDG_DATA_HOME", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"} {
		dir := filepath.Join(root, key)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		t.Setenv(key, dir)
	}
	t.Setenv("OPENAI_API_KEY", "")
	provider := newCTOProbeProvider(t)
	config := fmt.Sprintf(`{"provider":{"openai":{"options":{"baseURL":%q,"apiKey":"not-a-key"},"models":{"probe-model":{"name":"probe-model"}}}},"model":"openai/probe-model"}`, provider.srv.URL+"/v1")
	t.Setenv("OPENCODE_CONFIG_CONTENT", config)
	d := &Daemon{cfg: Config{HarnessPaths: map[string]string{"opencode": bin}}}
	got := d.probeSetupRoleModel(context.Background(), protocol.RoleModel{Harness: "opencode", Model: "openai/probe-model"})
	if !got.OK {
		t.Fatalf("real OpenCode and fake model: %+v", got)
	}
}
