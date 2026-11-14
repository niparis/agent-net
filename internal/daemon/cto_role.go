package daemon

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

// ctoRoleModels is the CTO's ordered model list: a setting on a fresh
// install, and today's active harness and model migrated into a one-entry
// list on an install that already had them (decision 0162). An entry with no
// model means that harness's own default, which is read live.
func (d *Daemon) ctoRoleModels(ctx context.Context) ([]protocol.RoleModel, error) {
	var raw, harness, activeModel, openCodeModel, claudeModel string
	err := d.st.db.QueryRow(`SELECT models, active_harness, active_model, opencode_model, claude_model FROM cto WHERE id=1`).
		Scan(&raw, &harness, &activeModel, &openCodeModel, &claudeModel)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// A fresh install: the setting is the default list.
		return d.roleModelsFromSetting(ctx, protocol.RoleCTO)
	case err != nil:
		return nil, err
	case raw != "":
		models, perr := protocol.ParseRoleModels(raw)
		if perr != nil {
			return nil, fmt.Errorf("cto models: %w", perr)
		}
		return d.resolveRoleModels(ctx, models), nil
	default:
		// Today's value, as a one-entry list.
		if harness == "" {
			harness = "opencode"
		}
		model := activeModel
		if model == "" {
			model = modelForCTOHarness(harness, openCodeModel, claudeModel)
		}
		return d.resolveRoleModels(ctx, []protocol.RoleModel{{Harness: harness, Model: model}}), nil
	}
}

// roleModelsFromSetting reads one role's list from the settings recipe and
// resolves an entry that names no model.
func (d *Daemon) roleModelsFromSetting(ctx context.Context, role string) ([]protocol.RoleModel, error) {
	key := protocol.RoleModelsKey(role)
	if key == "" {
		return nil, fmt.Errorf("unknown role %q", role)
	}
	models, err := protocol.ParseRoleModels(d.SettingsSnapshot().String(key, settings.Where{}))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", key, err)
	}
	return d.resolveRoleModels(ctx, models), nil
}

// resolveRoleModels fills each entry's model and provider so the gate checks
// the provider the start will really draw on. An entry that names no model
// uses the harness's own default, read live, as the CTO did before.
func (d *Daemon) resolveRoleModels(ctx context.Context, models []protocol.RoleModel) []protocol.RoleModel {
	out := make([]protocol.RoleModel, 0, len(models))
	byHarness := map[string][]string{}
	for _, m := range models {
		if m.IsZero() {
			continue
		}
		if m.Model == "" {
			live, ok := byHarness[m.Harness]
			if !ok {
				live, _ = d.readHarnessModels(ctx, m.Harness)
				byHarness[m.Harness] = live
			}
			m.Model = defaultRoleModel(m.Harness, live)
		}
		if m.Provider == "" && m.Model != "" {
			m.Provider = protocol.ProfileProvider(m.Harness, m.Model)
		}
		out = append(out, m)
	}
	return out
}

// defaultRoleModel is a harness's own default model: the queries the CTO used
// for OpenCode, the usual opus for Claude, else the first model listed.
func defaultRoleModel(harness string, live []string) string {
	if len(live) == 0 {
		return ""
	}
	queries := []string(nil)
	switch harness {
	case "opencode":
		queries = []string{"gpt 6.1", "opus 5.5", "deepseek v4 pro"}
	case "claude":
		queries = []string{"opus"}
	}
	for _, q := range queries {
		if model, err := protocol.ResolveModel(q, live, nil, nil); err == nil {
			return model
		}
	}
	return live[0]
}

// saveCTORoleModels writes the ordered list and the current choice. The two
// per-harness columns stay for the OpenCode and Claude runtimes.
func (d *Daemon) saveCTORoleModels(models []protocol.RoleModel, harness, model string) error {
	raw := protocol.FormatRoleModels(models)
	openCodeModel, claudeModel := "", ""
	if harness == "opencode" {
		openCodeModel = model
	}
	if harness == "claude" {
		claudeModel = model
	}
	_, err := d.st.db.Exec(`UPDATE cto SET models=?, active_harness=?, active_model=?, opencode_model=CASE WHEN ?='opencode' THEN ? ELSE opencode_model END, claude_model=CASE WHEN ?='claude' THEN ? ELSE claude_model END WHERE id=1`,
		raw, harness, model, harness, openCodeModel, harness, claudeModel)
	return err
}

// ctoCurrentRoleModel is the model the CTO runs now, for the resume check.
func ctoCurrentRoleModel(harness, activeModel, openCodeModel, claudeModel string) protocol.RoleModel {
	if harness == "" {
		harness = "opencode"
	}
	if activeModel == "" {
		activeModel = modelForCTOHarness(harness, openCodeModel, claudeModel)
	}
	return protocol.RoleModel{Harness: harness, Model: activeModel}
}

// ctoPick asks the one gate which CTO model may start. The CTO is a role, so
// it gets the locked checks and cooling only (decision 0162).
func (d *Daemon) ctoPick(ctx context.Context, models []protocol.RoleModel, current protocol.RoleModel, resume bool) (protocol.RolePickResult, error) {
	return d.pickRoleOnMachine(ctx, protocol.RoleCTO, "", models, current, resume)
}
