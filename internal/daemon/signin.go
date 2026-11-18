package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/quotaaxi"
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
	raised  map[string]string
	errors  map[string]string
	pending map[string]*signInPending
}

type signInPending struct {
	provider, tempDir string
	timer             *time.Timer
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

func (s *signInState) lastError(provider string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.errors[provider]
}

func (s *signInState) setLastError(provider, errText string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.errors == nil {
		s.errors = map[string]string{}
	}
	s.errors[provider] = errText
}

func (s *signInState) drop(provider string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.raised, provider)
	delete(s.errors, provider)
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

// signInProviderRow is the reading of one sign-in provider in a quota result.
func signInProviderRow(q *protocol.Quotas, provider string) (protocol.QuotaProvider, bool) {
	for _, p := range q.Providers {
		if key, ok := protocol.SignInProviderKey(p.Provider); ok && key == provider {
			return p, true
		}
	}
	return protocol.QuotaProvider{}, false
}

// signInDetail is the detail of a sign-in ask: the provider's fixed reason,
// then the error of the last reading when there is one. The reason itself never
// changes.
func signInDetail(reason, readErr string) string {
	if readErr == "" {
		return reason
	}
	if r := []rune(readErr); len(r) > 300 {
		readErr = string(r[:300]) + "..."
	}
	return reason + "\nLast reading: " + readErr
}

// syncSignInAsks raises one ask per provider that needs a login. It closes the
// ask only when the provider reads with windows and no error: a missing row or
// another error leaves it open, and a changed error updates its detail. A
// repeated quota reading is cheap: it makes no gateway call for a provider
// already raised with the same error.
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
		row, _ := signInProviderRow(q, provider)
		data, err := d.call(ctx, "sign_in.raise", protocol.SignInRaise{Provider: info.Provider, Reason: info.Reason})
		if err != nil {
			continue
		}
		var res struct {
			Ask protocol.Ask `json:"ask"`
		}
		// The ask was opened (the ref dedupes it); remember the provider so
		// the next reading does not ask again. Without an ID it cannot be
		// closed from here, which is the safe direction.
		d.signIn.add(provider, "")
		if json.Unmarshal(data, &res) == nil && res.Ask.ID != "" {
			d.signIn.add(provider, res.Ask.ID)
		}
		d.signIn.setLastError(provider, row.Error)
	}
	for provider := range have {
		row, ok := signInProviderRow(q, provider)
		if !ok {
			continue
		}
		if row.Error != "" {
			if row.Error == d.signIn.lastError(provider) {
				continue
			}
			info, _ := protocol.SignInProvider(provider)
			if _, err := d.call(ctx, "sign_in.raise", protocol.SignInRaise{Provider: info.Provider, Reason: signInDetail(info.Reason, row.Error)}); err == nil {
				d.signIn.setLastError(provider, row.Error)
			}
			continue
		}
		if len(row.Windows) == 0 {
			continue
		}
		if _, err := d.call(ctx, "sign_in.close", protocol.SignInClose{Provider: provider, Reason: protocol.AskReasonSignedIn}); err == nil {
			d.signIn.drop(provider)
		}
	}
}

