package setup

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/agentfiles"
	"github.com/niparis/agent-net/internal/daemon"
	"github.com/niparis/agent-net/internal/integrations"
	"github.com/niparis/agent-net/internal/protocol"
)

// ApplyIntegrations makes this machine follow the network answers. The local
// answers of the machine win. It does nothing when this machine already applied
// the same answers. The daemon calls it at start and when the answers change.
func ApplyIntegrations(net protocol.Integrations, logf func(format string, args ...any)) error {
	local := integrations.LoadLocal(daemon.ConfigDir())
	want := integrations.Effective(net, local)
	if applied, ok := integrations.LoadApplied(daemon.ConfigDir()); ok && applied == want {
		return nil
	}
	r, err := newRunner(Options{Out: logWriter(logf)})
	if err != nil {
		return err
	}
	r.want = &want
	r.claudePeerMessages()
	r.codexMCP()
	r.claudeHintHook()
	r.claudePolicyHook()
	r.claudeNeedsHook()
	r.claudeWorkersMod()
	if r.failed == nil {
		integrations.SaveApplied(daemon.ConfigDir(), want)
	}
	return r.failed
}

// recordApplied remembers the answers this run applied, so the daemon does not
// repeat the work at its next start.
func (r *runner) recordApplied() {
	if r.want != nil && r.failed == nil {
		integrations.SaveApplied(daemon.ConfigDir(), *r.want)
	}
}

// logWriter turns the setup step lines into daemon log lines.
type logWriter func(format string, args ...any)

func (w logWriter) Write(p []byte) (int, error) {
	if w != nil {
		w("%s", strings.TrimRight(string(p), "\n"))
	}
	return len(p), nil
}

// Values of Options.All.
const (
	AllYes = "yes" // --all
	AllNo  = "no"  // --none
)

const integrationsStepName = "integrations:"

// chooseIntegrations decides the seven integrations for this machine. The
// network answers win and are applied without a question. A machine without
// network answers asks one short question and stores the answer for every
// machine. A per-item flag is a local answer for this machine and wins over the
// network answer.
func (r *runner) chooseIntegrations(ctx context.Context) {
	net, hasNet := r.networkIntegrations(ctx)

	var base protocol.Integrations
	stored := false
	source := ""
	switch {
	case hasNet:
		base = net
		source = " (your network settings)"
	case r.All == AllYes:
		base = integrations.AllOn()
		stored = r.storeNetworkIntegrations(ctx, base)
		if stored {
			source = " (saved for your network)"
		}
	case r.All == AllNo:
		base = integrations.AllOff()
		stored = r.storeNetworkIntegrations(ctx, base)
		if stored {
			source = " (saved for your network)"
		}
	default:
		ans, ok := r.askIntegrations()
		if !ok {
			// No terminal and no answer for the network: keep this machine as
			// it is, so an upgrade does not turn off what an older setup
			// installed. A later setup with a terminal can ask the one
			// question.
			base = r.currentIntegrations()
		} else {
			base = ans
			stored = r.storeNetworkIntegrations(ctx, base)
			if stored {
				source = " (saved for your network)"
			}
		}
	}

	local := integrations.LoadLocal(daemon.ConfigDir())
	explicit := false
	for _, s := range integrations.Specs() {
		v := r.itemFlag(s.ID)
		if v == "" {
			continue
		}
		on := v == itemYes
		base.Set(s.ID, on)
		local.Set(s.ID, on)
		explicit = true
	}
	if explicit {
		if err := integrations.SaveLocal(daemon.ConfigDir(), local); err != nil {
			r.hint("cannot record the local integration answers: %v", err)
		}
	}
	want := integrations.Effective(base, local)
	r.want = &want

	r.step(integrationsStepName, fmt.Sprintf("%d of %d on%s", want.CountOn(), len(integrations.Specs()), source), nil)
}

const (
	itemYes = "yes"
	itemNo  = "no"
)

// Values of Options.QuotaAXI.
const (
	QuotaAXIYes = "yes" // --quota-axi
	QuotaAXINo  = "no"  // --no-quota-axi
)

