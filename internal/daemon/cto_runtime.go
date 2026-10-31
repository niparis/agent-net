package daemon

import (
	"context"

	"github.com/niparis/agent-net/internal/protocol"
)

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
		d.cto.stopLocked()
	} else {
		d.stopClaudeCTOLocked()
	}
	_, err := d.st.db.Exec(`INSERT INTO cto(id,session_id,active_harness) VALUES(1,'',?) ON CONFLICT(id) DO UPDATE SET active_harness=excluded.active_harness`, h.name())
	return h, err
}
func (d *Daemon) ctoSend(ctx context.Context, text string) (any, error) {
	d.ctoDispatchMu.Lock()
	defer d.ctoDispatchMu.Unlock()
	h, err := d.selectedCTOHarness(ctx)
	if err != nil {
		return nil, err
	}
	return h.send(ctx, d, text)
}
func (d *Daemon) ctoNew(ctx context.Context) (any, error) {
	d.ctoDispatchMu.Lock()
	defer d.ctoDispatchMu.Unlock()
	h, err := d.selectedCTOHarness(ctx)
	if err != nil {
		return nil, err
	}
	return h.newConversation(ctx, d)
}
func (d *Daemon) ctoEvents(ctx context.Context, index int) (protocol.CTOEvents, error) {
	d.ctoDispatchMu.Lock()
	defer d.ctoDispatchMu.Unlock()
	h, err := d.selectedCTOHarness(ctx)
	if err != nil {
		return protocol.CTOEvents{}, err
	}
	out, err := h.events(ctx, d, 0)
	if err != nil {
		return out, err
	}
	return d.mergeCTOHistory(out, h.name(), index)
}

func (d *Daemon) ctoEnter(ctx context.Context) (protocol.EnterPlan, error) {
	d.ctoDispatchMu.Lock()
	defer d.ctoDispatchMu.Unlock()
	h, err := d.selectedCTOHarness(ctx)
	if err != nil {
		return protocol.EnterPlan{}, err
	}
	d.cto.mu.Lock()
	defer d.cto.mu.Unlock()
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
		if e = d.startClaudeCTOLocked(ctx, id, model); e != nil {
			return plan, e
		}
		id = d.claudeCTO.session
		if _, e = d.st.db.Exec(`INSERT INTO cto(id,session_id,claude_session_id) VALUES(1,'',?) ON CONFLICT(id) DO UPDATE SET claude_session_id=excluded.claude_session_id`, id); e != nil {
			return plan, e
		}
		if _, e = d.st.db.Exec(`UPDATE cto_actions SET session_id=? WHERE session_id='' AND harness='claude'`, id); e != nil {
			return plan, e
		}
		plan.Argv = []string{bin, "attach", d.claudeCTO.ref}
	} else {
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
