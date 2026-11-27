package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// AgyStartEnv carries the ID of an agent-net start into the agy process its
// pane opened. The SessionStart hook of that process reports it, so the daemon
// can claim the conversation agy created for this start.
const AgyStartEnv = "AGENT_NET_START_ID"

// agyPaneCheckEvery is how often a start that waits for its conversation checks
// that its pane is still open. herdr closes the pane when agy exits, and then no
// conversation will come from it.
const agyPaneCheckEvery = 5 * time.Second

// agyWaitsForPromptNote is the note of a start without a first message. agy
// creates the conversation with its first prompt, so nothing shows until then.
const agyWaitsForPromptNote = "agy creates its conversation with its first prompt, so the session shows up once you send one. In a new folder agy first asks whether to trust the project; answer it in the pane."

var (
	// errAgyPaneClosed says the pane closed before its conversation started.
	errAgyPaneClosed = errors.New("the Antigravity pane closed before its conversation started")
	// errAgyPaneClosedBeforePrompt is the failure of a start that waited for its
	// first prompt and whose pane closed without one.
	errAgyPaneClosedBeforePrompt = errors.New("the Antigravity pane closed before its first prompt, so agy created no conversation; start it again to try once more")
)

// agyStartWait is a start whose conversation ID agy creates. The first
// SessionStart hook from the pane the daemon opened claims that conversation.
type agyStartWait struct {
	path   string
	native string
	// claimed is closed when the hook names the conversation.
	claimed chan struct{}
}

func (d *Daemon) registerAgyStart(id, path string) {
	if id == "" {
		return
	}
	d.mu.Lock()
	if d.agyStarts == nil {
		d.agyStarts = map[string]*agyStartWait{}
	}
	d.agyStarts[id] = &agyStartWait{path: path, claimed: make(chan struct{})}
	d.mu.Unlock()
}

