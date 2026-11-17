package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/daemon/opencodeapi"
)

// The real OpenCode service uses a local fake model and a temporary home.
func TestOpenCodeRolePermissionsRealServer(t *testing.T) {
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
	t.Setenv("OPENCODE_CONFIG_CONTENT", "")
	t.Setenv("OPENAI_API_KEY", "")
	provider := newCTOProbeProvider(t)
	cwd := filepath.Join(root, "role")
	if err := os.MkdirAll(cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf(`{"provider":{"openai":{"options":{"baseURL":%q,"apiKey":"not-a-key"},"models":{"probe-model":{"name":"probe-model"}}}},"model":"openai/probe-model"}`, provider.srv.URL+"/v1")
	if err := os.WriteFile(filepath.Join(cwd, "opencode.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(cwd, ".env")
	if err := os.WriteFile(secret, []byte("FIXTURE=hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	port, err := freePort()
	if err != nil {
		t.Fatal(err)
	}
	const password = "probe-password"
	serve := exec.Command(bin, "serve", "--hostname", "127.0.0.1", "--port", strconv.Itoa(port))
	serve.Dir = cwd
	serve.Env = append(os.Environ(), "OPENCODE_SERVER_PASSWORD="+password)
	if err := serve.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serve.Process.Kill(); _, _ = serve.Process.Wait() })
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	eventually(t, "OpenCode service starts", func() bool {
		_, status, err := probeAPI(base, password, http.MethodGet, "/api/info", nil)
		return err == nil && status == http.StatusOK
	})
	rules := []ctoPermission{{Action: "*", Resource: "*", Effect: "allow"}}
	body, status, err := probeAPI(base, password, http.MethodPost, "/api/session", map[string]any{
		"title": "role probe", "location": map[string]string{"directory": cwd},
		"model": map[string]string{"providerID": "openai", "id": "probe-model"}, "permissions": rules,
	})
	if err != nil || status/100 != 2 {
		t.Fatalf("create = %d %v %s", status, err, body)
	}
	var created struct {
		Data struct {
			ID          string          `json:"id"`
			Permissions []ctoPermission `json:"permissions"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &created); err != nil || created.Data.ID == "" || len(created.Data.Permissions) != 1 || created.Data.Permissions[0] != rules[0] {
		t.Fatalf("created session = %s: %v", body, err)
	}
	text := fmt.Sprintf(`CALL read {"path":%q}`, secret)
	if _, status, err := probeAPI(base, password, http.MethodPost, "/api/session/"+created.Data.ID+"/prompt", map[string]string{"text": text, "delivery": "queue"}); err != nil || status/100 != 2 {
		t.Fatalf("prompt = %d %v", status, err)
	}
	eventually(t, "role reads a protected file without a prompt", func() bool { return provider.count() > 0 })
	if pending, _, _ := probeAPI(base, password, http.MethodGet, "/api/session/"+created.Data.ID+"/permission", nil); strings.Contains(string(pending), `"id"`) {
		t.Fatalf("role has a pending permission: %s", pending)
	}
	t.Log("new role: protected file read returned, no permission request")
	old, status, err := probeAPI(base, password, http.MethodPost, "/api/session", map[string]any{
		"title": "adopted role probe", "location": map[string]string{"directory": cwd},
		"model": map[string]string{"providerID": "openai", "id": "probe-model"},
	})
	if err != nil || status/100 != 2 {
		t.Fatalf("create adopted session = %d %v %s", status, err, old)
	}
	if err := json.Unmarshal(old, &created); err != nil || created.Data.ID == "" {
		t.Fatalf("adopted session = %s: %v", old, err)
	}
	routes, err := opencodeapi.Detect(strings.NewReader(""), "2.3")
	if err != nil {
		t.Fatal(err)
	}
	api := openCodeWorkerAPI(context.Background(), base, password)
	if err := ensureOpenCodeRolePermissions(api, routes, created.Data.ID, cwd, old); err != nil {
		t.Fatalf("adopted role permissions: %v", err)
	}
	before := provider.count()
	if _, status, err := probeAPI(base, password, http.MethodPost, "/api/session/"+created.Data.ID+"/prompt", map[string]string{"text": text, "delivery": "queue"}); err != nil || status/100 != 2 {
		t.Fatalf("adopted prompt = %d %v", status, err)
	}
	eventually(t, "adopted role reads a protected file without a prompt", func() bool { return provider.count() > before })
	t.Log("adopted role: permission rule stored, protected file read returned")
}
