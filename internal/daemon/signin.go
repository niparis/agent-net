package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Sign-in asks (decision 0132). When quota-axi reports that a CLI needs the
// owner's interactive login, this machine raises one sign_in ask. The ask
// names a provider key only; the command comes from the allowlist in
// protocol/signin.go. Only the owner's click opens a pane, and the pane runs
// on the machine the ask names.

const (
	// signInWatchEvery is how often the open pane is checked. A login can take
	// minutes, so the check is cheap and passive.
	signInWatchEvery = time.Second
	// signInWatchFor bounds one watch. A forgotten login pane stops the watch.
	signInWatchFor = 30 * time.Minute
)

type signInState struct {
	mu sync.Mutex
	// raised maps a provider key to the open ask ID. It stays until the
	// provider's quota error clears, so a dismissed ask is not raised again
	// on every quota run.
	raised map[string]string
}

func (s *signInState) ids() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.raised))
	for k, v := range s.raised {
		out[k] = v
	}
	return out
}

func (s *signInState) add(provider, ask string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.raised == nil {
		s.raised = map[string]string{}
	}
	s.raised[provider] = ask
}

func (s *signInState) drop(provider string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.raised, provider)
}

// signInNeeded lists the allowlisted providers whose last quota reading asks
// for a login. The provider id from quota-axi may carry a suffix; only the
// allowlist key is returned.
func signInNeeded(q *protocol.Quotas) []string {
	if q == nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, p := range q.Providers {
		key, ok := protocol.SignInProviderKey(p.Provider)
		if !ok || seen[key] || !protocol.SignInNeeded(p.Provider, p.Error) {
			continue
		}
		seen[key] = true
		out = append(out, key)
	}
	return out
}

// syncSignInAsks raises one ask per provider that needs a login, and closes
// the ask when the error is gone. A repeated quota reading is cheap: it makes
// no gateway call for a provider already raised.
func (d *Daemon) syncSignInAsks(ctx context.Context, q *protocol.Quotas) {
	needed := signInNeeded(q)
	if q == nil {
		return
	}
	have := d.signIn.ids()
	for _, provider := range needed {
		if _, ok := have[provider]; ok {
			continue
		}
		info, ok := protocol.SignInProvider(provider)
		if !ok {
			continue
		}
		var data json.RawMessage
		var err error
		data, err = d.call(ctx, "sign_in.raise", protocol.SignInRaise{Provider: info.Provider, Reason: info.Reason})
		if err != nil {
			continue
		}
		var res struct {
			Ask protocol.Ask `json:"ask"`
		}
		if json.Unmarshal(data, &res) != nil || res.Ask.ID == "" {
			// The ask was opened (the ref dedupes it); remember the provider so
			// the next reading does not ask again. Without an ID it cannot be
			// closed from here, which is the safe direction.
			d.signIn.add(provider, "")
			continue
		}
		d.signIn.add(provider, res.Ask.ID)
	}
	for provider := range have {
		still := false
		for _, p := range needed {
			if p == provider {
				still = true
				break
			}
		}
		if still {
			continue
		}
		if _, err := d.call(ctx, "sign_in.close", protocol.SignInClose{Provider: provider, Reason: protocol.AskReasonSignedIn}); err == nil {
			d.signIn.drop(provider)
		}
	}
}

