package daemon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// The sidebar of agent-net's herdr session. herdr draws a workspace and tab
// sidebar that repeats the session list agent-net already shows. herdr reads
// its config from HERDR_CONFIG_PATH, so agent-net gives its own server a copy
// of the owner's config with the sidebar collapsed. The owner's config and
// his own servers are never changed.

// herdrSidebarSetting is the herdr [ui] key that hides the sidebar. herdr
// versions without it are started as they are.
const herdrSidebarSetting = "sidebar_collapsed_mode"

// herdrOwnConfigPath is the config.toml that herdr reads for the owner's own
// servers, following herdr's own rules: HERDR_CONFIG_PATH, then the XDG config
// folder.
func herdrOwnConfigPath() (string, error) {
	if p := os.Getenv("HERDR_CONFIG_PATH"); p != "" {
		return p, nil
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(xdg) {
		return filepath.Join(xdg, "herdr", "config.toml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "herdr", "config.toml"), nil
}

// agentNetHerdrConfig writes agent-net's own herdr config into the state
// folder and returns its path. It is the owner's config, copied as it is
// (keybindings and the rest) with the sidebar settings set in [ui]. A missing
// owner config gives a file with only those settings. The copy is rewritten
// each time agent-net starts its herdr server, so a later change to the
// owner's config reaches the next start.
func agentNetHerdrConfig(ownerPath string) (string, error) {
	raw, err := os.ReadFile(ownerPath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	cfg := map[string]any{}
	if len(raw) > 0 {
		if err := toml.Unmarshal(raw, &cfg); err != nil {
			return "", fmt.Errorf("herdr config %s does not parse: %w", ownerPath, err)
		}
	}
	ui := map[string]any{}
	if v, present := cfg["ui"]; present {
		table, ok := v.(map[string]any)
		if !ok {
			return "", fmt.Errorf("herdr config %s has a ui setting that is not a table", ownerPath)
		}
		ui = table
	}
	ui["sidebar_start_collapsed"] = true
	ui[herdrSidebarSetting] = "hidden"
	cfg["ui"] = ui
	body, err := toml.Marshal(cfg)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(StateDir(), "herdr")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "agent-net-config.toml")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return path, nil
}

// herdrHasSidebarSetting says whether bin is a herdr that knows the sidebar
// setting. `herdr --default-config` prints herdr's defaults and reaches no
// server.
func herdrHasSidebarSetting(bin string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--default-config")
	cmd.Env = herdrCleanEnv()
	out, err := cmd.Output()
	return err == nil && bytes.Contains(out, []byte(herdrSidebarSetting))
}

// agentNetHerdrServerEnv is the environment of agent-net's herdr server. It
// points HERDR_CONFIG_PATH at agent-net's copy of the config when this herdr
// knows the sidebar setting. Otherwise the server starts as herdr starts it,
// and the daemon log says why.
func (d *Daemon) agentNetHerdrServerEnv(bin string) []string {
	env := herdrCleanEnv()
	var reason string
	if !herdrHasSidebarSetting(bin) {
		reason = fmt.Sprintf("herdr at %s has no %s setting", bin, herdrSidebarSetting)
	} else if owner, err := herdrOwnConfigPath(); err != nil {
		reason = err.Error()
	} else if path, err := agentNetHerdrConfig(owner); err != nil {
		reason = err.Error()
	} else {
		return append(env, "HERDR_CONFIG_PATH="+path)
	}
	if d.log != nil {
		d.Logf("agent-net's herdr server opens with its sidebar shown: %s", reason)
	}
	return env
}
