package gateway

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
)

func (g *Gateway) importSkillFolder(ctx context.Context, p protocol.ToolFolderImport) (any, error) {
	host := g.conn(p.NodeID)
	if host == nil {
		return nil, reject("chosen machine is offline")
	}
	if !filepath.IsAbs(p.Location) {
		return nil, reject("use an absolute folder path")
	}
	raw, err := host.request(ctx, "node.skill_folder", p, 60*time.Second)
	if err != nil {
		return nil, err
	}
	var result protocol.ToolFolderBundle
	if json.Unmarshal(raw, &result) != nil || result.Name == "" || result.Name != filepath.Base(p.Location) {
		return nil, reject("invalid folder response")
	}
	dir, err := os.MkdirTemp("", "agent-net-folder-import-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	if err = inventory.UnpackSkill(result.Data, dir); err != nil {
		return nil, reject("invalid or oversized skill bundle")
	}
	if info, e := os.Lstat(filepath.Join(dir, "SKILL.md")); e != nil || !info.Mode().IsRegular() {
		return nil, reject("folder needs SKILL.md")
	}
	hash, err := inventory.SkillHash(dir)
	if err != nil {
		return nil, err
	}
	bundle, err := inventory.PackSkill(dir)
	if err != nil {
		return nil, err
	}
	identity, _ := json.Marshal([]string{protocol.InventorySkill, result.Name, hash})
	item := string(identity)
	if _, err = g.store.db.Exec(`INSERT OR IGNORE INTO tool_identities(id,kind,name,hash) VALUES(?,?,?,?)`, item, protocol.InventorySkill, result.Name, hash); err != nil {
		return nil, err
	}
	repo, err := g.skillRepository()
	if err != nil {
		return nil, err
	}
	return repo.put(item, hash, bundle, p.NodeID, p.Location)
}