// signInOpen runs the listener on the machine where the owner opened the TUI.
func (d *Daemon) signInOpen(ctx context.Context, q protocol.SignInOpenRequest) (protocol.EnterPlan, error) {
	if strings.TrimSpace(q.AskID) == "" {
		return protocol.EnterPlan{}, errors.New("sign_in_open needs an ask")
	}
	if viaFrom(ctx) == protocol.ViaPhone {
		request := protocol.UIRequest{SignInAskID: q.AskID}
		if !d.showLocalOwnerEnter(request) {
			_, err := d.call(ctx, "owner.ui.request", protocol.OwnerUIRelay{Owner: d.ownerSessionID(), Request: request})
			if err != nil {
				return protocol.EnterPlan{}, err
			}
		}
		return protocol.EnterPlan{OpenedHerdr: true, Note: "Sign-in opened in the owner's TUI."}, nil
	}
	raw, err := d.call(ctx, "query.sign_in", protocol.SignInQuery{AskID: q.AskID})
	if err != nil {
		return protocol.EnterPlan{}, err
	}
	var target protocol.SignInTarget
	if json.Unmarshal(raw, &target) != nil {
		return protocol.EnterPlan{}, errors.New("bad sign-in answer")
	}
	info, ok := protocol.SignInProvider(target.Provider)
	if !ok {
		return protocol.EnterPlan{}, errors.New("unknown sign-in provider")
	}

	pending := &signInPending{provider: info.Provider}
	plan := protocol.EnterPlan{Argv: append([]string(nil), info.Argv...), Host: d.hostName(), SignInAskID: q.AskID}
	env := d.signInEnv()
	plan.Env = map[string]string{"HOME": envValue(env, "HOME"), "BAILIAN_CONFIG_DIR": envValue(env, "BAILIAN_CONFIG_DIR"), "PATH": envValue(env, "PATH")}
	// The TUI resolves an executable before it applies Env. Pick the same bl
	// that quota-axi finds in its PATH when the reader is configured.
	if d.quota.pathText() != "" {
		bin, err := signInBinary(env)
		if err != nil {
			return protocol.EnterPlan{}, err
		}
		plan.Argv[0] = bin
	}
	if target.NodeID != "" && target.NodeID != d.NodeID() {
		pending.tempDir, err = os.MkdirTemp("", "agent-net-signin-*")
		if err != nil {
			return protocol.EnterPlan{}, errors.New("could not prepare sign-in")
		}
		// bl requests creation of a model API key when its config has none.
		// This flow needs only a console token, so the private config marks
		// an unused API key as present and is removed after the attempt.
		if err := os.WriteFile(filepath.Join(pending.tempDir, "config.json"), []byte(`{"api_key":"unused"}`), 0600); err != nil {
			_ = os.RemoveAll(pending.tempDir)
			return protocol.EnterPlan{}, errors.New("could not prepare sign-in")
		}
		plan.Env["BAILIAN_CONFIG_DIR"] = pending.tempDir
	}
	d.signIn.put(q.AskID, pending)
	return d.viewPlan("signin:"+info.Provider, "sign in: "+info.Label+" - Firefox, not Safari", false, plan), nil
}

// Only a completed, owner-opened attempt may read the freshly written token.
func (s *signInState) put(id string, p *signInPending) {
	s.mu.Lock()
	if s.pending == nil {
		s.pending = map[string]*signInPending{}
	}
	old := s.pending[id]
	s.pending[id] = p
	p.timer = time.AfterFunc(signInWatchFor, func() {
		s.mu.Lock()
		if s.pending[id] != p {
			s.mu.Unlock()
			return
		}
		delete(s.pending, id)
		s.mu.Unlock()
		if p.tempDir != "" {
			_ = os.RemoveAll(p.tempDir)
		}
	})
	s.mu.Unlock()
	if old != nil {
		old.timer.Stop()
		if old.tempDir != "" {
			_ = os.RemoveAll(old.tempDir)
		}
	}
}

func (s *signInState) take(id string) *signInPending {
	s.mu.Lock()
	p := s.pending[id]
	delete(s.pending, id)
	s.mu.Unlock()
	if p != nil {
		p.timer.Stop()
	}
	return p
}

