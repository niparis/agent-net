package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
)

// probeToolDestination inspects only paths, never skill contents.
func (d *Daemon) probeToolDestination(p protocol.ToolPlanProbe) protocol.ToolPlanProbeResult {
	bad := func(s string) protocol.ToolPlanProbeResult { return protocol.ToolPlanProbeResult{Conflict: s} }
	if p.Name == "" || p.Name == "." || p.Name == ".." || filepath.Base(p.Name) != p.Name || strings.ContainsAny(p.Name, "/\\\x00") {
		return bad("invalid skill name")
	}
	if p.Source != "" {
		d.toolsMu.Lock()
		found := false
		for _, v := range d.toolsReport.Items {
			if v.Kind == protocol.InventorySkill && v.Name == p.Name && v.Location == p.Source && v.Hash == p.Hash {
				found = true
				break
			}
		}
		d.toolsMu.Unlock()
		if !found {
			return bad("source is no longer observed")
		}
		hash, err := inventory.SkillHash(p.Source)
		if err != nil || hash != p.Hash {
			return bad("source changed since scan")
		}
		return protocol.ToolPlanProbeResult{}
	}
	base := d.cfg.InventoryHome
	if p.Checkout != "" {
		base = p.Checkout
	}
	if base == "" || !filepath.IsAbs(base) {
		return bad("destination root unavailable")
	}
	var root string
	switch p.Harness {
	case "claude":
		root = ".claude/skills"
	case "codex":
		root = ".agents/skills"
	case "opencode":
		if p.Checkout == "" {
			root = ".config/opencode/skills"
		} else {
			root = ".opencode/skills"
		}
	case "pi":
		if p.Checkout == "" {
			root = ".pi/agent/skills"
		} else {
			root = ".pi/skills"
		}
	default:
		return bad("unsupported harness")
	}
	dest := filepath.Join(base, root, p.Name)
	out := protocol.ToolPlanProbeResult{Destination: dest}
	for cur := base; ; {
		info, err := os.Lstat(cur)
		if err != nil {
			if os.IsNotExist(err) {
				break
			}
			out.Conflict = "cannot inspect destination"
			return out
		}
		if info.Mode()&os.ModeSymlink != 0 {
			out.Conflict = "destination contains a symlink"
			return out
		}
		if cur == dest {
			out.Conflict = "destination already exists and is not owned"
			return out
		}
		if !info.IsDir() {
			out.Conflict = "destination parent is not a directory"
			return out
		}
		rel, _ := filepath.Rel(cur, dest)
		first, _, _ := strings.Cut(rel, string(os.PathSeparator))
		cur = filepath.Join(cur, first)
	}
	return out
}
func (d *Daemon) onToolPlanProbe(ctx context.Context, l *link, h protocol.Header, raw []byte) {
	var p protocol.ToolPlanProbe
	if json.Unmarshal(raw, &p) != nil {
		return
	}
	result := d.probeToolDestination(p)
	if b, err := protocol.EncodeResult(h.ID, result, nil); err == nil {
		_ = l.write(ctx, b)
	}
}
