package daemon

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/integrations"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

func (d *Daemon) ctoSettings(ctx context.Context, in protocol.CTOSettingsRequest) (protocol.CTOSettings, error) {
	c, ok := d.integrationsClient()
	if !ok {
		return protocol.CTOSettings{}, errNotConnected
	}
	remote, err := readRemoteCTOSettings(ctx, c)
	if err != nil {
		return protocol.CTOSettings{}, err
	}
	if remote.Harness == "" {
		remote.Harness = "opencode"
	}
	remote.Title = protocol.CTOTitleOrDefault(remote.Title)
	title := remote.Title
	if in.Title != nil {
		normalized, err := protocol.NormalizeCTOTitle(*in.Title)
		if err != nil {
			return protocol.CTOSettings{}, err
		}
		title = protocol.CTOTitleOrDefault(normalized)
	}
	if err := d.migrateCTOModelSettings(remote); err != nil {
		return protocol.CTOSettings{}, err
	}
	openCodeModel, claudeModel, harness, err := d.localCTOModelSettings()
	if err != nil {
		return protocol.CTOSettings{}, err
	}
	models, err := d.ctoRoleModels(ctx)
	if err != nil {
		return protocol.CTOSettings{}, err
	}
	previousHarness := harness
	if in.Harness != nil {
		if *in.Harness != "opencode" && *in.Harness != "claude" {
			return protocol.CTOSettings{}, fmt.Errorf("bad CTO harness %q", *in.Harness)
		}
		harness = *in.Harness
	}
	if in.Models != nil {
		models = *in.Models
		if len(models) > 0 {
			harness = models[0].Harness
		}
	}
	if in.Model != nil || in.Harness != nil {
		// The form changes the current choice, which is the first entry. The
		// rest of the ordered list is kept, so a fallback survives an edit.
		if len(models) == 0 {
			models = []protocol.RoleModel{{Harness: harness}}
		}
		models[0].Harness = harness
		switch {
		case in.Model != nil:
			models[0].Model = *in.Model
		case in.Harness != nil:
			// Switching harness without naming a model keeps the model that
			// harness had, as the per-harness settings did.
			models[0].Model = modelForCTOHarness(harness, openCodeModel, claudeModel)
		}
		switch harness {
		case "claude":
			claudeModel = models[0].Model
		default:
			openCodeModel = models[0].Model
		}
	}
	switched := in.Harness != nil && previousHarness != harness
	if switched {
		if err := d.snapshotCTOHarness(ctx, previousHarness); err != nil {
			return protocol.CTOSettings{}, fmt.Errorf("save CTO history before switching harness: %w", err)
		}
	}

	if in.Model != nil || in.Harness != nil || in.Models != nil {
		model := ""
		if len(models) > 0 {
			model = models[0].Model
		}
		if err := d.saveCTORoleModels(models, harness, model); err != nil {
			return protocol.CTOSettings{}, err
		}
	}
	if switched {
		model := modelForCTOHarness(harness, openCodeModel, claudeModel)
		if model == "" {
			model = roleModelForHarness(models, harness)
		}
		if model == "" {
			model = "default"
		}
		payload, err := json.Marshal(model)
		if err != nil {
			return protocol.CTOSettings{}, err
		}
		if _, err := d.st.db.Exec(`INSERT INTO cto_history(harness,kind,at,payload) VALUES(?,'switch',?,?)`, harness, time.Now().UnixNano(), string(payload)); err != nil {
			return protocol.CTOSettings{}, err
		}
	}
	if in.Model != nil || in.Harness != nil || in.Title != nil || in.Models != nil {
		remote.Model = modelForCTOHarness(harness, openCodeModel, claudeModel)
		remote.Harness = harness
		remote.OpenCodeModel = openCodeModel
		remote.ClaudeModel = claudeModel
		remote.Models = models
		remote.Title = title
		if err := writeRemoteCTOSettings(ctx, c, remote); err != nil {
			return protocol.CTOSettings{}, err
		}
	}

	out := remote
	out.Harness = harness
	out.OpenCodeModel = openCodeModel
	out.ClaudeModel = claudeModel
	out.Model = modelForCTOHarness(harness, openCodeModel, claudeModel)
	if out.Model == "" {
		out.Model = roleModelForHarness(models, harness)
	}
	out.Models = models
	out.Title = title
	return out, nil
}

func readRemoteCTOSettings(ctx context.Context, c integrations.Client) (protocol.CTOSettings, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.URL, "/")+"/v1/cto-settings", nil)
	if err != nil {
		return protocol.CTOSettings{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return protocol.CTOSettings{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return protocol.CTOSettings{}, fmt.Errorf("gateway answered %s", resp.Status)
	}
	var out protocol.CTOSettings
	err = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&out)
	return out, err
}

func writeRemoteCTOSettings(ctx context.Context, c integrations.Client, settings protocol.CTOSettings) error {
	b, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, strings.TrimRight(c.URL, "/")+"/v1/cto-settings", bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("gateway answered %s", resp.Status)
	}
	return nil
}

func (d *Daemon) migrateCTOModelSettings(legacy protocol.CTOSettings) error {
	tx, err := d.st.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var migrated int
	var oldOpenCodeModel string
	err = tx.QueryRow(`SELECT models_migrated, opencode_model FROM cto WHERE id=1`).Scan(&migrated, &oldOpenCodeModel)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if migrated != 0 {
		return tx.Commit()
	}
	model := legacy.Model
	if model == "" {
		model = oldOpenCodeModel
	}
	openCodeModel, claudeModel := "", ""
	provider, id, hasProvider := strings.Cut(model, "/")
	if hasProvider && provider != "" && id != "" {
		openCodeModel = model
	} else {
		claudeModel = model
	}
	harness := legacy.Harness
	if harness != "claude" && harness != "opencode" {
		harness = "opencode"
	}
	// The ordered list migrates in the same step (decision 0162). A saved
	// model becomes a one-entry list; a fresh install takes the setting's
	// default list.
	models := protocol.FormatRoleModels(legacy.Models)
	if models == "" && model != "" {
		models = protocol.FormatRoleModels([]protocol.RoleModel{{Harness: harness, Model: model}})
	}
	if models == "" {
		models = d.SettingsSnapshot().String(protocol.RoleModelsKey(protocol.RoleCTO), settings.Where{})
	}
	_, err = tx.Exec(`INSERT INTO cto(id,session_id,active_harness,active_model,opencode_model,claude_model,models,models_migrated)
VALUES(1,'',?,?,?,?,?,1)
ON CONFLICT(id) DO UPDATE SET active_harness=excluded.active_harness,active_model=excluded.active_model,opencode_model=excluded.opencode_model,claude_model=excluded.claude_model,models=excluded.models,models_migrated=1`, harness, model, openCodeModel, claudeModel, models)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (d *Daemon) localCTOModelSettings() (openCodeModel, claudeModel, harness string, err error) {
	err = d.st.db.QueryRow(`SELECT opencode_model,claude_model,active_harness FROM cto WHERE id=1`).Scan(&openCodeModel, &claudeModel, &harness)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", "opencode", nil
	}
	if err == nil && harness == "" {
		harness = "opencode"
	}
	return
}

// roleModelForHarness is the first list entry for one harness, when the saved
// per-harness model is empty.
func roleModelForHarness(models []protocol.RoleModel, harness string) string {
	for _, m := range models {
		if m.Harness == harness {
			return m.Model
		}
	}
	return ""
}

func modelForCTOHarness(harness, openCodeModel, claudeModel string) string {
	if harness == "claude" {
		return claudeModel
	}
	return openCodeModel
}
