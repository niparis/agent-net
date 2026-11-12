package daemon

import (
	"context"
	"sync"
	"time"

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
type ctoHarness interface {
	name() string
	send(context.Context, *Daemon, string) (any, error)
	newConversation(context.Context, *Daemon) (any, error)
	events(context.Context, *Daemon, int) (protocol.CTOEvents, error)
}

type openCodeCTOHarness struct{}

func (openCodeCTOHarness) name() string { return "opencode" }
func (openCodeCTOHarness) send(ctx context.Context, d *Daemon, text string) (any, error) {
	return d.openCodeCTOSend(ctx, text)
}
func (openCodeCTOHarness) newConversation(ctx context.Context, d *Daemon) (any, error) {
	return d.openCodeCTONew(ctx)
}
func (openCodeCTOHarness) events(ctx context.Context, d *Daemon, index int) (protocol.CTOEvents, error) {
	return d.openCodeCTOEvents(ctx, index)
}

func (d *Daemon) selectedCTOHarness(ctx context.Context) (ctoHarness, error) {
	settings, err := d.ctoSettings(ctx, protocol.CTOSettingsRequest{})
	if err != nil {
		return nil, err
	}
	return d.prepareCTOHarness(settings)
}

func (d *Daemon) prepareCTOHarness(settings protocol.CTOSettings) (ctoHarness, error) {
	var h ctoHarness = openCodeCTOHarness{}
	if settings.Harness == "claude" {
		h = claudeCTOHarness{}
	}
	d.cto.mu.Lock()
	defer d.cto.mu.Unlock()
	if h.name() == "claude" {
		d.stopOpenCodeCTOLocked("harness_switch")
	} else {
		d.stopClaudeCTOLocked("harness_switch")
	}
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
	case "phone":
		return "phone"
	default:
		return "agent"
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
	out, err := h.send(ctx, d, text)
	if err == nil && sender != "" {
		err = d.st.recordCTODelivery(sender, time.Now())
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
	return h.newConversation(ctx, d)
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
		return d.mergeCTOHistory(out, "claude", index)
	}
	if err := d.ctoDispatchMu.lock(ctx); err != nil {
		return protocol.CTOEvents{}, err
	}
	defer d.ctoDispatchMu.unlock()
	h, err := d.selectedCTOHarness(ctx)
	if err != nil {
		return protocol.CTOEvents{}, err
	}
	if id, e := d.ctoSession(); e == nil && id != "" && !d.ctoRunning() {
		d.Logf("cto.wake trigger=timeline harness=%s", h.name())
	}
	out, err := h.events(ctx, d, 0)
	if err != nil {
		return out, err
	}
	return d.mergeCTOHistory(out, h.name(), index)
}

type ctoEnterCall struct {
	done chan struct{}
	plan protocol.EnterPlan
	err  error
}

// Concurrent callers share the same startup result, including failures. Each
// waiter can leave on its own deadline without starting another process.
func (d *Daemon) ctoEnter(ctx context.Context) (protocol.EnterPlan, error) {
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
	call.plan, call.err = d.ctoEnterOnce(ctx)
	d.ctoEnterMu.Lock()
	d.ctoEntering = nil
	close(call.done)
	d.ctoEnterMu.Unlock()
	return call.plan, call.err
}

func (d *Daemon) ctoEnterOnce(ctx context.Context) (protocol.EnterPlan, error) {
	if err := d.ctoDispatchMu.lock(ctx); err != nil {
		return protocol.EnterPlan{}, err
	}
	defer d.ctoDispatchMu.unlock()
	defer d.scheduleCTOPresence()
	h, err := d.selectedCTOHarness(ctx)
	if err != nil {
		return protocol.EnterPlan{}, err
	}
	if err := d.registerCTOSession(ctx); err != nil {
		return protocol.EnterPlan{}, err
	}
	if !d.ctoRunning() {
		d.Logf("cto.wake trigger=enter harness=%s", h.name())
	}
	plan := protocol.EnterPlan{Note: "Press ctrl+] to come back here; the CTO keeps running."}
	bin, err := d.harnessBinary(h.name())
	if err != nil {
		return plan, err
	}
	if h.name() == "claude" {
		settings, e := d.ctoSettings(ctx, protocol.CTOSettingsRequest{})
		if e != nil {
			return plan, e
		}
		model := settings.ClaudeModel
		if model == "" {
			model = "opus"
		}
		id, e := d.claudeCTOSession()
		if e != nil {
			return plan, e
		}
		if e = d.startClaudeCTO(ctx, id, model); e != nil {
			return plan, e
		}
		d.cto.mu.Lock()
		defer d.cto.mu.Unlock()
		oldID := id
		id = d.claudeCTO.session
		if _, e = d.st.db.Exec(`INSERT INTO cto(id,session_id,claude_session_id) VALUES(1,'',?) ON CONFLICT(id) DO UPDATE SET claude_session_id=excluded.claude_session_id`, id); e != nil {
			return plan, e
		}
		if _, e = d.st.db.Exec(`UPDATE cto_actions SET session_id=? WHERE (session_id='' OR session_id=?) AND harness='claude'`, id, oldID); e != nil {
			return plan, e
		}
		if d.claudeCTO.restarted {
			plan.Note = claudeCTORestartNotice
		}
		// No mouse variable here: the session's own environment decides mouse
		// tracking (see startClaudeCTOOnce), and the CTO must keep the wheel.
		plan.Argv = []string{bin, "attach", d.claudeCTO.ref}
	} else {
		d.cto.mu.Lock()
		defer d.cto.mu.Unlock()
		if err = d.startCTOLocked(ctx); err != nil {
			return plan, err
		}
		id, e := d.ctoSession()
		if e != nil {
			return plan, e
		}
		if id == "" {
			id, e = d.createCTOLocked(ctx)
			if e != nil {
				return plan, e
			}
		}
		if _, e = d.syncOpenCodeCTOSelection(ctx, id); e != nil {
			return plan, e
		}
		plan.Argv = []string{bin, "--server", d.cto.base, "--session", id}
		plan.Env = map[string]string{"OPENCODE_SERVER_PASSWORD": d.cto.password}
	}
	return plan, nil
}
