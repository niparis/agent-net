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
	perHarness, harness, err := d.localCTOModelSettings()
	if err != nil {
		return protocol.CTOSettings{}, err
	}
	models, err := d.ctoRoleModels(ctx)
	if err != nil {
		return protocol.CTOSettings{}, err
	}
	previousHarness := harness
	if in.Harness != nil {
		if err := protocol.CheckCTOHarness(*in.Harness); err != nil {
			return protocol.CTOSettings{}, err
		}
		harness = *in.Harness
	}
	if in.Models != nil {
		models = *in.Models
		if err := checkCTOModelList(models); err != nil {
			return protocol.CTOSettings{}, err
		}
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
			models[0].Model = strings.TrimSpace(*in.Model)
		case in.Harness != nil:
			// Switching harness without naming a model keeps the model that
			// harness had, as the per-harness settings did.
			models[0].Model = perHarness.of(harness)
		}
		if models[0].Model == "" {
			// A switch with no model named takes the harness's own default,
			// never the model or provider of the harness it leaves.
			live, _ := d.readHarnessModels(ctx, harness)
			models[0].Model = defaultRoleModel(harness, live)
		}
		if models[0].Model == "" {
			models[0].Provider = ""
		} else {
			models[0].Provider = protocol.ProfileProvider(harness, models[0].Model)
		}
		perHarness.set(harness, models[0].Model)
	}
	switched := in.Harness != nil && previousHarness != harness
	if switched {
		// Only a harness that was running can answer the handover. The timeline
		// read below starts an idle one, so the state is read first.
		oldRunning := d.ctoHarnessRunning(previousHarness)
		// The timeline is saved first, so the owner's view is the old one as it
		// was. The handover comes after: its exchange with the old conversation
		// is not part of what the owner saw (decision 0171).
		if err := d.snapshotCTOHarness(ctx, previousHarness); err != nil {
			return protocol.CTOSettings{}, fmt.Errorf("save CTO history before switching harness: %w", err)
		}
		if err := d.ctoHandover(ctx, previousHarness, harness, true, oldRunning); err != nil {
			return protocol.CTOSettings{}, fmt.Errorf("write the CTO handover before switching harness: %w", err)
		}
		if err := d.ctoSwitchConversation(harness); err != nil {
			return protocol.CTOSettings{}, err
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
		if switched {
			// The setting the owner reads must name the CTO that runs now
			// (decision 0162), so the switch writes its effective entry first.
			if err := d.writeCTOModelsSetting(ctx, models); err != nil {
				return protocol.CTOSettings{}, fmt.Errorf("record the CTO harness switch in cto.models: %w", err)
			}
		}
	}
	if switched {
		model := perHarness.of(harness)
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
		remote.Model = perHarness.of(harness)
		remote.Harness = harness
		remote.OpenCodeModel = perHarness.OpenCode
		remote.ClaudeModel = perHarness.Claude
		remote.CodexModel = perHarness.Codex
		remote.Models = models
		remote.Title = title
		if err := writeRemoteCTOSettings(ctx, c, remote); err != nil {
			return protocol.CTOSettings{}, err
		}
	}

	out := remote
	out.Harness = harness
	out.OpenCodeModel = perHarness.OpenCode
	out.ClaudeModel = perHarness.Claude
	out.CodexModel = perHarness.Codex
	out.Model = perHarness.of(harness)
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
	if protocol.CheckCTOHarness(harness) != nil {
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

// ctoHarnessModels is the model each CTO harness keeps, so a switch back to a
// harness keeps the model it had.
type ctoHarnessModels struct{ OpenCode, Claude, Codex string }

func (m ctoHarnessModels) of(harness string) string {
	switch harness {
	case "claude":
		return m.Claude
	case "codex":
		return m.Codex
	}
	return m.OpenCode
}

func (m *ctoHarnessModels) set(harness, model string) {
	switch harness {
	case "claude":
		m.Claude = model
	case "codex":
		m.Codex = model
	default:
		m.OpenCode = model
	}
}

// checkCTOModelList refuses a list of Pi entries only, with the Pi reason. The
// gate drops a Pi entry from a mixed list with the same reason, and refuses
// other entries this machine cannot run when the CTO starts (decision 0171).
func checkCTOModelList(models []protocol.RoleModel) error {
	for _, m := range models {
		if m.Harness != "pi" {
			return nil
		}
	}
	if len(models) > 0 {
		return errors.New(protocol.PiRoleReason)
	}
	return nil
}

func (d *Daemon) localCTOModelSettings() (models ctoHarnessModels, harness string, err error) {
	err = d.st.db.QueryRow(`SELECT opencode_model,claude_model,codex_model,active_harness FROM cto WHERE id=1`).Scan(&models.OpenCode, &models.Claude, &models.Codex, &harness)
	if errors.Is(err, sql.ErrNoRows) {
		return ctoHarnessModels{}, "opencode", nil
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
