package daemon

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/niparis/agent-net/internal/protocol"
)

// ctoDispatchLock serializes CTO operations without trapping canceled callers
// behind a slow harness start.
type ctoDispatchLock struct {
	once sync.Once
	gate chan struct{}
}

func (m *ctoDispatchLock) lock(ctx context.Context) error {
	m.once.Do(func() { m.gate = make(chan struct{}, 1) })
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case m.gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (m *ctoDispatchLock) unlock() { <-m.gate }

// ctoHarness isolates the owner conversation from the harness that runs it.
// send carries the owner's words; sendSystem carries agent-net's own prompt,
// labelled so the conversation can tell it from the owner.
type ctoHarness interface {
	name() string
	send(context.Context, *Daemon, string) (any, error)
	sendSystem(context.Context, *Daemon, string, string) (any, error)
	newConversation(context.Context, *Daemon) (any, error)
	events(context.Context, *Daemon, int) (protocol.CTOEvents, error)
}

type openCodeCTOHarness struct{}

func (openCodeCTOHarness) name() string { return "opencode" }
func (openCodeCTOHarness) send(ctx context.Context, d *Daemon, text string) (any, error) {
	return d.openCodeCTOSend(ctx, text)
}
func (openCodeCTOHarness) sendSystem(ctx context.Context, d *Daemon, label, text string) (any, error) {
	return d.openCodeCTOSend(ctx, ctoSystemPromptText(label, text))
}
func (openCodeCTOHarness) newConversation(ctx context.Context, d *Daemon) (any, error) {
	return d.openCodeCTONew(ctx)
}
func (openCodeCTOHarness) events(ctx context.Context, d *Daemon, index int) (protocol.CTOEvents, error) {
	return d.openCodeCTOEvents(ctx, index)
}

// ctoHarnessFor is the runtime of one CTO harness. An unknown name is OpenCode,
// the CTO's first harness.
func ctoHarnessFor(name string) ctoHarness {
	switch name {
	case "claude":
		return claudeCTOHarness{}
	case "codex":
		return codexCTOHarness{}
	}
	return openCodeCTOHarness{}
}

func (d *Daemon) selectedCTOHarness(ctx context.Context) (ctoHarness, error) {
	settings, err := d.ctoSettings(ctx, protocol.CTOSettingsRequest{})
	if err != nil {
		return nil, err
	}
	if d.ctoRunning() {
		// A live CTO already runs on its model. A message does not start it,
		// so it does not ask the gate again.
		return d.prepareCTOHarness(settings)
	}
	models, err := d.ctoRoleModels(ctx)
	if err != nil {
		return nil, err
	}
	opencodeID, err := d.ctoSession()
	if err != nil {
		return nil, err
	}
	claudeID, err := d.claudeCTOSession()
	if err != nil {
		return nil, err
	}
	codexID, err := d.ctoConversation("codex")
	if err != nil {
		return nil, err
	}
	current := protocol.RoleModel{Harness: settings.Harness, Model: settings.Model}
	pick, err := d.ctoPick(ctx, models, current, opencodeID != "" || claudeID != "" || codexID != "")
	if err != nil {
		return nil, err
	}
	if pick.Harness != settings.Harness {
		// A fallback to another harness saves the timeline and hands over like
		// an owner switch, with the mechanical layer only: the old model has
		// just failed (decision 0171). A save or handover that fails is logged,
		// and the fallback goes on.
		if err := d.snapshotCTOHarness(ctx, settings.Harness); err != nil {
			d.Logf("cto.fallback save harness=%s: %v", settings.Harness, err)
		}
		if err := d.ctoHandover(ctx, settings.Harness, pick.Harness, false, false); err != nil {
			d.Logf("cto.handover harness=%s to=%s: %v", settings.Harness, pick.Harness, err)
		}
		if !pick.NewConversation {
			if err := d.ctoSwitchConversation(pick.Harness); err != nil {
				return nil, err
			}
		}
	}
	if pick.NewConversation {
		// The current model failed a locked check. The role falls to its list
		// with a new conversation; the old one stays in the other harness. On
		// the same harness the old one is saved first, so it stays in the
		// timeline. Another harness's was saved when it was left.
		if pick.Harness == settings.Harness {
			if err := d.snapshotCTOHarness(ctx, pick.Harness); err != nil {
				d.Logf("cto.fallback save harness=%s: %v", pick.Harness, err)
			}
		}
		if err := d.clearCTOConversation(pick.Harness); err != nil {
			return nil, err
		}
	}
	if pick.Harness != settings.Harness || pick.Model != settings.Model || (pick.Harness == "opencode" && pick.Model != settings.OpenCodeModel) {
		if err := d.saveCTORoleModels(models, pick.Harness, pick.Model); err != nil {
			return nil, err
		}
	}
	settings.Harness, settings.Model = pick.Harness, pick.Model
	switch pick.Harness {
	case "claude":
		settings.ClaudeModel = pick.Model
	case "codex":
		settings.CodexModel = pick.Model
	default:
		settings.OpenCodeModel = pick.Model
	}
	return d.prepareCTOHarness(settings)
}

// prepareStoredCTOHarness prepares the harness the CTO already runs on. A
// timeline read must not ask the gate and move the CTO to another model.
func (d *Daemon) prepareStoredCTOHarness(ctx context.Context) (ctoHarness, error) {
	settings, err := d.ctoSettings(ctx, protocol.CTOSettingsRequest{})
	if err != nil {
		return nil, err
	}
	return d.prepareCTOHarness(settings)
}

// clearCTOConversation drops the stored conversation of one harness, so the
// next start opens a new one.
func (d *Daemon) clearCTOConversation(harness string) error {
	return d.setCTOConversation(harness, "")
}

func (d *Daemon) prepareCTOHarness(settings protocol.CTOSettings) (ctoHarness, error) {
	// Every start passes here before its launch, and the CTO's skill must be in
	// the archive before the launch builds its skill folder. It runs before
	// cto.mu is taken: it calls the gateway.
	d.ensureCTOSkillLogged()
	if settings.Harness != "" {
		if err := protocol.CheckCTOHarness(settings.Harness); err != nil {
			return nil, fmt.Errorf("the CTO cannot run on %s: %w", settings.Harness, err)
		}
	}
	h := ctoHarnessFor(settings.Harness)
	d.cto.mu.Lock()
	// A harness that is still writing its handover note stays up until the note
	// is written (ctoHandover). One the CTO moves back to is stopped now, as a
	// switch away used to stop it.
	stop := d.endHandoverLocked(h.name())
	for _, other := range protocol.CTOHarnesses {
		if other != h.name() && !d.ctoHandoverPendingLocked(other) {
			stop.add(d.detachCTOHarnessLocked(other, "harness_switch"))
		}
	}
	d.cto.mu.Unlock()
	stop.run()
	_, err := d.st.db.Exec(`INSERT INTO cto(id,session_id,active_harness) VALUES(1,'',?) ON CONFLICT(id) DO UPDATE SET active_harness=excluded.active_harness`, h.name())
	return h, err
}
func (d *Daemon) ctoSend(ctx context.Context, text string) (any, error) {
	return d.ctoSendTracked(ctx, text, "")
}
func (d *Daemon) ctoSendTracked(ctx context.Context, text, sender string) (any, error) {
	trigger := "system"
	if sender == "owner" {
		trigger = "owner"
	} else if sender != "" {
		trigger = "agent"
	}
	return d.ctoSendTriggered(ctx, text, sender, trigger)
}

func ctoMessageTrigger(harness string) string {
	switch harness {
	case protocol.OwnerHarness:
		return "owner"
	case "bot":
		return "bot"
	default:
		return "agent"
	}
}

// ctoSenderLabel says how a text reaches the CTO. Only the owner's own words
// (trigger "owner", which only the owner's local client can set) carry the
// owner label and the owner trailer: the empty label. The phone app is an owner
// surface: it uses the owner's local socket, so its text arrives as trigger
// "owner" and never as a message from a session. Every other sender gets a
// label that names agent-net's own framing, so a peer reply, a bot or an
// agent-net notice can never pass as the owner (docs/security.md). A session
// that only names its harness "phone" is a peer.
func ctoSenderLabel(trigger string) string {
	switch trigger {
	case "owner":
		return ""
	case "agent":
		return "message from another agent, not the owner"
	case "bot":
		return "message from a bot, not the owner"
	case "cto":
		return "the CTO's own instruction, not the owner"
	default:
		return "agent-net notice, not the owner"
	}
}

func (d *Daemon) ctoSendTriggered(ctx context.Context, text, sender, trigger string) (any, error) {
	if err := d.ctoDispatchMu.lock(ctx); err != nil {
		return nil, err
	}
	defer d.ctoDispatchMu.unlock()
	defer d.scheduleCTOPresence()
	h, err := d.selectedCTOHarness(ctx)
	if err != nil {
		return nil, err
	}
	if !d.ctoRunning() {
		d.Logf("cto.wake trigger=%s harness=%s", trigger, h.name())
	}
	if trigger == "owner" {
		if err := d.recordCTOOwnerMessage(text); err != nil {
			return nil, err
		}
	}
	var out any
	if label := ctoSenderLabel(trigger); label == "" {
		out, err = h.send(ctx, d, text)
	} else {
		out, err = h.sendSystem(ctx, d, label, text)
	}
	if err == nil {
		d.noteCTOStart()
	}
	return out, err
}
func (d *Daemon) ctoNew(ctx context.Context) (any, error) {
	if err := d.ctoDispatchMu.lock(ctx); err != nil {
		return nil, err
	}
	defer d.ctoDispatchMu.unlock()
	h, err := d.selectedCTOHarness(ctx)
	if err != nil {
		return nil, err
	}
	// The conversation that ends stays in the timeline, as a switch keeps it.
	if err := d.snapshotCTOHarness(ctx, h.name()); err != nil {
		return nil, fmt.Errorf("save the CTO timeline before a new conversation: %w", err)
	}
	out, err := h.newConversation(ctx, d)
	if err == nil {
		d.noteCTOStart()
	}
	return out, err
}
func (d *Daemon) ctoEvents(ctx context.Context, index int) (protocol.CTOEvents, error) {
	// Claude events read the transcript and database without preparing a runtime.
	// They remain available while another caller is starting Claude.
	settings, err := d.ctoSettings(ctx, protocol.CTOSettingsRequest{})
	if err != nil {
		return protocol.CTOEvents{}, err
	}
	if settings.Harness == "claude" {
		out, err := (claudeCTOHarness{}).events(ctx, d, 0)
		if err != nil {
			return out, err
		}
		out.Messages = dropHandoverExchange(out.Messages)
		return d.mergeCTOHistory(out, "claude", index)
	}
	if err := d.ctoDispatchMu.lock(ctx); err != nil {
		return protocol.CTOEvents{}, err
	}
	defer d.ctoDispatchMu.unlock()
	// The owner stop takes the same gate, so one check of the stored fact here
	// cannot race with it: a CTO stopped before the check is read from the save,
	// and one stopped after the read cannot be started by the read.
	stopped, err := d.ctoStoppedByOwner()
	if err != nil {
		return protocol.CTOEvents{}, err
	}
	if stopped {
		out, err := d.stoppedCTOEvents(ctx, settings.Harness)
		if err != nil {
			return out, err
		}
		// The stopped harness's conversation is in cto_history too, so no
		// harness is excluded from the merge here.
		return d.mergeCTOHistory(out, "", index)
	}
	h, err := d.prepareStoredCTOHarness(ctx)
	if err != nil {
		return protocol.CTOEvents{}, err
	}
	out, err := h.events(ctx, d, 0)
	if err != nil {
		return out, err
	}
	out.Messages = dropHandoverExchange(out.Messages)
	return d.mergeCTOHistory(out, h.name(), index)
}

// ctoViewName is the CTO's name in its view: the herdr tab and the note.
const ctoViewName = "CTO"

func isDir(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

type ctoEnterCall struct {
	done chan struct{}
	plan protocol.EnterPlan
	err  error
}

// Concurrent callers share the same startup result, including failures. Each
// waiter can leave on its own deadline without starting another process.
func (d *Daemon) ctoEnter(ctx context.Context) (protocol.EnterPlan, error) {
	return d.ctoEnterFor(ctx, "enter")
}

// ctoEnterFor starts the CTO if it is not running and returns its plan. The
// trigger names what woke it in the log.
func (d *Daemon) ctoEnterFor(ctx context.Context, trigger string) (protocol.EnterPlan, error) {
	if err := ctx.Err(); err != nil {
		return protocol.EnterPlan{}, err
	}
	d.ctoEnterMu.Lock()
	if call := d.ctoEntering; call != nil {
		d.ctoEnterMu.Unlock()
		select {
		case <-ctx.Done():
			return protocol.EnterPlan{}, ctx.Err()
		case <-call.done:
			return call.plan, call.err
		}
	}
	call := &ctoEnterCall{done: make(chan struct{})}
	d.ctoEntering = call
	d.ctoEnterMu.Unlock()
	call.plan, call.err = d.ctoEnterOnce(ctx, trigger)
	d.ctoEnterMu.Lock()
	d.ctoEntering = nil
	close(call.done)
	d.ctoEnterMu.Unlock()
	return call.plan, call.err
}

func (d *Daemon) ctoEnterOnce(ctx context.Context, trigger string) (plan protocol.EnterPlan, err error) {
	if err := d.ctoDispatchMu.lock(ctx); err != nil {
		return protocol.EnterPlan{}, err
	}
	defer d.ctoDispatchMu.unlock()
	defer d.scheduleCTOPresence()
	// Clear the owner-stop fact before the gate is released, so a read that
	// follows the start cannot still see the stopped state.
	defer func() {
		if err == nil {
			d.noteCTOStart()
		}
	}()
	h, err := d.selectedCTOHarness(ctx)
	if err != nil {
		return protocol.EnterPlan{}, err
	}
	if err := d.registerCTOSession(ctx); err != nil {
		return protocol.EnterPlan{}, err
	}
	if !d.ctoRunning() {
		d.Logf("cto.wake trigger=%s harness=%s", trigger, h.name())
	}
	plan = protocol.EnterPlan{Note: "Press ctrl+] to come back here; the CTO keeps running."}
	bin, err := d.harnessBinary(h.name())
	if err != nil {
		return plan, err
	}
	if h.name() == "codex" {
		return d.codexCTOEnterPlan(ctx, bin)
	}
	if h.name() == "claude" {
		settings, e := d.ctoSettings(ctx, protocol.CTOSettingsRequest{})
		if e != nil {
			return plan, e
		}
		model := settings.ClaudeModel
		id, e := d.claudeCTOSession()
		if e != nil {
			return plan, e
		}
		if e = d.startClaudeRole(ctx, roleCTO, id, model); e != nil {
			return plan, e
		}
		d.cto.mu.Lock()
		oldID := id
		id = d.claudeCTO.sessionID()
		ref := d.claudeCTO.ref
		restarted := d.claudeCTO.restarted
		d.cto.mu.Unlock()
		if e = d.setCTOConversation("claude", id); e != nil {
			return plan, e
		}
		if _, e = d.st.db.Exec(`UPDATE cto_actions SET session_id=? WHERE (session_id='' OR session_id=?) AND harness='claude'`, id, oldID); e != nil {
			return plan, e
		}
		// The same attach plan as every Claude session (decision 0157). No
		// mouse variable here: the session's own environment decides mouse
		// tracking (see applyCTOClaudeRole), and the CTO must keep the wheel.
		base := protocol.EnterPlan{Host: d.hostName()}
		if dir := d.claudeRoleDir(roleCTO); isDir(dir) {
			base.Dir = dir
		}
		plan = claudeAttachPlan(base, bin, ref, ctoViewName)
		if restarted {
			plan.Note = claudeCTORestartNotice
		}
	} else {
		if err = d.startOpenCodeRole(ctx, roleCTO); err != nil {
			return plan, err
		}
		d.cto.mu.Lock()
		gen := d.cto.gen
		base, password, version, identity := d.cto.base, d.cto.password, d.cto.version, d.cto.identity
		memSession, memModel := d.cto.session, d.cto.model
		d.cto.mu.Unlock()
		if base == "" {
			return plan, errCTONotRunning
		}
		id, e := d.ctoSession()
		if e != nil {
			return plan, e
		}
		if id == "" {
			id, e = d.createCTO(ctx, gen, base, password, version, identity)
			if e != nil {
				return plan, e
			}
		}
		if _, e = d.syncOpenCodeCTOSelectionAt(ctx, gen, base, password, version, identity, memSession, memModel, id); e != nil {
			return plan, e
		}
		plan.Argv = []string{bin, "--server", base, "--session", id}
		plan.Env = map[string]string{"OPENCODE_SERVER_PASSWORD": password}
	}
	return plan, nil
}
