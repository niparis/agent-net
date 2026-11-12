package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

// The owner's herdr config has a sidebar width and a keybinding that must
// survive the copy agent-net makes for its own herdr server.
const ownerHerdrConfig = `onboarding = false

[keys]
toggle_sidebar = "prefix+b"

[ui]
sidebar_width = 36
`

// sidebarTestEnv points the daemon's state and herdr's owner config at
// temporary folders, so the test never reads or writes the real setup.
func sidebarTestEnv(t *testing.T, owner string) (state, ownerPath string) {
	t.Helper()
	root := t.TempDir()
	state = filepath.Join(root, "state")
	xdg := filepath.Join(root, "config")
	ownerPath = filepath.Join(xdg, "herdr", "config.toml")
	if err := os.MkdirAll(filepath.Dir(ownerPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ownerPath, []byte(owner), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("HERDR_CONFIG_PATH", "")
	t.Setenv("AGENT_NET_STATE_DIR", state)
	return state, ownerPath
}

// herdrEnvLines returns the config path each fake herdr call ran with, keyed
// by the call's arguments.
func herdrEnvLines(t *testing.T, log string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(fakeHerdrEnvLog(log))
	if err != nil {
		t.Fatal(err)
	}
	lines := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		args, config, _ := strings.Cut(line, "|")
		lines[args] = config
	}
	return lines
}

func TestAgentNetHerdrServerGetsCollapsedSidebarConfig(t *testing.T) {
	state, ownerPath := sidebarTestEnv(t, ownerHerdrConfig)
	bin, log := fakeOpenHerdr(t)

	if _, err := (&Daemon{}).agentNetHerdr(bin); err != nil {
		t.Fatal(err)
	}

	own := filepath.Join(state, "herdr", "agent-net-config.toml")
	env := herdrEnvLines(t, log)
	if got := env["--session agent-net server"]; got != own {
		t.Fatalf("agent-net server config path %q, want %q", got, own)
	}
	for args, config := range env {
		if args != "--session agent-net server" && config != "" {
			t.Errorf("herdr %q ran with HERDR_CONFIG_PATH=%q; only agent-net's server gets it", args, config)
		}
	}

	raw, err := os.ReadFile(own)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Keys struct {
			Toggle string `toml:"toggle_sidebar"`
		} `toml:"keys"`
		UI struct {
			Width     int    `toml:"sidebar_width"`
			Start     bool   `toml:"sidebar_start_collapsed"`
			Collapsed string `toml:"sidebar_collapsed_mode"`
		} `toml:"ui"`
	}
	if err := toml.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("agent-net config does not parse: %v\n%s", err, raw)
	}
	if !cfg.UI.Start || cfg.UI.Collapsed != "hidden" || cfg.UI.Width != 36 || cfg.Keys.Toggle != "prefix+b" {
		t.Fatalf("agent-net config %+v, want the owner's keys with the sidebar hidden\n%s", cfg, raw)
	}

	owner, err := os.ReadFile(ownerPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(owner) != ownerHerdrConfig {
		t.Fatalf("the owner's herdr config changed:\n%s", owner)
	}
}

func TestAgentNetHerdrServerStartsNormallyWithoutSidebarSetting(t *testing.T) {
	state, _ := sidebarTestEnv(t, ownerHerdrConfig)
	bin, log := fakeOpenHerdr(t)
	if err := os.WriteFile(filepath.Join(filepath.Dir(log), "default-config"), []byte("[ui]\nsidebar_width = 26\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	socket, err := (&Daemon{}).agentNetHerdr(bin)
	if err != nil {
		t.Fatalf("the server did not start: %v", err)
	}
	if socket != fakeOwnHerdrSocket(log) {
		t.Fatalf("socket %q", socket)
	}
	if got := herdrEnvLines(t, log)["--session agent-net server"]; got != "" {
		t.Fatalf("server ran with HERDR_CONFIG_PATH=%q although herdr has no %s setting", got, herdrSidebarSetting)
	}
	if _, err := os.Stat(filepath.Join(state, "herdr", "agent-net-config.toml")); !os.IsNotExist(err) {
		t.Fatalf("agent-net wrote a config for a herdr without the setting: %v", err)
	}
}

func TestAgentNetHerdrConfigAddsSidebarToOwnerConfig(t *testing.T) {
	cases := map[string]struct {
		owner             string
		bad               bool
		onboarding, width any
	}{
		"no ui table":      {owner: "onboarding = false\n", onboarding: false},
		"ui table present": {owner: ownerHerdrConfig, onboarding: false, width: int64(36)},
		"unparsable owner": {owner: "[ui\n", bad: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			state, ownerPath := sidebarTestEnv(t, tc.owner)
			path, err := agentNetHerdrConfig(ownerPath)
			if tc.bad {
				if err == nil {
					t.Fatalf("no error for an owner config that does not parse, got %s", path)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if path != filepath.Join(state, "herdr", "agent-net-config.toml") {
				t.Fatalf("path %q", path)
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var cfg map[string]any
			if err := toml.Unmarshal(raw, &cfg); err != nil {
				t.Fatal(err)
			}
			ui, _ := cfg["ui"].(map[string]any)
			if ui["sidebar_start_collapsed"] != true || ui["sidebar_collapsed_mode"] != "hidden" {
				t.Fatalf("ui %+v", ui)
			}
			if cfg["onboarding"] != tc.onboarding || ui["sidebar_width"] != tc.width {
				t.Fatalf("owner settings lost or changed: %+v", cfg)
			}
		})
	}
}