func (d *Daemon) signInComplete(ctx context.Context, askID string, cancel bool) error {
	p := d.signIn.take(askID)
	if p == nil {
		return errors.New("sign-in is no longer pending")
	}
	if cancel {
		if p.tempDir != "" {
			_ = os.RemoveAll(p.tempDir)
		}
		return nil
	}
	if p.tempDir == "" {
		d.goBackground(func() { d.recheckSignIn(context.Background(), p.provider) })
		return nil
	}
	defer os.RemoveAll(p.tempDir)
	data, err := os.ReadFile(filepath.Join(p.tempDir, "config.json"))
	if err != nil {
		return errors.New("sign-in did not finish; open the ask to try again")
	}
	var credential struct {
		Token  string `json:"access_token"`
		Site   string `json:"console_site"`
		Region string `json:"console_region"`
	}
	if json.Unmarshal(data, &credential) != nil || credential.Token == "" ||
		(credential.Site != "" && credential.Site != "international") ||
		(credential.Region != "" && credential.Region != "ap-southeast-1") {
		return errors.New("sign-in did not return an international console token")
	}
	if err := d.importSignIn(ctx, credential.Token); err != nil {
		return err
	}
	_, err = d.call(ctx, "query.sign_in_transfer", protocol.SignInTransfer{AskID: askID, Owner: d.ownerSessionID(), Token: credential.Token})
	if err != nil {
		return errors.New("could not complete sign-in on the target machine")
	}
	d.goBackground(func() { d.recheckSignIn(context.Background(), p.provider) })
	return nil
}

// signInEnv uses the same HOME, config directory and executable search path as
// quota-axi. The browser login and token import must update the file it reads.
func (d *Daemon) signInEnv() []string {
	if path := d.quota.pathText(); path != "" {
		if env, err := quotaaxi.NodeExecEnv(path); err == nil {
			return env
		}
	}
	return os.Environ()
}

func envValue(env []string, name string) string {
	for _, item := range env {
		if value, ok := strings.CutPrefix(item, name+"="); ok {
			return value
		}
	}
	return ""
}

func signInBinary(env []string) (string, error) {
	for _, dir := range filepath.SplitList(envValue(env, "PATH")) {
		if dir == "" {
			continue
		}
		path := filepath.Join(dir, "bl")
		if st, err := os.Stat(path); err == nil && !st.IsDir() && st.Mode()&0111 != 0 {
			return path, nil
		}
	}
	return "", errors.New("bl was not found in the quota reader's PATH")
}

func (d *Daemon) importSignIn(ctx context.Context, token string) error {
	return importSignInWithEnv(ctx, token, d.signInEnv())
}

func importSignInWithEnv(ctx context.Context, token string, env []string) error {
	bin, err := signInBinary(env)
	if err != nil {
		return err
	}
	info, _ := protocol.SignInProvider("alibaba")
	cmd := exec.CommandContext(ctx, bin, info.ImportArgv[1:]...)
	cmd.Env = env
	cmd.Stdin = strings.NewReader(token)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if cmd.Run() != nil {
		return errors.New("bl could not import the console login")
	}
	return nil
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
	base := protocol.EnterPlan{Argv: append([]string(nil), info.Argv...), Host: d.hostName()}
	plan := d.viewPlan("signin:"+info.Provider, "sign in: "+info.Label+" - Firefox, not Safari", true, base)
	if plan.OpenedHerdr {
		d.watchSignIn(info.Provider, plan.HerdrPane)
	}
	return protocol.SignInPrepareResult{Opened: plan.OpenedHerdr, Host: d.hostName(), Pane: plan.HerdrPane, Session: plan.HerdrSession, Note: plan.Note}, nil
}

// watchSignIn starts the pane watch on the daemon's lifetime. A watch lives as
// long as its login pane, so Stop cancels it and waits for it through
// goBackground: no herdr call may outlive the daemon.
func (d *Daemon) watchSignIn(provider, pane string) {
	parent := d.ctoCtx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, signInWatchFor)
	bin := d.herdrBinary("")
	socket := d.hostedSocket("signin:" + provider)
	if !d.goBackground(func() {
		defer cancel()
		d.watchSignInPane(ctx, provider, bin, socket, pane)
	}) {
		cancel()
	}
}

// watchSignInPane waits until the owner closes the login pane, then re-runs
// that provider's quota check once. A good reading closes the ask as signed in
// through syncSignInAsks. The context ends the watch when the daemon stops.
func (d *Daemon) watchSignInPane(ctx context.Context, provider, bin, socket, pane string) {
	ticker := time.NewTicker(signInWatchEvery)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
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