// currentIntegrations reads what this machine has now. It lets a setup run
// without a terminal and without network answers keep the integrations that an
// older setup installed, instead of turning them off.
func (r *runner) currentIntegrations() protocol.Integrations {
	var in protocol.Integrations
	if claudePath() != "" {
		if doc, err := readSettings(claudeSettingsPath()); err == nil {
			if v, ok := doc.value(); ok && v == "accept" {
				in.PeerMessages = true
			}
		}
		in.HintHook = loadHintState().Installed
		in.PolicyHook = loadPolicyState().Installed
		in.NeedsHook = loadNeedsState().Installed
		in.WorkersMod = loadWorkersModState().Installed || exists(agentfiles.ClaudeWorkersDir())
	}
	if codexPath() != "" || exists(agentfiles.CodexHome()) {
		if b, err := os.ReadFile(agentfiles.CodexConfigPath()); err == nil {
			if s, e, ok := findCodexBlock(string(b)); ok && strings.Contains(string(b[s:e]), "default_tools_approval_mode") {
				in.CodexTools = true
			}
		}
	}
	if _, ok := daemon.FindQuotaAXI(""); ok {
		in.QuotaAXI = true
	}
	return in
}

// itemFlag returns the per-item flag answer for an integration, or "".
func (r *runner) itemFlag(id string) string {
	var yes, no bool
	switch id {
	case integrations.PeerMessages:
		yes, no = r.PeerMessages == PeerMessagesAccept, r.PeerMessages == PeerMessagesNo
	case integrations.CodexTools:
		yes, no = r.CodexTools == CodexToolsApprove, r.CodexTools == CodexToolsNo
	case integrations.HintHook:
		yes, no = r.HintHook == HintHookYes, r.HintHook == HintHookNo
	case integrations.PolicyHook:
		yes, no = r.PolicyHook == PolicyHookYes, r.PolicyHook == PolicyHookNo
	case integrations.NeedsHook:
		yes, no = r.NeedsHook == NeedsHookYes, r.NeedsHook == NeedsHookNo
	case integrations.WorkersMod:
		yes, no = r.WorkersMod == WorkersModYes, r.WorkersMod == WorkersModNo
	case integrations.QuotaAXI:
		yes, no = r.QuotaAXI == QuotaAXIYes, r.QuotaAXI == QuotaAXINo
	}
	switch {
	case yes:
		return itemYes
	case no:
		return itemNo
	}
	return ""
}

// askIntegrations prints the one question. ok is false when there is no
// terminal.
func (r *runner) askIntegrations() (protocol.Integrations, bool) {
	tty, err := openTTY()
	if err != nil {
		return protocol.Integrations{}, false
	}
	defer tty.Close()
	rd := bufio.NewReader(tty)
	fmt.Fprintln(r.Out, "Connect your agents fully? Recommended:")
	for _, s := range integrations.Specs() {
		fmt.Fprintln(r.Out, "  - "+s.Label)
	}
	fmt.Fprintln(r.Out, "Details: docs/setup.md#integrations")
	for {
		fmt.Fprint(r.Out, "Turn all on? [Y/n/c] ")
		line, _ := rd.ReadString('\n')
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "", "y", "yes":
			return integrations.AllOn(), true
		case "n", "no":
			return integrations.AllOff(), true
		case "c", "custom":
			var a protocol.Integrations
			for _, s := range integrations.Specs() {
				fmt.Fprintf(r.Out, "%s? [Y/n] ", s.Label)
				l, _ := rd.ReadString('\n')
				v := strings.ToLower(strings.TrimSpace(l))
				a.Set(s.ID, v != "n" && v != "no")
			}
			return a, true
		default:
			fmt.Fprint(r.Out, "Please answer y, n or c. ")
		}
	}
}

// networkIntegrations reads the owner's answers from the gateway. It returns
// false when the network has none, or when the gateway cannot be reached.
func (r *runner) networkIntegrations(ctx context.Context) (protocol.Integrations, bool) {
	c, ok := r.integrationsClient()
	if !ok {
		return protocol.Integrations{}, false
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	res, err := c.Get(ctx)
	if err != nil || !res.Set {
		return protocol.Integrations{}, false
	}
	return res.Integrations, true
}

// storeNetworkIntegrations stores an answer for every machine.
func (r *runner) storeNetworkIntegrations(ctx context.Context, a protocol.Integrations) bool {
	c, ok := r.integrationsClient()
	if !ok {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := c.Set(ctx, a); err != nil {
		r.hint("could not save these answers for your network: %v", err)
		return false
	}
	return true
}

func (r *runner) integrationsClient() (integrations.Client, bool) {
	nc, ca, err := daemon.LoadNode(daemon.ConfigDir())
	if err != nil {
		return integrations.Client{}, false
	}
	hc, err := daemon.PinnedClient(ca)
	if err != nil {
		return integrations.Client{}, false
	}
	return integrations.Client{URL: nc.GatewayURL, Token: nc.Token, HTTP: hc}, true
}