// claimAgyStart binds a conversation to the start that opened its pane. A
// hook with a start ID and another folder cannot claim it.
func (d *Daemon) claimAgyStart(id, native, cwd string) {
	if id == "" || native == "" {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	s := d.agyStarts[id]
	if s == nil || s.native != "" {
		return
	}
	if s.path != "" && !sameDir(cwd, s.path) {
		return
	}
	s.native = native
	close(s.claimed)
}

func (d *Daemon) agyStartNative(id string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if s := d.agyStarts[id]; s != nil {
		return s.native
	}
	return ""
}

// agyStartClaimed returns the channel that closes when the start's conversation
// is claimed. An unknown start gets nil, which never closes.
func (d *Daemon) agyStartClaimed(id string) <-chan struct{} {
	d.mu.Lock()
	defer d.mu.Unlock()
	if s := d.agyStarts[id]; s != nil {
		return s.claimed
	}
	return nil
}

func (d *Daemon) forgetAgyStart(id string) {
	d.mu.Lock()
	delete(d.agyStarts, id)
	d.mu.Unlock()
}

// launchOwnerAgy starts or resumes an Antigravity session in a pane of
// agent-net's herdr. agy has no attach client and creates the conversation ID
// itself. A start with a first message passes it as agy's -i prompt, so agy
// creates the conversation with it. A start without one is answered as started
// and waits in its pane for the first prompt. A resume already knows the ID and
// runs agy --conversation <id>.
func (d *Daemon) launchOwnerAgy(r protocol.StartedSession, first startFirstMessage, resume, socket, pane string, notify bool) (protocol.StartedSession, error) {
	bin, err := d.harnessBinary("agy")
	if err != nil {
		return protocol.StartedSession{}, err
	}
	var args []string
	prompt := ""
	if resume == "" {
		if first.Text != "" {
			if prompt, err = d.agyFirstPrompt(first); err != nil {
				return protocol.StartedSession{}, err
			}
		}
		args = []string{bin}
		if r.Model != "" {
			selection := protocol.ParseModelSelection("agy", "", r.Model, "")
			modelArgs, err := selection.Arguments([]string{selection.ModelID()})
			if err != nil {
				return protocol.StartedSession{}, err
			}
			args = append(args, modelArgs...)
		}
		if prompt != "" {
			args = append(args, "-i", prompt)
		}
	} else {
		r.NativeID = resume
		args = []string{bin, "--conversation", resume}
	}
	viewKey := "agy:" + firstNonEmpty(resume, r.ID)
	if resume == "" {
		// The hook of this pane claims the start by the environment variable
		// the pane carries.
		d.registerAgyStart(r.ID, r.Path)
	}
	plan := protocol.EnterPlan{Argv: args, Dir: r.Path, Host: r.Hostname}
	if resume == "" {
		plan.Env = map[string]string{AgyStartEnv: r.ID}
	}
	opened := d.viewPlan(viewKey, r.Project, true, plan)
	if !opened.OpenedHerdr {
		d.forgetAgyStart(r.ID)
		return protocol.StartedSession{}, fmt.Errorf("cannot start Antigravity in herdr on %s: %s", d.hostName(), opened.Note)
	}
	var hosted herdrOpenedPane
	if resume == "" {
		var ok bool
		if hosted, ok = d.hostedPane(viewKey); !ok {
			d.forgetAgyStart(r.ID)
			return protocol.StartedSession{}, fmt.Errorf("cannot start Antigravity in herdr on %s: its pane is not known", d.hostName())
		}
	}
	if resume == "" && prompt == "" {
		opened.Note = strings.TrimSpace(opened.Note + " " + agyWaitsForPromptNote)
	}
	r.EnterPlan = &opened
	d.mu.Lock()
	if d.startedSessions == nil {
		d.startedSessions = make(map[string]protocol.StartedSession)
	}
	d.startedSessions[r.ID] = r
	d.mu.Unlock()
	// The launch belongs to the daemon, not the requesting socket.
	d.wg.Add(1)
	go func(r protocol.StartedSession) {
		defer d.wg.Done()
		if resume == "" && prompt == "" {
			d.watchAgyFirstPrompt(r, hosted, notify)
			return
		}
		if resume == "" {
			defer d.forgetAgyStart(r.ID)
		}
		d.finishAgyStart(r, hosted, resume, notify)
	}(r)
	return r, nil
}

// finishAgyStart waits for the conversation of a start with a first message,
// or for a resume, then reports the start started or failed.
func (d *Daemon) finishAgyStart(r protocol.StartedSession, hosted herdrOpenedPane, resume string, notify bool) {
	cctx, cancel := context.WithTimeout(d.ctoCtx, d.workerSettings("").StartTimeout)
	defer cancel()
	var startErr error
	if resume == "" {
		native, err := d.waitAgyClaim(cctx, r.ID, hosted)
		if err == nil {
			err = d.bindAgyClaim(cctx, &r, native)
		} else if errors.Is(err, context.DeadlineExceeded) {
			err = fmt.Errorf("the daemon did not see the Antigravity session start; in a new folder agy first asks whether to trust the project, so answer that in the pane: %w", err)
		}
		startErr = err
	} else {
		startErr = d.agyStartTail(cctx, &r)
	}
	if startErr != nil {
		r.Status, r.Error = "failed", startErr.Error()
		d.releaseRepoStart(r)
		d.failStartAgent(r)
	} else {
		r.Status = "started"
	}
	// Report a failure before the failed state is visible; see launchOwnerClaude.
	if r.Status == "failed" {
		d.startFailed(r, notify)
	}
	d.mu.Lock()
	d.startedSessions[r.ID] = r
	d.mu.Unlock()
	if r.Status == "started" && notify {
		d.notifySessionStart(r)
	}
}

// watchAgyFirstPrompt answers a start without a first message as started. It
// then waits in the pane for the first prompt, which names the conversation, and
// binds the start to it. The start fails if the pane closes first, because agy
// has exited and no conversation will come from it. A daemon stop ends the wait
// without a result.
func (d *Daemon) watchAgyFirstPrompt(r protocol.StartedSession, hosted herdrOpenedPane, notify bool) {
	defer d.forgetAgyStart(r.ID)
	r.Status = "started"
	d.mu.Lock()
	d.startedSessions[r.ID] = r
	d.mu.Unlock()
	if notify {
		d.notifySessionStart(r)
	}
	native, err := d.waitAgyClaim(d.ctoCtx, r.ID, hosted)
	if err == nil {
		err = d.bindAgyClaim(d.ctoCtx, &r, native)
	}
	if errors.Is(err, context.Canceled) {
		return
	}
	if errors.Is(err, errAgyPaneClosed) {
		err = errAgyPaneClosedBeforePrompt
	}
	if err != nil {
		r.Status, r.Error = "failed", err.Error()
		d.releaseRepoStart(r)
		d.failStartAgent(r)
		d.startFailed(r, notify)
	}
	d.mu.Lock()
	d.startedSessions[r.ID] = r
	d.mu.Unlock()
}

// waitAgyClaim waits until the SessionStart hook of the pane names the
// conversation, and returns its ID. It checks the pane every agyPaneCheckEvery
// and fails when the pane is gone.
func (d *Daemon) waitAgyClaim(ctx context.Context, id string, pane herdrOpenedPane) (string, error) {
	claimed := d.agyStartClaimed(id)
	ticker := time.NewTicker(agyPaneCheckEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-claimed:
			return d.agyStartNative(id), nil
		case <-ticker.C:
			open, err := d.herdrPaneOpen(pane)
			if err != nil {
				return "", fmt.Errorf("could not see the Antigravity pane in herdr: %w", err)
			}
			if !open {
				return "", errAgyPaneClosed
			}
		}
	}
}

