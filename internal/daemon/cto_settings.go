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
	if err := d.migrateCTOModelSettings(remote); err != nil {
		return protocol.CTOSettings{}, err
	}
	openCodeModel, claudeModel, harness, err := d.localCTOModelSettings()
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
	if in.Model != nil {
		switch harness {
		case "claude":
			claudeModel = *in.Model
		default:
			openCodeModel = *in.Model
		}
	}
	compaction := remote.Compaction.Normalized()
	if in.Compaction != nil {
		compaction = in.Compaction.Normalized()
	}
	switched := in.Harness != nil && previousHarness != harness
	if switched {
		if err := d.snapshotCTOHarness(ctx, previousHarness); err != nil {
			return protocol.CTOSettings{}, fmt.Errorf("save CTO history before switching harness: %w", err)
		}
	}

	if in.Model != nil || in.Harness != nil {
		if _, err := d.st.db.Exec(`UPDATE cto SET opencode_model=?, claude_model=?, active_harness=? WHERE id=1`, openCodeModel, claudeModel, harness); err != nil {
			return protocol.CTOSettings{}, err
		}
	}
	if switched {
		model := modelForCTOHarness(harness, openCodeModel, claudeModel)
		if model == "" {
			model = "default"
			if harness == "claude" {
				model = "opus"
			}
		}
		payload, err := json.Marshal(model)
		if err != nil {
			return protocol.CTOSettings{}, err
		}
		if _, err := d.st.db.Exec(`INSERT INTO cto_history(harness,kind,at,payload) VALUES(?,'switch',?,?)`, harness, time.Now().UnixNano(), string(payload)); err != nil {
			return protocol.CTOSettings{}, err
		}
	}
	if in.Model != nil || in.Harness != nil || in.Compaction != nil {
		remote.Model = modelForCTOHarness(harness, openCodeModel, claudeModel)
		remote.Harness = harness
		remote.OpenCodeModel = openCodeModel
		remote.ClaudeModel = claudeModel
		remote.Compaction = compaction
		if err := writeRemoteCTOSettings(ctx, c, remote); err != nil {
			return protocol.CTOSettings{}, err
		}
	}

	out := remote
	out.Harness = harness
	out.OpenCodeModel = openCodeModel
	out.ClaudeModel = claudeModel
	out.Model = modelForCTOHarness(harness, openCodeModel, claudeModel)
	out.Compaction = compaction
	d.cacheCTOSettings(out)
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
	_, err = tx.Exec(`INSERT INTO cto(id,session_id,active_harness,opencode_model,claude_model,models_migrated)
VALUES(1,'',?,?,?,1)
ON CONFLICT(id) DO UPDATE SET active_harness=excluded.active_harness,opencode_model=excluded.opencode_model,claude_model=excluded.claude_model,models_migrated=1`, harness, openCodeModel, claudeModel)
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

func modelForCTOHarness(harness, openCodeModel, claudeModel string) string {
	if harness == "claude" {
		return claudeModel
	}
	return openCodeModel
}
