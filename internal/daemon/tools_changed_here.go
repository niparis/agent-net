package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/niparis/agent-net/internal/protocol"
)

// markChangedSkills compares each skill copy the daemon made with the hash it
// deployed. The scan already computed the copy's hash, so this reads only the
// small manifest file and compares strings. A copy whose hash no longer matches
// is marked ChangedHere: someone edited it after it was deployed. Copies the
// daemon did not make are never marked.
func (d *Daemon) markChangedSkills(report *protocol.InventoryReport) {
	if d.cfg.StateDir == "" {
		return
	}
	b, err := os.ReadFile(filepath.Join(d.cfg.StateDir, "skill-manifest.json"))
	if err != nil {
		return
	}
	var entries []skillManifestEntry
	if json.Unmarshal(b, &entries) != nil {
		return
	}
	deployed := map[string]string{}
	for _, e := range entries {
		if e.Owned && e.Hash != "" {
			deployed[filepath.Clean(e.Path)] = e.Hash
		}
	}
	if len(deployed) == 0 {
		return
	}
	for i := range report.Items {
		it := &report.Items[i]
		if it.Kind != protocol.InventorySkill || it.State != protocol.InventoryOK || it.Hash == "" {
			continue
		}
		hash, ok := deployed[filepath.Clean(it.Location)]
		if !ok {
			continue
		}
		it.DeployedHash = hash
		it.ChangedHere = it.Hash != hash
	}
}