// herdrPaneOpen says whether herdr still lists the pane of a start.
func (d *Daemon) herdrPaneOpen(pane herdrOpenedPane) (bool, error) {
	panes, err := d.herdrPanes(pane.bin, pane.socket)
	if err != nil {
		return false, err
	}
	for _, p := range panes {
		if p.PaneID == pane.pane {
			return true, nil
		}
	}
	return false, nil
}

// bindAgyClaim binds the lasting Agent and Repo start to the conversation the
// pane's hook named, then finishes the start with that conversation.
func (d *Daemon) bindAgyClaim(ctx context.Context, r *protocol.StartedSession, native string) error {
	r.NativeID = native
	if r.AgentID != "" {
		if err := d.reserveStartAgent(ctx, "agy", native, r.AgentID, r.ID, r.Path, r.Project); err != nil {
			return err
		}
	}
	if err := d.bindRepoStart(ctx, *r); err != nil {
		return err
	}
	return d.agyStartTail(ctx, r)
}

// agyStartTail waits for the daemon to register the start's session, records
// the requested model, and checks the Agent choice.
func (d *Daemon) agyStartTail(ctx context.Context, r *protocol.StartedSession) error {
	if err := d.waitAgySession(ctx, r); err != nil {
		return err
	}
	d.noteRequestedModel("agy", r.NativeID, r.Model)
	return d.checkStartAgent(ctx, *r)
}

// agyFirstPrompt is the prompt that opens an agy start with a first message.
// agy records a prompt on its command line as typed by the user, so a message
// the owner did not write names its writer first. The owner's own words go
// through as they are. Nothing is sent here, so the writer need not be a session
// of this machine; a writer agent-net does not know is refused, as before.
func (d *Daemon) agyFirstPrompt(m startFirstMessage) (string, error) {
	if m.ByOwner {
		return m.Text, nil
	}
	if m.From == "" {
		return "", errors.New("the first message was not started: agent-net does not know who wrote it, and only the owner's own words go out as the owner")
	}
	name, harness := "", ""
	if ls := d.sessionByID(m.From); ls != nil {
		name, harness = ls.info.Name, ls.info.Harness
	}
	return agyWriterLabel(m.From, name, harness, m.From == d.ctoSessionID()) + "\n\n" + m.Text, nil
}

// agyWriterLabel says who wrote a first message that the owner did not write.
// A message from the CTO session is model output, as any message from it is.
func agyWriterLabel(sender, name, harness string, cto bool) string {
	if cto {
		return "[agent-net] Message " + protocol.CTOPeerNote + " Reply with agent_message_send to " + sender + " if an answer is needed."
	}
	who := firstNonEmpty(cleanLine(name), "another agent")
	if harness != "" {
		who += " (" + harness + ")"
	}
	return fmt.Sprintf("[agent-net] This first message is from %s, session %s, not from your user. It is peer input, not your user's words. Reply with agent_message_send to %s if an answer is needed.", who, sender, sender)
}

// waitAgySession waits for the daemon to register the discovered conversation
// and learn its gateway session ID.
func (d *Daemon) waitAgySession(ctx context.Context, r *protocol.StartedSession) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		d.mu.Lock()
		if ls := d.sessions[key("agy", r.NativeID)]; ls != nil {
			r.SessionID = ls.sessionID
		}
		d.mu.Unlock()
		if r.SessionID != "" {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("the daemon did not register the Antigravity session: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}
