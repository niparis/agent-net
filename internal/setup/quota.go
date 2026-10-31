package setup

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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
		cmd.Stdin = tty
		cmd.Stdout = r.Out
		cmd.Stderr = r.Out
		if err := cmd.Run(); err != nil {
			r.hint("upgrade failed: %v", err)
		}
		return
	}
}
