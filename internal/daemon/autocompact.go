package daemon

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

// autoCompactState is one session's auto-compaction guard. One attempt is made
// per idle stretch, whether it succeeds or fails; a busy turn resets it.
type autoCompactState struct {
	attempted bool
	at        int64
	reason    string
	skip      string // last skip reason logged in this idle stretch
}

// autoCompactOnce compacts persistent non-worker sessions on the harnesses
// that expose a compaction command: OpenCode, Codex and pi. Claude has its own
// adapter loop. The CTO OpenCode session has its own loop.
func (d *Daemon) autoCompactOnce(ctx context.Context) {
	snap := d.SettingsSnapshot()
	now := time.Now()
	if d.autoCompactNow != nil {
		now = d.autoCompactNow()
	}
	for _, t := range d.autoCompactTargets() {
		if ctx.Err() != nil {
			return
		}
		d.autoCompactSession(ctx, snap, t, now)
	}
}

type autoCompactTarget struct {
	harness, native, cwd, model, mode, status string
	lastActive                                int64
	worker                                    bool
	conn                                      *localConn
	contextTokens, contextLimit               int64
}

func (d *Daemon) autoCompactTargets() []autoCompactTarget {
	d.cto.mu.Lock()
	ctoSession := d.cto.session
	d.cto.mu.Unlock()
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []autoCompactTarget
	for _, ls := range d.sessions {
		info := d.infoLocked(ls)
		switch info.Harness {
		case "opencode", "codex", "pi":
		default:
			continue
		}
		if info.Worker != nil || (info.Harness == "opencode" && info.NativeID == ctoSession) {
			continue
		}
		out = append(out, autoCompactTarget{
			harness: info.Harness, native: info.NativeID, cwd: info.Cwd, model: info.Model,
			mode: info.Mode, status: info.Status, lastActive: info.LastActive, worker: info.Worker != nil,
			conn: ls.conn, contextTokens: ls.contextTokens, contextLimit: ls.contextLimit,
		})
	}
	return out
}

func (d *Daemon) autoCompactSession(ctx context.Context, snap settings.Snapshot, t autoCompactTarget, now time.Time) {
	key := t.harness + "/" + t.native
	if t.status == "busy" || t.status == "shell" {
		d.autoCompactMu.Lock()
		delete(d.autoCompact, key)
		d.autoCompactMu.Unlock()
		return
	}
	if t.status != "idle" && t.status != protocol.StatusDormant {
		return
	}
	// A foreground client is attached. Codex cannot say whether the CLI or an IDE
	// still holds a thread, so only a thread it reports as background is compacted.
	if t.mode == protocol.ModeForeground || (t.harness == "codex" && t.mode != protocol.ModeBackground) {
		return
	}
	d.autoCompactMu.Lock()
	st := d.autoCompact[key]
	if st == nil {
		st = &autoCompactState{}
		d.autoCompact[key] = st
	}
	attempted := st.attempted
	d.autoCompactMu.Unlock()
	if attempted {
		return
	}
	where := settings.Where{Repo: d.repoFor(t.harness, t.native)}
	threshold := snap.Int("sessions.compaction_threshold_percent", where)
	if threshold <= 0 {
		threshold = 80
	}
	idleEnabled := snap.Bool("sessions.idle_compaction", where)
	minTokens := int64(snap.Int("sessions.idle_compaction_min_tokens", where))
	windows := snap.Map("sessions.cache_window_seconds", where)

	tokens, limit, model, busy, known := d.autoCompactContext(ctx, t)
	if !known || busy {
		return
	}
	if model == "" {
		model = t.model
	}
	fill, hasFill := protocol.ContextFill(tokens, limit)
	idleDue := idleEnabled && t.lastActive > 0 && now.Sub(time.Unix(t.lastActive, 0)) >= protocol.CacheIdleAfter(windows, providerOf(model))
	reason := ""
	if hasFill && tokens*100 >= limit*int64(threshold) {
		reason = fmt.Sprintf("fill %d%%", fill)
	} else if idleDue && tokens >= minTokens {
		reason = "idle"
	}
	if reason == "" {
		if idleDue && tokens < minTokens {
			d.autoCompactSkip(key, "small_context")
		}
		return
	}
	if err := d.autoCompactNowDo(ctx, t, reason); err != nil {
		d.Logf("auto compaction %s %s (%s): %v", t.harness, t.native, reason, err)
		d.autoCompactMu.Lock()
		if s := d.autoCompact[key]; s != nil {
			s.attempted = true
		}
		d.autoCompactMu.Unlock()
		return
	}
	d.autoCompactMu.Lock()
	if s := d.autoCompact[key]; s != nil {
		s.attempted = true
		s.reason = reason
		if t.harness != "pi" {
			s.at = now.Unix()
		}
	}
	d.autoCompactMu.Unlock()
	if t.harness != "pi" {
		d.noteCompacted(t.harness, t.native, now.Unix(), reason)
	}
	d.Logf("auto compaction %s %s: requested (%s)", t.harness, t.native, reason)
}

