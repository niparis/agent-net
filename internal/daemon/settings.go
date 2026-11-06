package daemon

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

// SettingsSnapshot returns a detached view. Call once per unit of work.
func (d *Daemon) SettingsSnapshot() settings.Snapshot {
	if p := d.settingsSnapshot.Load(); p != nil {
		return p.Clone()
	}
	return settings.Snapshot{Recipe: settings.RecipeHash(), Maturity: map[string]string{}}
}
func (d *Daemon) loadSettingsSnapshot() error {
	var raw string
	err := d.st.db.QueryRow(`SELECT payload FROM settings_snapshot WHERE id=1`).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	snap, err := d.cleanSettingsSnapshot([]byte(raw))
	if err != nil {
		if d.log != nil {
			d.log.Printf("settings snapshot: %v", err)
		}
		return nil
	}
	d.settingsSnapshot.Store(&snap)
	return nil
}
func (d *Daemon) cleanSettingsSnapshot(raw []byte) (settings.Snapshot, error) {
	var wire struct {
		Revision                *int64                        `json:"revision"`
		Recipe                  string                        `json:"recipe"`
		DeliveryCutover         bool                          `json:"delivery_cutover"`
		DeliveryPipelineCutover bool                          `json:"delivery_pipeline_cutover"`
		DeliveryAssignments     []settings.DeliveryAssignment `json:"delivery_assignments"`
		WorkerSandboxCutover    bool                          `json:"worker_sandbox_cutover"`
		Values                  []settings.Value              `json:"values"`
		Maturity                map[string]string             `json:"maturity"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return settings.Snapshot{}, err
	}
	if wire.Revision == nil || *wire.Revision < 0 {
		return settings.Snapshot{}, fmt.Errorf("invalid revision")
	}
	snap := settings.Snapshot{Revision: *wire.Revision, Recipe: wire.Recipe, DeliveryCutover: wire.DeliveryCutover, DeliveryPipelineCutover: wire.DeliveryPipelineCutover, WorkerSandboxCutover: wire.WorkerSandboxCutover, Values: []settings.Value{}, Maturity: wire.Maturity}
	for _, a := range wire.DeliveryAssignments {
		ref := protocol.DeliveryEdgeRef(a.Kind)
		if a.Project != "" && a.Node != "" && ref.Step == a.Step && ref.For == a.For && ref.AfterMerge == a.AfterMerge && a.Step != "" {
			snap.DeliveryAssignments = append(snap.DeliveryAssignments, a)
		}
	}
	recipe := settings.Standard()
	seen := map[string]bool{}
	for _, v := range wire.Values {
		def, ok := recipe.Definition(v.Key)
		allowed := false
		for _, level := range def.Levels {
			if level == v.Level {
				allowed = true
			}
		}
		id := v.Key + "\x00" + v.Level + "\x00" + v.Scope + "\x00" + v.Item
		if !ok || !allowed || seen[id] || v.Item != "" || (v.Level != "network" && v.Level != "repo") || (v.Level == "network" && v.Scope != "network") || (v.Level == "repo" && v.Scope == "") {
			if d.log != nil {
				d.log.Printf("settings: ignoring unknown or invalid address %s", v.Key)
			}
			continue
		}
		b, err := def.Codec(v.Value)
		if err != nil {
			if d.log != nil {
				d.log.Printf("settings: ignoring invalid %s: %v", v.Key, err)
			}
			continue
		}
		v.Value = b
		seen[id] = true
		snap.Values = append(snap.Values, v)
	}
	return snap, nil
}
func (d *Daemon) acceptSettingsSnapshot(raw []byte, first bool) error {
	snap, err := d.cleanSettingsSnapshot(raw)
	if err != nil {
		return err
	}
	d.settingsMu.Lock()
	defer d.settingsMu.Unlock()
	if old := d.settingsSnapshot.Load(); old != nil && !first && snap.Revision < old.Revision {
		return nil
	}
	b, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	_, err = d.st.db.Exec(`INSERT INTO settings_snapshot(id,revision,payload,received_at) VALUES(1,?,?,?) ON CONFLICT(id) DO UPDATE SET revision=excluded.revision,payload=excluded.payload,received_at=excluded.received_at`, snap.Revision, string(b), time.Now().UnixNano())
	if err != nil {
		return err
	}
	old := d.SettingsSnapshot()
	d.settingsSnapshot.Store(&snap)
	if d.cfg.RefreshAgentFiles != nil && guidanceChanged(old, snap) {
		d.cfg.RefreshAgentFiles(d.Logf, snap.Clone())
	}
	return nil
}

func guidanceChanged(old, next settings.Snapshot) bool {
	for _, def := range settings.Standard().Definitions {
		if !strings.HasPrefix(def.Key, "guidance.") || !def.Live {
			continue
		}
		a, errA := old.Resolve(def.Key, settings.Where{})
		b, errB := next.Resolve(def.Key, settings.Where{})
		if errA != nil || errB != nil || !bytes.Equal(a.Value, b.Value) {
			return true
		}
	}
	return false
}

func (d *Daemon) readSettings(ctx context.Context, q protocol.SettingsReadRequest, owner bool) ([]protocol.SettingView, error) {
	var target *protocol.SettingsRef
	if q.Target != nil {
		if q.Repo != "" {
			return nil, fmt.Errorf("settings target and repo cannot be combined")
		}
		raw, err := d.call(ctx, "settings.target", *q.Target)
		if err != nil {
			return nil, err
		}
		var resolution protocol.SettingsTargetResolution
		if err = json.Unmarshal(raw, &resolution); err != nil {
			return nil, err
		}
		if resolution.Target == nil {
			if len(resolution.Choices) > 0 {
				return nil, ambiguousSettingsTarget(*q.Target, resolution.Choices)
			}
			return nil, fmt.Errorf("unknown %s name %q", q.Target.Level, q.Target.Name)
		}
		target = resolution.Target
	}
	if latest, e := d.call(ctx, "settings.snapshot", nil); e == nil {
		if e = d.acceptSettingsSnapshot(latest, false); e != nil {
			return nil, e
		}
	}
	raw, err := d.call(ctx, "settings.history", map[string]any{"key": q.Key, "owner": owner})
	if err != nil {
		return nil, err
	}
	var history []protocol.SettingsHistory
	if err := json.Unmarshal(raw, &history); err != nil {
		return nil, err
	}
	return d.workerSettingsViewsAt(d.SettingsSnapshot(), history, q, owner, target)
}

// ambiguousSettingsTarget names every typed candidate and never picks one.
func ambiguousSettingsTarget(t protocol.SettingsTarget, choices []protocol.SettingsTargetChoice) error {
	parts := make([]string, 0, len(choices))
	for _, c := range choices {
		parts = append(parts, fmt.Sprintf("%s %s (id %s)", c.Ref.Kind, c.Name, c.Ref.ID))
	}
	return fmt.Errorf("%s name %q is ambiguous: %s", t.Level, t.Name, strings.Join(parts, ", "))
}

func (d *Daemon) settingsRevisionHasRule(ctx context.Context, rev int64) (bool, error) {
	if rev <= 0 {
		return false, fmt.Errorf("revision required")
	}
	raw, err := d.call(ctx, "settings.history", map[string]any{"revision": rev, "owner": true})
	if err != nil {
		return false, err
	}
	var history []protocol.SettingsHistory
	if err = json.Unmarshal(raw, &history); err != nil {
		return false, err
	}
	if len(history) == 0 {
		return false, fmt.Errorf("no settings history for revision %d", rev)
	}
	hasRule := false
	recipe := settings.Standard()
	for _, h := range history {
		def, ok := recipe.Definition(h.Key)
		if !ok || def.Visibility == "owner" {
			return false, fmt.Errorf("owner preference is unavailable to CTO")
		}
		hasRule = hasRule || def.Kind != "advice"
	}
	return hasRule, nil
}

func validateSettingsWrite(q protocol.SettingsWrite) (bool, error) {
	if len(q.Changes) == 0 {
		return false, fmt.Errorf("changes required")
	}
	recipe := settings.Standard()
	seen := map[[4]string]bool{}
	rule := false
	for _, c := range q.Changes {
		def, ok := recipe.Definition(c.Key)
		if !ok {
			return false, fmt.Errorf("unknown setting %s", c.Key)
		}
		if def.Visibility == "owner" {
			return false, fmt.Errorf("owner preference is unavailable to CTO")
		}
		if def.Kind == "rule" {
			rule = true
		}
		allowed := false
		for _, level := range def.Levels {
			if level == c.Level {
				allowed = true
			}
		}
		if c.Item != "" || (c.Level != "network" && c.Level != "repo") || !allowed || (c.Level == "network" && c.Scope != "network") || (c.Level == "repo" && c.Scope == "") {
			return false, fmt.Errorf("invalid setting address")
		}
		address := [4]string{c.Key, c.Level, c.Scope, c.Item}
		if seen[address] {
			return false, fmt.Errorf("duplicate setting address")
		}
		seen[address] = true
		if c.Expect != nil && !bytes.Equal(bytes.TrimSpace(c.Expect), []byte("null")) {
			if _, err := def.CodecExpected(c.Expect); err != nil {
				return false, fmt.Errorf("invalid expected value: %w", err)
			}
		}
		if c.Delete {
			if c.Value != nil {
				return false, fmt.Errorf("delete with value")
			}
		} else if _, err := def.Codec(c.Value); err != nil {
			return false, err
		}
	}
	return rule, nil
}

func (d *Daemon) routeCTOSettings(ctx context.Context, raw json.RawMessage) (any, error) {
	var q struct {
		Area     string                    `json:"area"`
		Changes  []protocol.SettingsChange `json:"changes"`
		Revision int64                     `json:"revision"`
		Reason   string                    `json:"reason"`
	}
	if err := json.Unmarshal(raw, &q); err != nil {
		return nil, err
	}
	var rule bool
	var err error
	switch q.Area {
	case "values_write":
		rule, err = validateSettingsWrite(protocol.SettingsWrite{Changes: q.Changes, Reason: q.Reason})
	case "values_undo":
		rule, err = d.settingsRevisionHasRule(ctx, q.Revision)
	default:
		return routeCTOTool(ctx, "settings_change", raw, d.Propose, d.executeCTOTool)
	}
	if err != nil {
		return nil, err
	}
	if rule {
		return routeCTOTool(ctx, "settings_change", raw, d.Propose, d.executeCTOTool)
	}
	concrete, args, _, err := ctoConcreteCall("settings_change", raw)
	if err != nil {
		return nil, err
	}
	return d.executeCTOTool(context.WithValue(ctx, proposalActorKey{}, "cto"), concrete, args)
}
