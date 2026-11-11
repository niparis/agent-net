package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/harnesses"
	"github.com/niparis/agent-net/internal/protocol"
)

// ModelListTTL bounds how long a live harness model read is reused. The scan
// that used to run every six hours is gone: this cache only makes a burst of
// calls (a profile add, then a picker) share one harness run.
const ModelListTTL = 5 * time.Minute

type modelCacheEntry struct {
	models  []string
	fetched time.Time
}

// listModelsLive asks the harness itself for its models now. One read per
// harness is shared by every caller inside ModelListTTL. A harness that is not
// installed, or a read that fails, gives an empty list and the harness error.
func (d *Daemon) listModelsLive(ctx context.Context, harness, filter string) protocol.ModelListResult {
	out := protocol.ModelListResult{Harnesses: []protocol.HarnessModels{}}
	names := []string{harness}
	if harness == "" {
		list, _ := d.inventory()
		names = names[:0]
		for _, h := range list {
			names = append(names, h.Name)
		}
	}
	for _, name := range names {
		models, cached, err := d.modelsForHarness(ctx, name)
		if filter != "" {
			kept := models[:0]
			for _, id := range models {
				if strings.Contains(strings.ToLower(id), strings.ToLower(filter)) {
					kept = append(kept, id)
				}
			}
			models = kept
		}
		row := protocol.HarnessModels{Name: name, Models: models, Cached: cached}
		if err != nil {
			row.Error = err.Error()
		}
		out.Harnesses = append(out.Harnesses, row)
	}
	out.FetchedAt = time.Now().Unix()
	return out
}

func (d *Daemon) modelsForHarness(ctx context.Context, name string) ([]string, bool, error) {
	d.modelMu.Lock()
	if e, ok := d.modelCache[name]; ok && time.Since(e.fetched) < ModelListTTL {
		models := append([]string(nil), e.models...)
		d.modelMu.Unlock()
		return models, true, nil
	}
	d.modelMu.Unlock()

	models, err := d.readHarnessModels(ctx, name)
	if err != nil {
		return nil, false, err
	}
	if len(models) == 0 {
		// A failed or empty read is never cached: the next call asks the
		// harness again, so a login or quota that was just fixed is seen.
		return nil, false, nil
	}
	d.modelMu.Lock()
	if d.modelCache == nil {
		d.modelCache = map[string]modelCacheEntry{}
	}
	d.modelCache[name] = modelCacheEntry{models: append([]string(nil), models...), fetched: time.Now()}
	d.modelMu.Unlock()
	return models, false, nil
}

func (d *Daemon) readHarnessModels(ctx context.Context, name string) ([]string, error) {
	if d.cfg.ListModels != nil {
		return d.cfg.ListModels(ctx, name)
	}
	if name == "claude" {
		return harnesses.ClaudeModels(), nil
	}
	path, err := d.harnessBinary(name)
	if err != nil {
		return nil, err
	}
	opts := harnesses.Options{}
	if d.cfg.HarnessSearch != nil {
		opts = *d.cfg.HarnessSearch
	}
	return harnesses.ListModels(ctx, name, path, opts)
}

// explainStartFailure adds the harness's own error and one live model lookup
// to a failed start, so the owner learns why the model did not run.
func (d *Daemon) explainStartFailure(ctx context.Context, harness, model string, err error) error {
	if err == nil {
		return err
	}
	base := protocol.ParseModelSelection(harness, "", model, "").Model
	live := d.listModelsLive(ctx, harness, base)
	var detail string
	if len(live.Harnesses) > 0 {
		row := live.Harnesses[0]
		switch {
		case row.Error != "":
			detail = fmt.Sprintf("live %s model lookup failed: %s", harness, row.Error)
		case len(row.Models) == 0:
			detail = fmt.Sprintf("live %s model lookup: no model matches %q", harness, base)
		default:
			shown := row.Models
			if len(shown) > 5 {
				shown = shown[:5]
			}
			detail = fmt.Sprintf("live %s model lookup: %d match %q: %s", harness, len(row.Models), base, strings.Join(shown, ", "))
		}
	}
	if detail == "" {
		return err
	}
	return fmt.Errorf("%w; %s", err, detail)
}

// modelTestArgs builds one small test turn. The effort is built through
// protocol.ModelSelection.Arguments, the same helper each worker launcher uses
// (worker_claude.go, worker_opencode.go, worker_codex.go): no harness takes it
// glued to the model id.
func modelTestArgs(harness, model, effort string) ([]string, error) {
	selection := protocol.ParseModelSelection(harness, "", model, effort)
	switch harness {
	case "claude":
		modelArgs, err := selection.Arguments(nil)
		if err != nil {
			return nil, err
		}
		return append([]string{"-p"}, append(modelArgs, "Reply with OK.")...), nil
	case "opencode":
		modelArgs, err := selection.Arguments(nil)
		if err != nil {
			return nil, err
		}
		// The launcher sends the effort as the session variant, and
		// `opencode run` takes that as --variant.
		args := []string{"run", "--model", modelArgs[0]}
		if modelArgs[1] != "" {
			args = append(args, "--variant", modelArgs[1])
		}
		return append(args, "Reply with OK."), nil
	case "codex":
		modelArgs, err := selection.Arguments(nil)
		if err != nil {
			return nil, err
		}
		// The launcher splits "model:effort" and sends the effort on
		// turn/start; `codex exec` takes it as its reasoning-effort config.
		name, level := splitCodexModel(modelArgs[0])
		args := []string{"exec", "--model", name, "--skip-git-repo-check"}
		if level != "" {
			args = append(args, "-c", "model_reasoning_effort="+strconv.Quote(level))
		}
		return append(args, "Reply with OK."), nil
	}
	return nil, fmt.Errorf("a test start is not supported for %s", harness)
}

// testModel runs one small harness turn with a model. It is the optional real
// test of a profile add. The output is the harness's own, trimmed.
func (d *Daemon) testModel(ctx context.Context, p protocol.ModelTestRequest) protocol.ModelTestResult {
	path, err := d.harnessBinary(p.Harness)
	if err != nil {
		return protocol.ModelTestResult{Output: err.Error()}
	}
	args, err := modelTestArgs(p.Harness, p.Model, p.Effort)
	if err != nil {
		return protocol.ModelTestResult{Output: err.Error()}
	}
	cctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, path, args...)
	cmd.Dir = os.TempDir()
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if len(text) > 2000 {
		text = "..." + text[len(text)-2000:]
	}
	res := protocol.ModelTestResult{OK: err == nil && cctx.Err() == nil, Output: text}
	if err != nil && text == "" {
		res.Output = err.Error()
	}
	if cctx.Err() == context.DeadlineExceeded {
		res.Output = "the test start timed out; " + text
	}
	return res
}

// onModelsFrame answers FrameNodeModels from the gateway.
func (d *Daemon) onModelsFrame(ctx context.Context, l *link, h protocol.Header, raw []byte) {
	var p protocol.ModelListRequest
	var data any
	var err error
	if err = json.Unmarshal(raw, &p); err == nil {
		data = d.listModelsLive(ctx, p.Harness, p.Filter)
	}
	if res, eerr := protocol.EncodeResult(h.ID, data, err); eerr == nil {
		l.write(ctx, res)
	}
}
