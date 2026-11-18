package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/quotaaxi"
	"github.com/niparis/agent-net/internal/redact"
)

// quota-axi reads Alibaba's token plan from `bl usage token-plan` but accepts
// only the weekly shape. A plan with a monthly allowance is rejected as
// malformed, so the owner gets no row. When quota-axi did not read Alibaba,
// agent-net runs the same bl command itself and builds the windows from its
// per<N><Unit>Percentage and per<N><Unit>ResetTime fields.

// alibabaBLTimeout bounds one bl call. It is the limit quota-axi uses. A test
// shortens it.
var alibabaBLTimeout = 15 * time.Second

const (
	alibabaProvider = "alibaba"
	// alibabaDefaultPlan is the plan name quota-axi shows when bl names none.
	alibabaDefaultPlan = "Alibaba Coding Plan"
)

// alibabaBLArgs is the whole command line after the bl binary.
var alibabaBLArgs = []string{"usage", "token-plan", "--output", "json"}

// alibabaWindowSpec names a window agent-net builds from bl's fields. The
// ids, labels and kinds match what quota-axi uses for the weekly window.
var alibabaWindowSpecs = []struct{ field, id, label, kind string }{
	{"per1Week", "weekly", "week", "weekly"},
	{"per1Month", "monthly", "month", "monthly"},
}

// alibabaPlan is a parsed bl reading.
type alibabaPlan struct {
	Plan    string
	Windows []protocol.QuotaWindow
}

// parseAlibabaTokenPlan reads the first JSON document of bl's output, so a
// later document (an update warning) is ignored. An error document gives the
// error text back with bl's own message, which the sign-in ask keys on.
func parseAlibabaTokenPlan(raw []byte) (alibabaPlan, error) {
	var root map[string]any
	if err := json.NewDecoder(bytes.NewReader(raw)).Decode(&root); err != nil {
		return alibabaPlan{}, errors.New("bl_usage_malformed_json")
	}
	if e, ok := root["error"]; ok && e != nil {
		return alibabaPlan{}, errors.New(blErrorText(e))
	}
	out := alibabaPlan{Plan: alibabaDefaultPlan}
	for _, key := range []string{"planName", "plan"} {
		if s, ok := root[key].(string); ok && strings.TrimSpace(s) != "" {
			out.Plan = strings.TrimSpace(s)
			break
		}
	}
	for _, spec := range alibabaWindowSpecs {
		fraction, ok := root[spec.field+"Percentage"].(float64)
		if !ok || math.IsNaN(fraction) || math.IsInf(fraction, 0) {
			continue
		}
		// bl prints the share as a fraction of 1 (its own text view multiplies
		// by 100), so 0.76 is 76 percent used.
		w := protocol.QuotaWindow{ID: spec.id, Label: spec.label, Kind: spec.kind, PercentUsed: clampPercent(fraction * 100)}
		if reset, ok := root[spec.field+"ResetTime"].(float64); ok {
			w.ResetsAt = unixSeconds(reset)
		}
		w.Exhausted = w.PercentUsed >= 100
		out.Windows = append(out.Windows, w)
	}
	return out, nil
}

// blErrorText is the message of a bl error document, in the form
// quota-axi reports it, so protocol.SignInNeeded still matches.
func blErrorText(e any) string {
	msg := ""
	switch v := e.(type) {
	case map[string]any:
		msg, _ = v["message"].(string)
	case string:
		msg = v
	}
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return "bl_usage_failed"
	}
	if len(msg) > 240 {
		msg = msg[:240]
	}
	return "bl_usage_failed: " + redact.Secrets(msg)
}

// unixSeconds turns bl's reset time, in epoch milliseconds or seconds, into
// Unix seconds. quota-axi uses the same 1e11 cut-off.
func unixSeconds(v float64) int64 {
	if v <= 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	if v > 100_000_000_000 {
		return int64(v / 1000)
	}
	return int64(v)
}

// alibabaNeedsBL says whether quota-axi left the alibaba row without a reading.
// A row that read fine is never replaced.
func alibabaNeedsBL(q *protocol.Quotas) (int, bool) {
	for i, p := range q.Providers {
		if p.Provider == alibabaProvider {
			return i, p.Error != "" || len(p.Windows) == 0
		}
	}
	return 0, false
}

// fillAlibabaFromBL runs bl for the alibaba row when quota-axi did not read
// it. bl comes from the quota reader's own PATH, with no shell.
func (d *Daemon) fillAlibabaFromBL(ctx context.Context, quotaPath string, q *protocol.Quotas) {
	i, needed := alibabaNeedsBL(q)
	if !needed {
		return
	}
	env, err := quotaaxi.NodeExecEnv(quotaPath)
	if err != nil {
		return
	}
	bin, err := signInBinary(env)
	if err != nil {
		return
	}
	start := time.Now()
	plan, err := runAlibabaBL(ctx, bin, env)
	d.Logf("quota: bl usage token-plan took %s", time.Since(start).Round(time.Millisecond))
	p := &q.Providers[i]
	if err != nil {
		// Keep the failure, with bl's own words, so a missing console login
		// still raises the sign-in ask.
		p.Error = err.Error()
		return
	}
	if len(plan.Windows) == 0 {
		return
	}
	p.Plan, p.Windows, p.Error, p.Stale, p.NeedsAttention = plan.Plan, plan.Windows, "", false, false
}

func runAlibabaBL(ctx context.Context, bin string, env []string) (alibabaPlan, error) {
	ctx, cancel := context.WithTimeout(ctx, alibabaBLTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, alibabaBLArgs...)
	cmd.Env = env
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.WaitDelay = time.Second
	raw, runErr := cmd.Output()
	if ctx.Err() != nil {
		return alibabaPlan{}, fmt.Errorf("bl_usage_failed: no answer within %s", alibabaBLTimeout)
	}
	plan, parseErr := parseAlibabaTokenPlan(raw)
	if parseErr == nil && runErr == nil {
		return plan, nil
	}
	// bl may print its error document on either stream.
	if parseErr != nil && parseErr.Error() != "bl_usage_malformed_json" {
		return alibabaPlan{}, parseErr
	}
	if _, err := parseAlibabaTokenPlan(stderr.Bytes()); err != nil && err.Error() != "bl_usage_malformed_json" {
		return alibabaPlan{}, err
	}
	if runErr != nil {
		if line := firstStderrLine(stderr.Bytes()); line != "" {
			return alibabaPlan{}, errors.New("bl_usage_failed: " + line)
		}
		return alibabaPlan{}, errors.New("bl_usage_failed")
	}
	return alibabaPlan{}, parseErr
}
