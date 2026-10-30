package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/niparis/agent-net/internal/integrations"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/quotaaxi"
)

// This file reads the owner's subscription quotas with quota-axi (decision
// 0022). quota-axi is a Node CLI that reads the logins already on the machine.
// agent-net installs a pinned private copy when the owner has none (decision
// 0025), never asks quota-axi to refresh a credential, and never asks for
// account identity: the call is exactly
//
//	quota-axi --json --max-age 5m --no-credential-refresh
//
// The reading goes to the gateway with the usage report. The newest good
// reading is kept when a run fails, so a transient provider error does not
// erase what was known.

const (
	// defaultQuotaInterval is how often quota-axi is asked. It caches its own
	// reading for 5 minutes, so a shorter interval would not help.
	defaultQuotaInterval = 5 * time.Minute
	// quotaTimeout bounds one run. A provider that answers slowly must not
	// hold the loop.
	quotaTimeout = 90 * time.Second
	// quotaCommand is the name of the CLI.
	quotaCommand = "quota-axi"
)

// quotaArgs are the only arguments agent-net passes. --full is never used:
// it would include account identity and derivation detail.
var quotaArgs = []string{"--json", "--max-age", "5m", "--no-credential-refresh"}

// ensureQuotaAXI installs the private copy. Tests replace it so they never
// run npm.
var ensureQuotaAXI = quotaaxi.Ensure

type quotaState struct {
	mu   sync.Mutex
	last *protocol.Quotas
	at   time.Time
	err  string
	// note is why no reading can be made yet, for example missing Node. It is
	// shown only in the empty state of the TUI Quotas tab.
	note string
}

func (q *quotaState) lock()   { q.mu.Lock() }
func (q *quotaState) unlock() { q.mu.Unlock() }

// set records a good reading.
func (q *quotaState) set(p *protocol.Quotas) {
	q.lock()
	defer q.unlock()
	q.last, q.at, q.err = p, time.Now(), ""
}

// fail keeps the last good reading and records why the newest run failed.
func (q *quotaState) fail(err error) {
	q.lock()
	defer q.unlock()
	q.err = err.Error()
}

// snapshot returns the last good reading, or nil.
func (q *quotaState) snapshot() *protocol.Quotas {
	q.lock()
	defer q.unlock()
	if q.last == nil {
		return nil
	}
	return cloneQuotas(q.last)
}

// report returns the reading and its most recent command error together.
func (q *quotaState) report() (*protocol.Quotas, string) {
	q.lock()
	defer q.unlock()
	return cloneQuotas(q.last), q.err
}

// errorText returns why the newest run failed, or "".
func (q *quotaState) errorText() string {
	q.lock()
	defer q.unlock()
	return q.err
}

// setNote records why quota-axi cannot run yet, or "" to clear it.
func (q *quotaState) setNote(note string) {
	q.lock()
	defer q.unlock()
	q.note = note
}

// noteText returns the current install note.
func (q *quotaState) noteText() string {
	q.lock()
	defer q.unlock()
	return q.note
}

func cloneQuotas(q *protocol.Quotas) *protocol.Quotas {
	if q == nil {
		return nil
	}
	cp := *q
	cp.Providers = make([]protocol.QuotaProvider, len(q.Providers))
	for i, p := range q.Providers {
		cp.Providers[i] = p
		cp.Providers[i].Windows = append([]protocol.QuotaWindow(nil), p.Windows...)
	}
	return &cp
}

// FindQuotaAXI finds the quota-axi to run: the configured path, else the one
// on PATH, else ~/.local/bin/quota-axi, else the usual Node folders on macOS,
// else agent-net's private copy. A copy the owner installed always wins.
func FindQuotaAXI(configured string, logf ...func(format string, args ...any)) (string, bool) {
	return quotaaxi.Path(configured, logf...)
}

// quotaAllowed is the owner's answer for the quota-axi integration. It is on
// by default, including for an older setup record that predates the item, so
// an installed quota-axi is read unless the owner turned it off.
func quotaAllowed(configDir string) bool {
	applied, ok := integrations.LoadApplied(configDir)
	if !ok {
		return true
	}
	return applied.QuotaAXI
}

// startQuota runs the quota reader when quota-axi is installed and the owner
// allowed it. When it is allowed but missing, it installs the private copy in
// the background (decision 0025), then reads with it.
func (d *Daemon) startQuota(ctx context.Context) {
	allowed := quotaAllowed(ConfigDir())
	every := d.cfg.QuotaInterval
	if every <= 0 {
		every = defaultQuotaInterval
	}
	path, ok := FindQuotaAXI(d.cfg.QuotaAXIPath, d.Logf)
	if !ok {
		if !allowed {
			return
		}
		d.wg.Add(1)
		go func() {
			defer d.wg.Done()
			if err := ensureQuotaAXI(ctx, d.Logf); err != nil {
				if errors.Is(err, quotaaxi.ErrNeedNode) {
					d.quota.setNote(quotaaxi.NeedNodeMessage)
					d.Logf("%s", quotaaxi.NeedNodeMessage)
				}
				return
			}
			p, ok := FindQuotaAXI(d.cfg.QuotaAXIPath, d.Logf)
			if !ok {
				return
			}
			d.quota.setNote("")
			d.quotaLoop(ctx, p, every)
		}()
		return
	}
	if !allowed {
		d.Logf("quota-axi is installed but the integration is off")
		return
	}
	d.quota.setNote("")
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		d.quotaLoop(ctx, path, every)
	}()
}

