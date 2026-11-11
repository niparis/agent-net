package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
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
	return harnesses.ListModels(ctx, name, path, opts), nil
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

// testModel runs one small harness turn with a model. It is the optional real
// test of a profile add. The output is the harness's own, trimmed.
func (d *Daemon) testModel(ctx context.Context, p protocol.ModelTestRequest) protocol.ModelTestResult {
	path, err := d.harnessBinary(p.Harness)
	if err != nil {
		return protocol.ModelTestResult{Output: err.Error()}
	}
	var args []string
	model := p.Model
	if p.Effort != "" {
		model += ":" + p.Effort
	}
	switch p.Harness {
	case "claude":
		args = []string{"-p", "--model", model, "Reply with OK."}
	case "opencode":
		args = []string{"run", "--model", model, "Reply with OK."}
	case "codex":
		args = []string{"exec", "--model", model, "--skip-git-repo-check", "Reply with OK."}
	default:
		return protocol.ModelTestResult{Output: "a test start is not supported for " + p.Harness}
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
