package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
)

// onToolSkillScan reads the file manifest of one observed skill. The owner's
// compare plan asks for this when the stored inventory has no details for a
// copy, so one skill is read instead of scanning the whole machine.
func (d *Daemon) onToolSkillScan(ctx context.Context, l *link, h protocol.Header, raw []byte) {
	var p protocol.ToolSkillScan
	if json.Unmarshal(raw, &p) != nil {
		_ = writeSkillScanResult(l, ctx, h.ID, protocol.ToolSkillDetails{}, errors.New("bad skill scan"))
		return
	}
	details, err := d.skillDetails(p)
	if err != nil {
		_ = writeSkillScanResult(l, ctx, h.ID, protocol.ToolSkillDetails{}, err)
		return
	}
	_ = writeSkillScanResult(l, ctx, h.ID, details, nil)
}

func writeSkillScanResult(l *link, ctx context.Context, id string, value any, err error) error {
	b, e := protocol.EncodeResult(id, value, err)
	if e != nil {
		return e
	}
	return l.write(ctx, b)
}

func (d *Daemon) skillDetails(p protocol.ToolSkillScan) (protocol.ToolSkillDetails, error) {
	if p.Name == "" || p.Location == "" {
		return protocol.ToolSkillDetails{}, errors.New("skill name and location are required")
	}
	d.toolsMu.Lock()
	observed := false
	kind := ""
	for _, item := range d.toolsReport.Items {
		if protocol.IsBundleKind(item.Kind) && item.Name == p.Name && item.Location == p.Location && item.Hash == p.Hash && item.State == protocol.InventoryOK {
			observed, kind = true, item.Kind
			break
		}
	}
	d.toolsMu.Unlock()
	if !observed {
		return protocol.ToolSkillDetails{}, errors.New("skill is no longer observed")
	}
	if protocol.IsFileKind(kind) {
		hash, manifest, err := inventory.ObserveFile(p.Location, p.Name)
		if err != nil {
			return protocol.ToolSkillDetails{}, errors.New("skill cannot be read")
		}
		if hash != p.Hash {
			return protocol.ToolSkillDetails{}, errors.New("skill changed since its scan")
		}
		return protocol.ToolSkillDetails{Manifest: manifest, ScannedAt: time.Now().UTC()}, nil
	}
	hash, err := inventory.SkillHash(p.Location)
	if err != nil {
		return protocol.ToolSkillDetails{}, errors.New("skill cannot be read")
	}
	if hash != p.Hash {
		return protocol.ToolSkillDetails{}, errors.New("skill changed since its scan")
	}
	manifest, err := inventory.SkillManifest(p.Location)
	if err != nil {
		return protocol.ToolSkillDetails{}, errors.New("skill details cannot be read")
	}
	return protocol.ToolSkillDetails{Manifest: manifest, ScannedAt: time.Now().UTC()}, nil
}
