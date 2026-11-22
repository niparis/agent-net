package setup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/daemon"
)

const (
	herdrStepName   = "herdr"
	herdrInstallURL = "https://herdr.dev/install.sh"
	herdrDocsURL    = "https://herdr.dev/tui/docs/install/"
)

// These are variables so tests can run setup without a network download.
var (
	findHerdr    = daemon.FindHerdr
	installHerdr = installHerdrOfficial
)

// SetHerdrForTesting replaces herdr's lookup and installer and returns a
// function that puts the real ones back. Tests outside this package that run
// Setup use it so they never download or install herdr.
func SetHerdrForTesting(find func(string) (string, error), install func(context.Context) error) func() {
	oldFind, oldInstall := findHerdr, installHerdr
	findHerdr, installHerdr = find, install
	return func() {
		findHerdr, installHerdr = oldFind, oldInstall
	}
}

// herdr makes sure the owner's herdr is available. Herdr is how the owner opens
// sessions (decision 0046) and Pi sessions need it, so a missing one is
// installed with herdr's official installer. A failed install is reported and
// setup carries on: the rest of setup does not depend on herdr.
func (r *runner) herdr(ctx context.Context) {
	if r.NoHerdr {
		r.step(herdrStepName, "skipped (--no-herdr)", nil)
		return
	}
	nc, _, _ := daemon.LoadNode(daemon.ConfigDir())
	configured := nc.HarnessPaths["herdr"]
	if bin, err := findHerdr(configured); err == nil {
		r.step(herdrStepName, "ok (found "+displayPath(bin)+")", nil)
		return
	}
	if err := installHerdr(ctx); err != nil {
		r.herdrFailed(err)
		return
	}
	bin, err := findHerdr(configured)
	if err != nil {
		r.herdrFailed(errors.New("installed but not found"))
		return
	}
	version := herdrVersionOf(ctx, bin)
	if version == "" {
		version = "unknown"
	}
	r.step(herdrStepName, "installed ("+version+", "+displayPath(bin)+")", nil)
}

// herdrFailed prints a plain line, not a step failure, so setup keeps its
// summary and the other steps.
func (r *runner) herdrFailed(err error) {
	fmt.Fprintf(r.Out, "%-21s %s\n", herdrStepName, r.colors.attention("failed ("+err.Error()+"; install it from "+herdrDocsURL+")"))
}

// installHerdrOfficial runs herdr's documented Linux and macOS installer. It
// installs a user-level binary in ~/.local/bin and never uses sudo.
func installHerdrOfficial(ctx context.Context) error {
	curl, err := exec.LookPath("curl")
	if err != nil {
		return errors.New("curl is not installed")
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		return errors.New("sh is not installed")
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	var fetchErr bytes.Buffer
	fetch := exec.CommandContext(ctx, curl, "-fsSL", herdrInstallURL)
	fetch.Stderr = &fetchErr
	script, err := fetch.Output()
	if err != nil {
		return fmt.Errorf("download the installer: %s", lastLine(fetchErr.String(), err))
	}
	var out bytes.Buffer
	install := exec.CommandContext(ctx, sh)
	install.Stdin = bytes.NewReader(script)
	install.Stdout = &out
	install.Stderr = &out
	if err := install.Run(); err != nil {
		return fmt.Errorf("installer: %s", lastLine(out.String(), err))
	}
	return nil
}

var ansiSGR = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// lastLine returns the last non-empty line of a command's output, without
// color escapes. Herdr's installer decorates its output, and its reason is the
// last line.
func lastLine(out string, err error) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(ansiSGR.ReplaceAllString(lines[i], "")); line != "" {
			return line
		}
	}
	return err.Error()
}

// herdrVersionOf asks the binary for its version. Herdr prints "herdr 0.9.5";
// the line shows only the version.
func herdrVersionOf(ctx context.Context, path string) string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	fields := strings.Fields(line)
	if len(fields) > 0 && strings.EqualFold(fields[0], "herdr") {
		fields = fields[1:]
	}
	if len(fields) == 0 {
		return ""
	}
	return strings.TrimPrefix(fields[0], "v")
}