// signInOpen answers the owner's click. It opens the login on the machine the
// ask names and returns the plan that shows it here. A machine without herdr
// answers with a note; there is no ssh fallback.
func (d *Daemon) signInOpen(ctx context.Context, q protocol.SignInOpenRequest) (protocol.EnterPlan, error) {
	if strings.TrimSpace(q.AskID) == "" {
		return protocol.EnterPlan{}, errors.New("sign_in_open needs an ask")
	}
	raw, err := d.call(ctx, "query.sign_in", protocol.SignInQuery{AskID: q.AskID})
	if err != nil {
		return protocol.EnterPlan{}, err
	}
	var target protocol.SignInTarget
	if err := json.Unmarshal(raw, &target); err != nil {
		return protocol.EnterPlan{}, errors.New("bad sign-in answer")
	}
	info, ok := protocol.SignInProvider(target.Provider)
	if !ok {
		return protocol.EnterPlan{}, errors.New("unknown sign-in provider")
	}
	if target.NodeID == "" || target.NodeID == d.NodeID() {
		return d.openSignInPane(info, q.HerdrSocket, q.HerdrPaneID)
	}
	raw, err = d.call(ctx, "query.sign_in_prepare", protocol.SignInQuery{AskID: q.AskID, Owner: d.ownerSessionID()})
	if err != nil {
		return protocol.EnterPlan{}, err
	}
	var res protocol.SignInPrepareResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return protocol.EnterPlan{}, errors.New("bad sign-in prepare answer")
	}
	host := firstNonEmpty(cleanLine(res.Host), cleanLine(target.Hostname))
	if !res.Opened {
		return protocol.EnterPlan{Host: host, Note: firstNonEmpty(cleanLine(res.Note), "Could not open the sign-in pane.")}, nil
	}
	if host == "" {
		return protocol.EnterPlan{Note: "The sign-in pane opened, but the machine did not say how to reach it."}, nil
	}
	bin := d.herdrBinary("")
	if bin == "" {
		return protocol.EnterPlan{Host: host, Note: fmt.Sprintf("The sign-in pane is open on %s in pane %s, but herdr is not installed here. Install it from https://herdr.dev/tui/docs/install/ to see it.", host, cleanLine(res.Pane))}, nil
	}
	targetHost := host
	if t := d.cfg.EnterSSHHosts[host]; t != "" {
		targetHost = t
	}
	if !sshTargetOK(targetHost) {
		return protocol.EnterPlan{Host: host, Note: fmt.Sprintf("The sign-in pane is open on %s in pane %s, but enter_ssh_hosts gives a target herdr --remote cannot use.", host, cleanLine(res.Pane))}, nil
	}
	return protocol.EnterPlan{
		Argv: []string{bin, "--remote", targetHost}, Host: host,
		Note: fmt.Sprintf("Opens herdr on %s; the sign-in is pane %s. Detach with ctrl+b then q (herdr's prefix + q) to come back here.", host, cleanLine(res.Pane)),
	}, nil
}

// openSignInPane opens the allowlisted login command in this machine's herdr.
// It runs the argv from the table, never a value from an ask or an agent.
func (d *Daemon) openSignInPane(info protocol.SignInProviderInfo, socket, callerPane string) (protocol.EnterPlan, error) {
	plan := protocol.EnterPlan{Argv: append([]string(nil), info.Argv...), Host: d.hostName()}
	opened := d.openInHerdr("signin:"+info.Provider, "sign in: "+info.Label, socket, callerPane, plan)
	if !opened.OpenedHerdr {
		return protocol.EnterPlan{Host: d.hostName(), Note: firstNonEmpty(opened.Note, herdrInstallNote)}, nil
	}
	bin := d.herdrBinary("")
	if bin != "" {
		go d.watchSignInPane(info.Provider, bin, socket, opened.HerdrPane)
	}
	return opened, nil
}

// signInPrepare opens the login pane on this machine for the gateway. The
// frame carries a provider key only, so no caller can choose the command.
func (d *Daemon) signInPrepare(raw []byte) (protocol.SignInPrepareResult, error) {
	var p protocol.SignInPrepare
	if json.Unmarshal(raw, &p) != nil || p.AskID == "" {
		return protocol.SignInPrepareResult{}, errors.New("bad sign-in prepare")
	}
	info, ok := protocol.SignInProvider(p.Provider)
	if !ok {
		return protocol.SignInPrepareResult{}, errors.New("unknown sign-in provider")
	}
	plan, err := d.openSignInPane(info, "", "")
	if err != nil {
		return protocol.SignInPrepareResult{Opened: false, Host: d.hostName(), Note: err.Error()}, nil
	}
	return protocol.SignInPrepareResult{Opened: plan.OpenedHerdr, Host: d.hostName(), Pane: plan.HerdrPane, Note: plan.Note}, nil
}

// watchSignInPane waits until the owner closes the login pane, then re-runs
// that provider's quota check once. A good reading closes the ask as signed in
// through syncSignInAsks.
func (d *Daemon) watchSignInPane(provider, bin, socket, pane string) {
	ctx, cancel := context.WithTimeout(context.Background(), signInWatchFor)
	defer cancel()
	ticker := time.NewTicker(signInWatchEvery)
	defer ticker.Stop()
	for {
		panes, err := d.herdrPanes(bin, socket)
		if err != nil {
			break
		}
		found := false
		for _, p := range panes {
			if p.PaneID == pane {
				found = true
				break
			}
		}
		if !found {
			break
		}
		if active, known := d.herdrPaneActive(bin, socket, pane); known && !active {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
	d.recheckSignIn(ctx, provider)
}

// recheckSignIn runs the quota reader once after a login pane closed. A good
// reading closes the ask with "signed in"; a persisting error keeps it open.
func (d *Daemon) recheckSignIn(ctx context.Context, provider string) {
	path := d.quota.pathText()
	if path == "" {
		return
	}
	rctx, cancel := context.WithTimeout(ctx, quotaTimeout)
	defer cancel()
	d.refreshQuota(rctx, path)
}
