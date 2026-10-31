package setup

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/daemon"
	"github.com/niparis/agent-net/internal/loginpath"
	"github.com/niparis/agent-net/internal/quotaaxi"
)

// These are variables so tests can run setup without npm or the network.
var (
	ensureQuotaAXI      = quotaaxi.Ensure
	ensureQuotaAXIAsync = func(logf func(string, ...any)) {
		go func() {
			nc, _, _ := daemon.LoadNode(daemon.ConfigDir())
			if err := quotaaxi.EnsureChoice(context.Background(), daemon.ConfigDir(), nc.QuotaAXI, logf); err != nil && logf != nil {
				logf("quota-axi: %v", err)
			}
		}()
	}
	removeQuotaAXI = quotaaxi.Remove
)

const quotaStepName = "quota-axi"

// quotaAXI installs the private copy when the owner allowed quotas, or removes
// it when they turned the item off. Missing Node is reported in one line and
// never fails setup.
func (r *runner) quotaAXI(ctx context.Context) {
	if r.want == nil || !r.want.QuotaAXI {
		removed, err := removeQuotaAXI()
		switch {
		case err != nil:
			r.step(quotaStepName, "", err)
		case removed:
			r.step(quotaStepName, "off (removed the private copy)", nil)
		default:
			r.step(quotaStepName, "off", nil)
		}
		return
	}
	nc, _, _ := daemon.LoadNode(daemon.ConfigDir())
	r.offerQuotaUpgrade(ctx, quotaaxi.Discover(nc.QuotaAXI))
	choice, err := quotaaxi.DiscoverChoice(daemon.ConfigDir(), nc.QuotaAXI)
	if err != nil {
		r.step(quotaStepName, "", err)
		return
	}
	if choice.Path != "" {
		if choice.Private {
			r.step(quotaStepName, "ok (already installed)", nil)
		} else {
			r.step(quotaStepName, "ok (using the installed quota-axi)", nil)
		}
		r.quotaAuth(ctx, choice)
		return
	}
	if err := ensureQuotaAXI(ctx, nil); err != nil {
		if errors.Is(err, quotaaxi.ErrNeedNode) {
			r.step(quotaStepName, quotaaxi.NeedNodeMessage, nil)
			return
		}
		r.step(quotaStepName, "", err)
		return
	}
	choice, err = quotaaxi.DiscoverChoice(daemon.ConfigDir(), nc.QuotaAXI)
	if err != nil {
		r.step(quotaStepName, "", err)
		return
	}
	if choice.Path == "" {
		r.step(quotaStepName, "skipped (install failed earlier today, trying again tomorrow)", nil)
		return
	}
	r.step(quotaStepName, "ok (installed "+quotaaxi.Version+")", nil)
	r.quotaAuth(ctx, choice)
}

// quotaUpgradeCommand chooses the installer from the executable or its target.
func quotaUpgradeCommand(path string) []string {
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		target = path
	}
	if strings.Contains(target, "/Cellar/quota-axi/") || strings.HasPrefix(path, "/opt/homebrew/bin/") {
		return []string{"brew", "upgrade", "quota-axi"}
	}
	if strings.Contains(target, "/node_modules/quota-axi/") || strings.Contains(path, "/.nvm/versions/node/") {
		return []string{"npm", "i", "-g", "quota-axi"}
	}
	return []string{"quota-axi", "update"}
}

