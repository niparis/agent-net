package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// liveModelTimeout bounds one live harness read. The daemon bounds each
// harness probe itself; this is the outer answer limit.
const liveModelTimeout = 40 * time.Second

// liveModelTTL is how long one live answer is reused for repeat calls.
const liveModelTTL = 5 * time.Minute

// gatewayModelCacheEntry is one live model answer kept for repeat calls.
type gatewayModelCacheEntry struct {
	result protocol.ModelListResult
	at     time.Time
}

// liveModels asks one machine's daemon for its harness model list now. It never
// reads the last reported inventory: an offline machine has no live answer. A
// read with models is reused for ModelListTTL, so a profile add followed by a
// picker does not run the harness twice. A failed or empty read is not reused.
func (g *Gateway) liveModels(ctx context.Context, node protocol.NodeView, harness, filter string) (protocol.ModelListResult, error) {
	key := node.NodeID + "\x00" + strings.ToLower(harness)
	g.modelMu.Lock()
	if e, ok := g.modelCache[key]; ok && time.Since(e.at) < liveModelTTL {
		out := e.result
		g.modelMu.Unlock()
		return filterModels(out, filter), nil
	}
	g.modelMu.Unlock()

	g.mu.Lock()
	target := g.conns[node.NodeID]
	g.mu.Unlock()
	if target == nil {
		return protocol.ModelListResult{}, fmt.Errorf("machine %s is offline, cannot list live", node.Hostname)
	}
	cctx, cancel := context.WithTimeout(ctx, liveModelTimeout)
	defer cancel()
	raw, err := target.call(cctx, protocol.FrameNodeModels, protocol.ModelListRequest{Harness: harness})
	if err != nil {
		return protocol.ModelListResult{}, fmt.Errorf("machine %s could not list its models live: %v", node.Hostname, err)
	}
	var out protocol.ModelListResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return protocol.ModelListResult{}, fmt.Errorf("machine %s answered with a bad live model list: %v", node.Hostname, err)
	}
	// A failed or empty read is never cached: the next call asks the machine
	// again, so a login or quota that was just fixed is seen at once.
	if cacheableModels(out) {
		g.modelMu.Lock()
		if g.modelCache == nil {
			g.modelCache = map[string]gatewayModelCacheEntry{}
		}
		g.modelCache[key] = gatewayModelCacheEntry{result: out, at: time.Now()}
		g.modelMu.Unlock()
	}
	return filterModels(out, filter), nil
}

// cacheableModels reports whether a live answer may be reused. An answer with
// an error or no models is a failed read, not a list.
func cacheableModels(out protocol.ModelListResult) bool {
	if len(out.Harnesses) == 0 {
		return false
	}
	for _, h := range out.Harnesses {
		if h.Error != "" || len(h.Models) == 0 {
			return false
		}
	}
	return true
}

func filterModels(in protocol.ModelListResult, filter string) protocol.ModelListResult {
	out := in
	out.Harnesses = append([]protocol.HarnessModels(nil), in.Harnesses...)
	for i := range out.Harnesses {
		out.Harnesses[i].Models = append([]string(nil), in.Harnesses[i].Models...)
		if filter == "" {
			continue
		}
		kept := out.Harnesses[i].Models[:0]
		for _, id := range out.Harnesses[i].Models {
			if containsFold(id, filter) {
				kept = append(kept, id)
			}
		}
		out.Harnesses[i].Models = kept
	}
	return out
}

func containsFold(s, part string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(part))
}

// testModel asks one machine to run a short real turn with a model.
func (g *Gateway) testModel(ctx context.Context, node protocol.NodeView, harness, model, effort string) (protocol.ModelTestResult, error) {
	g.mu.Lock()
	target := g.conns[node.NodeID]
	g.mu.Unlock()
	if target == nil {
		return protocol.ModelTestResult{}, fmt.Errorf("machine %s is offline, cannot test the model", node.Hostname)
	}
	cctx, cancel := context.WithTimeout(ctx, 100*time.Second)
	defer cancel()
	raw, err := target.call(cctx, protocol.FrameModelTest, protocol.ModelTestRequest{Harness: harness, Model: model, Effort: effort})
	if err != nil {
		return protocol.ModelTestResult{}, fmt.Errorf("machine %s could not test the model: %v", node.Hostname, err)
	}
	var out protocol.ModelTestResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return protocol.ModelTestResult{}, err
	}
	return out, nil
}
