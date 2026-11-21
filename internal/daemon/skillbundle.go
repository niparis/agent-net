package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
)

// bundleRefusal is an error whose text the gateway may show the owner.
type bundleRefusal struct{ msg string }

func (e bundleRefusal) Error() string { return e.msg }

// skillSnapshotTTL is how long a packed archive bundle waits for its parts.
const skillSnapshotTTL = 5 * time.Minute

// skillSnapshots holds the packed archive bundles that the gateway reads in
// parts (decision 0169). A bundle is packed once, so every part comes from the
// same bytes. It is dropped when its last part is sent, or when its time runs
// out, so an interrupted read does not keep the bytes.
type skillSnapshots struct {
	mu    sync.Mutex
	items map[string]skillSnapshot
}

type skillSnapshot struct {
	data    []byte
	expires time.Time
}

// put keeps one packed bundle and returns the ID the parts are asked by.
func (s *skillSnapshots) put(data []byte, now time.Time) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweep(now)
	if s.items == nil {
		s.items = map[string]skillSnapshot{}
	}
	id := protocol.NewID("ss")
	s.items[id] = skillSnapshot{data: data, expires: now.Add(skillSnapshotTTL)}
	return id
}

// part returns one part of a kept bundle.
func (s *skillSnapshots) part(id string, index int, now time.Time) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweep(now)
	snap, ok := s.items[id]
	if !ok {
		return nil, bundleRefusal{"the skill is no longer ready to read; read it again"}
	}
	parts := protocol.PartCount(int64(len(snap.data)))
	if index < 0 || index >= parts {
		return nil, bundleRefusal{"the skill part is out of range"}
	}
	start := index * protocol.TransferPartSize
	end := min(start+protocol.TransferPartSize, len(snap.data))
	if index == parts-1 {
		delete(s.items, id)
	}
	return snap.data[start:end], nil
}

// sweep drops the bundles whose time has run out.
func (s *skillSnapshots) sweep(now time.Time) {
	for id, snap := range s.items {
		if now.After(snap.expires) {
			delete(s.items, id)
		}
	}
}

// skillBundleFor packs one skill folder as it is on disk now. The folder must
// be one the last node.skill_sources answer listed. The archive limit applies
// (decision 0169); the hash is not checked.
func (d *Daemon) skillBundleFor(p protocol.SkillBundleRequest) ([]byte, error) {
	d.sourceMu.Lock()
	listed := d.sourceLocations[p.Location]
	d.sourceMu.Unlock()
	if !listed {
		return nil, errors.New("skill location is not a current observation")
	}
	bundle, err := inventory.PackSkillLimit(p.Location, inventory.ArchiveLimit)
	if errors.Is(err, inventory.ErrOverLimit) {
		return nil, bundleRefusal{fmt.Sprintf("the skill is larger than the %d MB archive limit", inventory.ArchiveLimit.Bytes>>20)}
	}
	if err != nil {
		return nil, errors.New("cannot safely read skill bundle")
	}
	return bundle, nil
}

// skillBundleAnswer answers node.skill_bundle. A whole bundle comes in one
// answer, except an archive bundle that is more than one part: that answer
// holds part 0 and a snapshot for the others.
func (d *Daemon) skillBundleAnswer(p protocol.SkillBundleRequest) (protocol.SkillBundle, error) {
	if p.Snapshot != "" {
		data, err := d.skillSnapshots.part(p.Snapshot, p.Part, time.Now())
		if err != nil {
			return protocol.SkillBundle{}, err
		}
		return protocol.SkillBundle{Data: data}, nil
	}
	bundle, err := d.skillBundleFor(p)
	if err != nil {
		return protocol.SkillBundle{}, err
	}
	if len(bundle) <= protocol.TransferPartSize {
		return protocol.SkillBundle{Data: bundle}, nil
	}
	sum := sha256.Sum256(bundle)
	id := d.skillSnapshots.put(bundle, time.Now())
	return protocol.SkillBundle{
		Data:     bundle[:protocol.TransferPartSize],
		Snapshot: id,
		Parts:    protocol.PartCount(int64(len(bundle))),
		Size:     int64(len(bundle)),
		SHA256:   hex.EncodeToString(sum[:]),
	}, nil
}

func (d *Daemon) onSkillBundle(ctx context.Context, l *link, h protocol.Header, raw []byte) {
	var p protocol.SkillBundleRequest
	var data any
	err := json.Unmarshal(raw, &p)
	if err == nil {
		var answer protocol.SkillBundle
		if answer, err = d.skillBundleAnswer(p); err == nil {
			data = answer
		}
	}
	var refusal bundleRefusal
	if err != nil && data == nil && !errors.As(err, &refusal) {
		err = errors.New("cannot import skill from this location")
	}
	if b, e := protocol.EncodeResult(h.ID, data, err); e == nil {
		_ = l.write(ctx, b)
	}
}