func (d *Daemon) quotaLoop(ctx context.Context, path string, every time.Duration) {
	// Read at once, then on the interval.
	for {
		d.refreshQuota(ctx, path)
		t := time.NewTimer(every)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
}

// refreshQuota runs quota-axi once and records the result.
func (d *Daemon) refreshQuota(ctx context.Context, path string) *protocol.Quotas {
	rctx, cancel := context.WithTimeout(ctx, quotaTimeout)
	defer cancel()
	q, err := readQuotaAXI(rctx, path)
	if err != nil {
		d.quota.fail(err)
		d.Logf("quota-axi: %v", err)
		return nil
	}
	d.quota.set(q)
	return q
}

// readQuotaAXI runs the CLI and parses its JSON.
func readQuotaAXI(ctx context.Context, path string) (*protocol.Quotas, error) {
	cmd := exec.CommandContext(ctx, path, quotaArgs...)
	cmd.Env = quotaaxi.ExecEnv(path)
	// A shell wrapper can leave a child holding stdout after the context kills
	// it. Stop waiting for the pipe after a short delay.
	cmd.WaitDelay = time.Second
	raw, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("it did not answer within %s", quotaTimeout)
		}
		return nil, fmt.Errorf("it failed: %v", err)
	}
	return parseQuotaAXI(raw)
}

// quotaAXIReport is the part of quota-axi's JSON model that agent-net reads.
// See the quota-axi README ("Quota windows", "Pace signals" and "Effective
// usable runway") for the fields and their meaning.
type quotaAXIReport struct {
	GeneratedAt   string `json:"generatedAt"`
	SchemaVersion int    `json:"schemaVersion"`
	Providers     []struct {
		Provider string `json:"provider"`
		Plan     string `json:"plan"`
		Windows  []struct {
			ID               string   `json:"id"`
			Label            string   `json:"label"`
			Kind             string   `json:"kind"`
			PercentRemaining *float64 `json:"percentRemaining"`
			PercentUsed      *float64 `json:"percentUsed"`
			ResetsAt         string   `json:"resetsAt"`
			Pace             *struct {
				Status string `json:"status"`
			} `json:"pace"`
		} `json:"windows"`
		State struct {
			Status string `json:"status"`
			Stale  bool   `json:"stale"`
			Error  string `json:"error"`
		} `json:"state"`
		QuotaSemantics struct {
			EffectiveAvailability []struct {
				Scope string `json:"scope"`
				Pace  struct {
					AheadWindowIDs []string `json:"aheadWindowIds"`
				} `json:"pace"`
				Runway struct {
					Status           string `json:"status"`
					LimitingWindowID string `json:"limitingWindowId"`
				} `json:"runway"`
			} `json:"effectiveAvailability"`
		} `json:"quotaSemantics"`
	} `json:"providers"`
}

// parseQuotaAXI turns the JSON model into the compact report agent-net keeps.
func parseQuotaAXI(raw []byte) (*protocol.Quotas, error) {
	var in quotaAXIReport
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("bad JSON: %v", err)
	}
	out := &protocol.Quotas{Providers: []protocol.QuotaProvider{}}
	if t, err := time.Parse(time.RFC3339, in.GeneratedAt); err == nil {
		out.GeneratedAt = t.Unix()
	}
	for _, p := range in.Providers {
		qp := protocol.QuotaProvider{Provider: p.Provider, Plan: p.Plan, Stale: p.State.Stale || strings.EqualFold(p.State.Status, "stale")}
		if p.State.Error != "" {
			qp.Error = p.State.Error
		}
		// Per-window pace facts.
		for _, w := range p.Windows {
			pct, ok := quotaPercent(w.PercentUsed, w.PercentRemaining)
			if !ok {
				continue
			}
			qw := protocol.QuotaWindow{ID: w.ID, Label: w.Label, Kind: w.Kind, PercentUsed: pct}
			if t, err := time.Parse(time.RFC3339, w.ResetsAt); err == nil {
				qw.ResetsAt = t.Unix()
			}
			if w.Pace != nil {
				qw.Pace = w.Pace.Status
			}
			if pct >= 100 {
				qw.Exhausted = true
			}
			qp.Windows = append(qp.Windows, qw)
		}
		// Effective availability says which windows are ahead, and whether a
		// scope is exhausted now even when a raw window hides it.
		for _, e := range p.QuotaSemantics.EffectiveAvailability {
			if strings.EqualFold(e.Runway.Status, "exhausted_now") {
				markQuotaExhausted(&qp, e.Runway.LimitingWindowID)
			}
			for _, id := range e.Pace.AheadWindowIDs {
				markQuotaPace(&qp, id, protocol.QuotaPaceAhead)
			}
		}
		if qp.Windows == nil {
			qp.Windows = []protocol.QuotaWindow{}
		}
		out.Providers = append(out.Providers, qp)
	}
	return out, nil
}

// quotaPercent prefers percentUsed when quota-axi sends it and otherwise
// derives it from percentRemaining. A window with neither is left out.
func quotaPercent(used, remaining *float64) (float64, bool) {
	if used != nil {
		return clampPercent(*used), true
	}
	if remaining != nil {
		return clampPercent(100 - *remaining), true
	}
	return 0, false
}

func clampPercent(v float64) float64 {
	if v != v || v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func markQuotaExhausted(p *protocol.QuotaProvider, id string) {
	for i := range p.Windows {
		if id == "" || p.Windows[i].ID == id {
			p.Windows[i].Exhausted = true
			if p.Windows[i].PercentUsed < 100 {
				p.Windows[i].PercentUsed = 100
			}
			if id != "" {
				return
			}
		}
	}
}

func markQuotaPace(p *protocol.QuotaProvider, id, pace string) {
	for i := range p.Windows {
		if p.Windows[i].ID == id && (p.Windows[i].Pace == "" || p.Windows[i].Pace == protocol.QuotaPaceUnknown) {
			p.Windows[i].Pace = pace
			return
		}
	}
}
