package daemon

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
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
