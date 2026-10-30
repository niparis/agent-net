package daemon

import (
	"context"
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
