package daemon

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/redact"
)

// probeToolDestination checks paths and fingerprints existing destinations.
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
	if p.Checkout != "" {
		// A machine refuses the home folder, a folder outside Git and a
		// worktree of another project, whatever the plan says.
		if conflict := d.skillFolderConflict(p.Checkout, p.Worktrees, p.Worktrees != nil); conflict != "" {
			return bad(conflict)
		}
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
	case "antigravity":
		root = ".gemini/config/skills"
	case "pi":
		if p.Checkout == "" {
			root = ".pi/agent/skills"
		} else {
			root = ".pi/skills"
		}
	default:
		return bad("unsupported harness")
	}
	if p.Root != "" {
		root = p.Root
		if root != ".claude/skills" && root != ".agents/skills" && !(p.Checkout == "" && root == ".gemini/config/skills") {
			return bad("unsupported skill root")
		}
		if root == ".gemini/config/skills" {
			info, err := os.Lstat(filepath.Join(base, ".gemini"))
			if os.IsNotExist(err) {
				return protocol.ToolPlanProbeResult{Skip: true}
			}
			if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return bad("destination root unavailable")
			}
		}
		foreign := []string{".codex/skills", ".config/opencode/skills", ".pi/agent/skills"}
		if p.Checkout != "" {
			foreign = []string{".codex/skills", ".opencode/skills", ".pi/skills", ".agent/skills"}
		}
		for _, other := range foreign {
			if _, err := os.Lstat(filepath.Join(base, other, p.Name)); err == nil || !os.IsNotExist(err) {
				return bad("another skill source may shadow the planned path")
			}
		}
	}
	dest := filepath.Join(base, root, p.Name)
	out := protocol.ToolPlanProbeResult{Destination: dest}
	for cur := base; ; {
		info, err := os.Lstat(cur)
		if err != nil {
			if os.IsNotExist(err) {
				if cur == base {
					out.Conflict = "destination root is unavailable"
					return out
				}
				break
			}
			out.Conflict = "cannot inspect destination"
			return out
		}
		if info.Mode()&os.ModeSymlink != 0 {
			out.Conflict = "destination contains a symlink"
			fingerprint, e := d.destinationFingerprint(cur)
			if e != nil {
				out.Conflict = "cannot inspect destination"
			} else {
				out.Fingerprint = fingerprint
			}
			return out
		}
		if cur == dest {
			out.Conflict = "destination already exists and is not owned"
			fingerprint, err := d.destinationFingerprint(dest)
			if err != nil {
				out.Conflict = "cannot inspect destination"
			} else {
				out.Fingerprint = fingerprint
			}
			return out
		}
		if !info.IsDir() {
			out.Conflict = "destination parent is not a directory"
			fingerprint, e := d.destinationFingerprint(cur)
			if e != nil {
				out.Conflict = "cannot inspect destination"
			} else {
				out.Fingerprint = fingerprint
			}
			return out
		}
		rel, _ := filepath.Rel(cur, dest)
		first, _, _ := strings.Cut(rel, string(os.PathSeparator))
		cur = filepath.Join(cur, first)
	}
	return out
}
func (d *Daemon) probeToolDestinations(probes []protocol.ToolPlanProbe) []protocol.ToolPlanProbeResult {
	start := time.Now()
	results := make([]protocol.ToolPlanProbeResult, len(probes))
	for i, probe := range probes {
		results[i] = d.probeToolDestination(probe)
		if probe.Operation == "repo_remove" {
			results[i] = d.probeRepoSkillRemoval(probe)
		}
		if results[i].Conflict != "" {
			d.Logf("tool probe failed: skill=%q project=%q reason=%q", redact.Secrets(probe.Name), redact.Secrets(probe.Project), redact.Secrets(results[i].Conflict))
		}
	}
	d.Logf("tool probes: count=%d duration=%s", len(probes), time.Since(start))
	return results
}

func (d *Daemon) onToolPlanProbes(ctx context.Context, l *link, h protocol.Header, raw []byte) {
	var p protocol.ToolPlanProbes
	if json.Unmarshal(raw, &p) != nil {
		return
	}
	results := d.probeToolDestinations(p.Probes)
	if b, err := protocol.EncodeResult(h.ID, results, nil); err == nil {
		_ = l.write(ctx, b)
	}
}

func (d *Daemon) onToolPlanProbe(ctx context.Context, l *link, h protocol.Header, raw []byte) {
	var p protocol.ToolPlanProbe
	if json.Unmarshal(raw, &p) != nil {
		return
	}
	result := d.probeToolDestination(p)
	if p.Operation == "repo_remove" {
		result = d.probeRepoSkillRemoval(p)
	}
	if result.Conflict != "" {
		d.Logf("tool probe failed: skill=%q project=%q reason=%q", redact.Secrets(p.Name), redact.Secrets(p.Project), redact.Secrets(result.Conflict))
	}
	if b, err := protocol.EncodeResult(h.ID, result, nil); err == nil {
		_ = l.write(ctx, b)
	}
}

func (d *Daemon) destinationFingerprint(root string) (string, error) {
	d.toolsPlanKeyOnce.Do(func() { _, d.toolsPlanKeyErr = rand.Read(d.toolsPlanKey[:]) })
	if d.toolsPlanKeyErr != nil {
		return "", d.toolsPlanKeyErr
	}
	h := hmac.New(sha256.New, d.toolsPlanKey[:])
	count := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		count++
		if count > 10000 {
			return fmt.Errorf("destination exceeds inspection limit")
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%d:%s:%d:%d:%d\x00", len(rel), rel, info.Mode(), info.Size(), info.ModTime().UnixNano())
		return nil
	})
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		digest, err := inventory.SkillHash(root)
		if err != nil {
			return "", err
		}
		h.Write([]byte(digest))
	} else if info.Mode().IsRegular() {
		if info.Size() > 64<<20 {
			return "", fmt.Errorf("destination exceeds inspection limit")
		}
		file, err := os.Open(root)
		if err != nil {
			return "", err
		}
		opened, err := file.Stat()
		if err != nil || !os.SameFile(info, opened) {
			file.Close()
			return "", fmt.Errorf("destination changed during inspection")
		}
		n, err := io.Copy(h, io.LimitReader(file, (64<<20)+1))
		closeErr := file.Close()
		if err != nil {
			return "", err
		}
		if closeErr != nil {
			return "", closeErr
		}
		if n != info.Size() {
			return "", fmt.Errorf("destination changed during inspection")
		}
	} else if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(root)
		if err != nil {
			return "", err
		}
		h.Write([]byte(target))
	} else {
		return "", fmt.Errorf("unsupported destination type")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (d *Daemon) onToolPlanScan(ctx context.Context, l *link, h protocol.Header) {
	var err error
	if d.cfg.InventoryHome == "" {
		err = errors.New("inventory scanner unavailable")
	} else {
		report := d.scanTools(ctx)
		if reason := inventory.PlanReportProblem(report); reason != "" {
			err = errors.New(reason)
		} else if _, e := d.call(ctx, "node.inventory", report); e != nil {
			err = errors.New("inventory upload unavailable")
		}
	}
	if b, e := protocol.EncodeResult(h.ID, nil, err); e == nil {
		_ = l.write(ctx, b)
	}
}