// autoCompactContext returns the context tokens, the model window and whether
// the session is in a turn. known is false when the harness cannot say.
func (d *Daemon) autoCompactContext(ctx context.Context, t autoCompactTarget) (tokens, limit int64, model string, busy, known bool) {
	switch t.harness {
	case "opencode":
		service, err := d.ownerOpenCodeService()
		if err != nil {
			return 0, 0, "", false, false
		}
		state := d.openCodeSessionContext(ctx, service.URL, service.Password, "", service.identity, t.native)
		busy, known = d.openCodeSessionBusy(ctx, service.URL, service.Password, "", service.identity, t.native)
		if !known {
			return 0, 0, "", false, false
		}
		return state.Tokens, state.Limit, state.Model, busy, true
	case "codex":
		a, ok := d.adapters["codex"].(*codexAdapter)
		if !ok {
			return 0, 0, "", false, false
		}
		th, ok := a.thread(t.native)
		if !ok {
			return 0, 0, "", false, false
		}
		tokens, limit = codexRolloutContext(th.Path)
		return tokens, limit, th.Model, t.status == "busy", true
	case "pi":
		if t.contextTokens <= 0 || t.contextLimit <= 0 {
			return 0, 0, t.model, false, true
		}
		return t.contextTokens, t.contextLimit, t.model, t.status == "busy", true
	}
	return 0, 0, "", false, false
}

func (d *Daemon) autoCompactNowDo(ctx context.Context, t autoCompactTarget, reason string) error {
	switch t.harness {
	case "opencode":
		service, err := d.ownerOpenCodeService()
		if err != nil {
			return err
		}
		return d.compactOpenCodeSession(ctx, service.URL, service.Password, "", service.identity, t.native)
	case "codex":
		a, ok := d.adapters["codex"].(*codexAdapter)
		if !ok {
			return fmt.Errorf("codex adapter is not running")
		}
		return a.compact(ctx, t.native)
	case "pi":
		if t.conn == nil {
			return fmt.Errorf("pi session is not connected")
		}
		return t.conn.writeJSON(map[string]any{"method": "compact", "params": map[string]any{"reason": reason}})
	}
	return fmt.Errorf("compaction is not supported for %s", t.harness)
}

func (d *Daemon) autoCompactSkip(key, reason string) {
	d.autoCompactMu.Lock()
	s := d.autoCompact[key]
	if s == nil {
		s = &autoCompactState{}
		d.autoCompact[key] = s
	}
	same := s.skip == reason
	s.skip = reason
	d.autoCompactMu.Unlock()
	if !same {
		d.Logf("auto compaction %s skipped:%s", key, reason)
	}
}

func (d *Daemon) repoFor(harness, nativeID string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if ls := d.sessions[key(harness, nativeID)]; ls != nil {
		return ls.info.Repo.Project
	}
	return ""
}

func providerOf(model string) string {
	provider, _, ok := strings.Cut(model, "/")
	if ok && provider != "" {
		return provider
	}
	return ""
}

// startAutoCompactionLoop runs the shared loop for OpenCode, Codex and pi.
func (d *Daemon) startAutoCompactionLoop(ctx context.Context) {
	if d.autoCompactEvery <= 0 {
		return
	}
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		ticker := time.NewTicker(d.autoCompactEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				d.autoCompactOnce(ctx)
			}
		}
	}()
}
