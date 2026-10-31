package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
)

func (d *Daemon) onSkillBundle(ctx context.Context, l *link, h protocol.Header, raw []byte) {
	var p protocol.SkillBundleRequest
	var data any
	err := json.Unmarshal(raw, &p)
	if err == nil {
		allowed := false
		d.toolsMu.Lock()
		for _, v := range d.toolsReport.Items {
			if v.Kind == protocol.InventorySkill && v.Name == p.Name && v.Hash == p.Hash && v.Location == p.Location && v.State == protocol.InventoryOK {
				allowed = true
				break
			}
		}
		d.toolsMu.Unlock()
		if !allowed {
			err = errors.New("skill location is not a current observation")
		} else {
			var bundle []byte
			bundle, err = inventory.PackSkill(p.Location)
			if err != nil {
				err = errors.New("cannot safely read skill bundle")
			} else {
				data = protocol.SkillBundle{Data: bundle}
			}
		}
	}
	if err != nil && data == nil {
		err = errors.New("cannot import skill from this location")
	}
	if b, e := protocol.EncodeResult(h.ID, data, err); e == nil {
		_ = l.write(ctx, b)
	}
}

func (d *Daemon) onSkillText(ctx context.Context, l *link, h protocol.Header, raw []byte) {
	var p protocol.SkillTextRequest
	var data any
	err := json.Unmarshal(raw, &p)
	if err == nil {
		var fingerprint string
		d.toolsMu.Lock()
		for _, v := range d.toolsReport.Items {
			if v.Kind == protocol.InventorySkill && v.Name == p.Name && v.Hash == p.Hash && v.Location == p.Location.Location && v.State == protocol.InventoryOK && v.Manifest != nil && !v.Manifest.TooLarge {
				for _, f := range v.Manifest.Files {
					if f.Path == p.Path {
						fingerprint = f.Fingerprint
						break
					}
				}
				break
			}
		}
		d.toolsMu.Unlock()
		if fingerprint == "" {
			err = errors.New("file is not in the current manifest")
		} else {
			var b []byte
			b, err = inventory.ReadSkillText(p.Location.Location, p.Path)
			if err == nil {
				sum := sha256.Sum256(append([]byte("file\x00"), b...))
				if hex.EncodeToString(sum[:]) != fingerprint {
					err = errors.New("file changed since scan")
				} else {
					data = struct {
						Text string `json:"text"`
					}{string(b)}
				}
			}
		}
	}
	if err != nil {
		err = errors.New("cannot read observed skill file")
	}
	if b, e := protocol.EncodeResult(h.ID, data, err); e == nil {
		_ = l.write(ctx, b)
	}
}