// offerQuotaUpgrade asks before touching an older owner installation.
func (r *runner) offerQuotaUpgrade(ctx context.Context, copies []quotaaxi.Copy) {
	for _, c := range copies {
		if !c.Private && c.Runs && quotaaxi.Supported(c.Version) {
			return
		}
	}
	for _, c := range copies {
		if c.Private || !c.Runs || quotaaxi.Supported(c.Version) {
			continue
		}
		args := quotaUpgradeCommand(c.Path)
		tty, err := openTTY()
		if err != nil {
			r.hint("%s is too old; upgrade with %s", c.Path, strings.Join(args, " "))
			return
		}
		defer tty.Close()
		fmt.Fprintf(r.Out, "quota-axi %s at %s is too old. Run %s? [y/N] ", c.Version, c.Path, strings.Join(args, " "))
		line, _ := bufio.NewReader(tty).ReadString('\n')
		if answer := strings.ToLower(strings.TrimSpace(line)); answer != "y" && answer != "yes" {
			return
		}
		executable := args[0]
		if executable == "quota-axi" {
			executable = c.Path
		} else {
			for _, dir := range append([]string{filepath.Dir(c.Path)}, filepath.SplitList(loginpath.Path())...) {
				p := filepath.Join(dir, executable)
				if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0111 != 0 {
					executable = p
					break
				}
			}
		}
		cmd := exec.CommandContext(ctx, executable, args[1:]...)
		cmd.Env = quotaaxi.ExecEnv(c.Path)
		cmd.Stdout = r.Out
		cmd.Stderr = r.Out
		if err := cmd.Run(); err != nil {
			r.hint("upgrade failed: %v", err)
		}
		return
	}
}

var quotaPlatform = runtime.GOOS

// quotaAuth inspects sources only while a person is present. It never stores
// source paths, errors from the tool, or credential values.
func (r *runner) quotaAuth(ctx context.Context, choice quotaaxi.Choice) {
	tty, err := openTTY()
	if err != nil {
		r.hint("to check provider logins later, run agent-net setup --quota")
		return
	}
	defer tty.Close()
	result := quotaaxi.AuthResult{Path: choice.Path, Version: choice.Version, CheckedAt: time.Now().UTC()}
	run := func(prompt bool) error {
		args := []string{"auth", "--json"}
		if prompt {
			args = append([]string{"--allow-keychain-prompt"}, args...)
		}
		runCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()
		cmd := exec.CommandContext(runCtx, choice.Path, args...)
		env, err := quotaaxi.NodeExecEnv(choice.Path)
		if err != nil {
			return err
		}
		cmd.Env = env
		raw, err := cmd.Output()
		if err != nil {
			return err
		}
		var report struct {
			Auth []struct {
				Provider string `json:"provider"`
				Sources  []struct {
					Status string `json:"status"`
				} `json:"sources"`
			} `json:"auth"`
		}
		if err = json.Unmarshal(raw, &report); err != nil {
			return err
		}
		if report.Auth == nil {
			return errors.New("missing auth report")
		}
		result.Providers = nil
		for _, p := range report.Auth {
			if p.Provider == "" {
				continue
			}
			status := "not found"
			for _, source := range p.Sources {
				if source.Status == "available" {
					status = "found"
					break
				}
				if source.Status != "missing" {
					status = "needs attention"
				}
			}
			result.Providers = append(result.Providers, quotaaxi.AuthProvider{Provider: p.Provider, Status: status})
		}
		return nil
	}
	if err := run(false); err != nil {
		result.Error = "auth check failed"
		r.hint("could not check provider logins; run agent-net setup --quota")
	} else {
		for _, p := range result.Providers {
			r.hint("%s: %s", p.Provider, p.Status)
		}
	}
	if quotaPlatform == "darwin" {
		fmt.Fprint(r.Out, "Allow a Keychain prompt now? Choose Always Allow in the dialog. [y/N] ")
		line, _ := bufio.NewReader(tty).ReadString('\n')
		if answer := strings.ToLower(strings.TrimSpace(line)); answer == "y" || answer == "yes" {
			result.KeychainPrompt = true
			result.Error = ""
			if err := run(true); err != nil {
				result.Error = "auth check failed"
				r.hint("could not check provider logins; run agent-net setup --quota")
			} else {
				for _, p := range result.Providers {
					r.hint("%s: %s", p.Provider, p.Status)
				}
			}
		}
	}
	if err := quotaaxi.SaveAuth(daemon.ConfigDir(), result); err != nil {
		r.step("quota auth", "", err)
	}
}
