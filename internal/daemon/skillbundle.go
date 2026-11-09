package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
)

// skillBundleFor packs one current observation of a skill, a prompt or an
// agent. A skill is packed as its folder; a prompt or an agent as a bundle of
// its one Markdown file.
func (d *Daemon) skillBundleFor(p protocol.SkillBundleRequest) ([]byte, error) {
	kind := ""
	d.toolsMu.Lock()
	for _, v := range d.toolsReport.Items {
		if protocol.IsBundleKind(v.Kind) && v.Name == p.Name && v.Hash == p.Hash && v.Location == p.Location && v.State == protocol.InventoryOK {
			kind = v.Kind
			break
		}
	}
	d.toolsMu.Unlock()
	if kind == "" {
		return nil, errors.New("skill location is not a current observation")
	}
	var bundle []byte
	var err error
	if protocol.IsFileKind(kind) {
		bundle, err = inventory.PackNamedFile(p.Location, protocol.BundleFileName(p.Name))
	} else {
		bundle, err = inventory.PackSkill(p.Location)
	}
	if err != nil && err.Error() == "skill exceeds bundle limit" {
		return nil, fmt.Errorf("the skill is larger than the %d MB import limit", inventory.MaxBundleBytes>>20)
	}
	if err != nil {
		return nil, errors.New("cannot safely read skill bundle")
	}
	return bundle, nil
}

func (d *Daemon) onSkillBundle(ctx context.Context, l *link, h protocol.Header, raw []byte) {
	var p protocol.SkillBundleRequest
	var data any
	err := json.Unmarshal(raw, &p)
	if err == nil {
		var bundle []byte
		if bundle, err = d.skillBundleFor(p); err == nil {
			data = protocol.SkillBundle{Data: bundle}
		}
	}
	if err != nil && data == nil && !strings.Contains(err.Error(), "import limit") {
		err = errors.New("cannot import skill from this location")
	}
	if b, e := protocol.EncodeResult(h.ID, data, err); e == nil {
		_ = l.write(ctx, b)
	}
}

// skillTextFor reads one file of an observed copy. The file must be in the
// copy's manifest and match its fingerprint. A prompt or an agent has one file,
// its bundle name, which is read from the copy's location.
func (d *Daemon) skillTextFor(p protocol.SkillTextRequest) (string, error) {
	var fingerprint, kind string
	d.toolsMu.Lock()
	for _, v := range d.toolsReport.Items {
		if protocol.IsBundleKind(v.Kind) && v.Name == p.Name && v.Hash == p.Hash && v.Location == p.Location.Location && v.State == protocol.InventoryOK && v.Manifest != nil && !v.Manifest.TooLarge {
			for _, f := range v.Manifest.Files {
				if f.Path == p.Path {
					fingerprint, kind = f.Fingerprint, v.Kind
					break
				}
			}
			break
		}
	}
	d.toolsMu.Unlock()
	if fingerprint == "" {
		return "", errors.New("file is not in the current manifest")
	}
	var b []byte
	var err error
	if protocol.IsFileKind(kind) {
		b, err = inventory.ReadSkillText(filepath.Dir(p.Location.Location), filepath.Base(p.Location.Location))
	} else {
		b, err = inventory.ReadSkillText(p.Location.Location, p.Path)
	}
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(append([]byte("file\x00"), b...))
	if hex.EncodeToString(sum[:]) != fingerprint {
		return "", errors.New("file changed since scan")
	}
	return string(b), nil
}

func (d *Daemon) onSkillText(ctx context.Context, l *link, h protocol.Header, raw []byte) {
	var p protocol.SkillTextRequest
	var data any
	err := json.Unmarshal(raw, &p)
	if err == nil {
		var text string
		if text, err = d.skillTextFor(p); err == nil {
			data = struct {
				Text string `json:"text"`
			}{text}
		}
	}
	if err != nil {
		err = errors.New("cannot read observed skill file")
	}
	if b, e := protocol.EncodeResult(h.ID, data, err); e == nil {
		_ = l.write(ctx, b)
	}
}
