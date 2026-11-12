package setup

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/agentfiles"
	"github.com/niparis/agent-net/internal/daemon"
)

// TestMain keeps even tests that forget a fixture away from the user's setup.
func TestMain(m *testing.M) {
	root, err := os.MkdirTemp("", "agent-net-setup-tests-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := func() int {
		defer os.RemoveAll(root)
		for _, item := range os.Environ() {
			key, _, _ := strings.Cut(item, "=")
			if strings.HasPrefix(key, "XDG_") || strings.HasPrefix(key, "AGENT_NET_") {
				if err := os.Unsetenv(key); err != nil {
					fmt.Fprintln(os.Stderr, err)
					return 1
				}
			}
		}
		// These harness overrides can also point outside HOME.
		for _, key := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "PI_CODING_AGENT_DIR", "PI_CODING_AGENT_SESSION_DIR", "OPENCODE_CONFIG", "OPENCODE_CONFIG_DIR", "OPENCODE_CONFIG_CONTENT", "OPENCODE_DB"} {
			if err := os.Unsetenv(key); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
		}
		// A real claude or codex on the developer's PATH makes every uninstall
		// test start it, and makes results differ between machines and CI.
		cleanPath, err := pathWithoutRealHarnesses(os.Getenv("PATH"), root)
		if err == nil {
			err = os.Setenv("PATH", cleanPath)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		env := map[string]string{
			"HOME":                          filepath.Join(root, "home"),
			"XDG_CONFIG_HOME":               filepath.Join(root, "xdg-config"),
			"XDG_DATA_HOME":                 filepath.Join(root, "xdg-data"),
			"XDG_STATE_HOME":                filepath.Join(root, "xdg-state"),
			"XDG_CACHE_HOME":                filepath.Join(root, "xdg-cache"),
			"XDG_RUNTIME_DIR":               filepath.Join(root, "xdg-runtime"),
			"AGENT_NET_CONFIG_DIR":          filepath.Join(root, "config"),
			"AGENT_NET_STATE_DIR":           filepath.Join(root, "state"),
			"AGENT_NET_SOCKET":              filepath.Join(root, "daemon.sock"),
			"AGENT_NET_GATEWAY_DATA":        filepath.Join(root, "gateway"),
			"AGENT_NET_CLAUDE_SESSIONS_DIR": filepath.Join(root, "claude-sessions"),
			"AGENT_NET_NO_SERVICE":          "1",
			"AGENT_NET_SETUP_TEST_ROOT":     root,
		}
		for key, value := range env {
			if err := os.Setenv(key, value); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
		}
		if err := os.MkdirAll(env["HOME"], 0o700); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return m.Run()
	}()
	os.Exit(code)
}

// realHarnessPrograms are the programs setup would start or look for. Tests
// that need one put a fake on PATH themselves.
var realHarnessPrograms = []string{"claude", "codex", "pi", "opencode", "agy", "gemini", "tailscale", "quota-axi"}

// pathWithoutRealHarnesses returns PATH with the real harnesses hidden. A
// folder that holds one is replaced by a folder of links to its other
// programs, so tools such as touch and rm stay reachable.
func pathWithoutRealHarnesses(path, scratch string) (string, error) {
	hidden := map[string]bool{}
	for _, name := range realHarnessPrograms {
		hidden[name] = true
	}
	var keep []string
	for i, dir := range filepath.SplitList(path) {
		found := false
		for name := range hidden {
			if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
				found = true
				break
			}
		}
		if !found {
			keep = append(keep, dir)
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		filtered := filepath.Join(scratch, fmt.Sprintf("path-%d", i))
		if err := os.MkdirAll(filtered, 0o700); err != nil {
			return "", err
		}
		for _, entry := range entries {
			if hidden[entry.Name()] {
				continue
			}
			if err := os.Symlink(filepath.Join(dir, entry.Name()), filepath.Join(filtered, entry.Name())); err != nil {
				return "", err
			}
		}
		keep = append(keep, filtered)
	}
	return strings.Join(keep, string(os.PathListSeparator)), nil
}

func TestSetupTestPathHidesRealHarnesses(t *testing.T) {
	for _, name := range realHarnessPrograms {
		if p, err := exec.LookPath(name); err == nil {
			t.Errorf("tests can reach a real %s at %s", name, p)
		}
	}
}

func TestSetupTestEnvironmentIsIsolated(t *testing.T) {
	root := os.Getenv("AGENT_NET_SETUP_TEST_ROOT")
	if root == "" {
		t.Fatal("setup tests have no isolated environment")
	}
	paths := map[string]string{
		"HOME":                          os.Getenv("HOME"),
		"XDG_CONFIG_HOME":               os.Getenv("XDG_CONFIG_HOME"),
		"XDG_DATA_HOME":                 os.Getenv("XDG_DATA_HOME"),
		"XDG_STATE_HOME":                os.Getenv("XDG_STATE_HOME"),
		"XDG_CACHE_HOME":                os.Getenv("XDG_CACHE_HOME"),
		"XDG_RUNTIME_DIR":               os.Getenv("XDG_RUNTIME_DIR"),
		"AGENT_NET_CONFIG_DIR":          daemon.ConfigDir(),
		"AGENT_NET_STATE_DIR":           daemon.StateDir(),
		"AGENT_NET_SOCKET":              daemon.SocketPath(),
		"AGENT_NET_GATEWAY_DATA":        (&runner{}).gatewayData(),
		"AGENT_NET_CLAUDE_SESSIONS_DIR": daemon.ClaudeSessionsDir(),
		"Pi sessions":                   daemon.PiSessionsDir(),
		"dist":                          DefaultDist(),
		"systemd":                       systemdDir(),
		"launch agents":                 launchAgentsDir(),
		"Claude settings":               claudeSettingsPath(),
		"Codex config":                  agentfiles.CodexConfigPath(),
		"OpenCode config":               agentfiles.OpencodeDir(),
	}
	for name, path := range paths {
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == ".." || filepath.IsAbs(rel) || len(rel) >= 3 && rel[:3] == ".."+string(filepath.Separator) {
			t.Errorf("%s path %q is outside test root %q", name, path, root)
		}
	}
}
