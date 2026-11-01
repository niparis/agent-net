package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
)

func readSkillFolder(path string) (protocol.ToolFolderBundle, error) {
	var result protocol.ToolFolderBundle
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || filepath.Base(path) == "." || filepath.Base(path) == string(filepath.Separator) {
		return result, errors.New("use an absolute skill folder path")
	}
	info, err := os.Lstat(filepath.Join(path, "SKILL.md"))
	if err != nil || !info.Mode().IsRegular() {
		return result, errors.New("folder needs a regular SKILL.md")
	}
	result.Data, err = inventory.PackSkill(path)
	result.Name = filepath.Base(path)
	return result, err
}
func (d *Daemon) onSkillFolder(ctx context.Context, l *link, h protocol.Header, raw []byte) {
	var p protocol.ToolFolderImport
	var result protocol.ToolFolderBundle
	err := json.Unmarshal(raw, &p)
	if err == nil {
		result, err = readSkillFolder(p.Location)
	}
	_ = writeSkillScanResult(l, ctx, h.ID, result, err)
}

// onSkillRemoveCopy uses the existing guarded removal journal and backups.
// The gateway sends this only for an explicit, owner-gated copy choice.
func (d *Daemon) onSkillRemoveCopy(ctx context.Context, l *link, h protocol.Header, raw []byte) {
	var p protocol.ToolSkillScan
	err := json.Unmarshal(raw, &p)
	var result protocol.ToolResult
	if err == nil {
		if _, err = d.skillDetails(p); err == nil {
			item, _ := json.Marshal([]string{protocol.InventorySkill, p.Name, p.Hash})
			result = d.removeSkill(protocol.ToolTarget{Name: p.Name, Revision: protocol.ToolRevision{Item: string(item), Version: p.Hash}, Target: protocol.ToolPlanTarget{Source: p.Location, Action: "remove"}})
			if result.State == "applied" {
				d.scanTools(ctx)
			}
		}
	}
	_ = writeSkillScanResult(l, ctx, h.ID, result, err)
}
