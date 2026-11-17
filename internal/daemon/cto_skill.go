package daemon

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/niparis/agent-net/internal/daemon/ctoskill"
	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
)

// The CTO's own skill, agent-net-setup, is carried in the daemon's binary. It is
// not written into a harness folder (decision 0171). ensureCTOSkill ships it to
// the skill archive when its content changes, and adds it once to the CTO's node
// list. Each harness then gets it from the node's skill folder, as any skill does
// (decision 0169, task 4).

// ctoSkillTimeout bounds the gateway calls of one ensure, so a gateway that does
// not answer delays a CTO message by seconds and not by the start timeout.
const ctoSkillTimeout = 10 * time.Second

// ensureCTOSkillLogged runs ensureCTOSkill once per daemon, and logs a failure.
// A failure costs the skill for this start only.
func (d *Daemon) ensureCTOSkillLogged() {
	if d.ctoSkillDone.Load() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), ctoSkillTimeout)
	defer cancel()
	if err := d.ensureCTOSkill(ctx); err != nil {
		d.Logf("cto skill %s: %v", ctoskill.Name, err)
		return
	}
	d.ctoSkillDone.Store(true)
}

func (d *Daemon) ensureCTOSkill(ctx context.Context) error {
	if d.cfg.StateDir == "" {
		return errors.New("no state folder")
	}
	removeOldCTOSkillCopies(d.cfg.StateDir)
	bundle, hash, err := packCTOSkillOnce()
	if err != nil {
		return err
	}
	shipped, listed, err := d.ctoSkillState()
	if err != nil {
		return err
	}
	if shipped != hash {
		if _, err := d.call(ctx, protocol.FrameSkillArchiveBundled, protocol.SkillArchiveBundled{Name: ctoskill.Name, Bundle: bundle, By: "cto"}); err != nil {
			return err
		}
		if _, err := d.st.db.Exec(`UPDATE cto SET setup_skill_hash=? WHERE id=1`, hash); err != nil {
			return err
		}
	}
	// Once only: an owner who takes the skill off the CTO's list keeps it off.
	if !listed {
		if _, err := d.call(ctx, protocol.FrameSkillsSet, protocol.SkillSetRequest{Level: "agent", Scope: "cto", Add: []string{ctoskill.Name}, By: "cto"}); err != nil {
			return err
		}
		if _, err := d.st.db.Exec(`UPDATE cto SET setup_skill_listed=1 WHERE id=1`); err != nil {
			return err
		}
	}
	return nil
}

// ctoSkillState is the hash of the skill the archive last got from this daemon,
// and whether it was added to the CTO's list.
func (d *Daemon) ctoSkillState() (string, bool, error) {
	var hash string
	var listed int
	err := d.st.db.QueryRow(`SELECT setup_skill_hash, setup_skill_listed FROM cto WHERE id=1`).Scan(&hash, &listed)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return hash, listed != 0, err
}

// ctoSkillPacked is the packed skill. The binary carries one fixed skill, so it is
// packed once per process.
var ctoSkillPacked struct {
	once   sync.Once
	bundle []byte
	hash   string
	err    error
}

func packCTOSkillOnce() ([]byte, string, error) {
	ctoSkillPacked.once.Do(func() {
		ctoSkillPacked.bundle, ctoSkillPacked.hash, ctoSkillPacked.err = packCTOSkill()
	})
	return ctoSkillPacked.bundle, ctoSkillPacked.hash, ctoSkillPacked.err
}

// packCTOSkill packs the embedded skill folder the way a machine bundle is
// packed, and returns it with the content hash the archive will store.
func packCTOSkill() ([]byte, string, error) {
	dir, err := os.MkdirTemp("", "agent-net-cto-skill-")
	if err != nil {
		return nil, "", err
	}
	defer os.RemoveAll(dir)
	folder := filepath.Join(dir, ctoskill.Name)
	if err := ctoskill.Write(folder); err != nil {
		return nil, "", err
	}
	hash, err := inventory.SkillHash(folder)
	if err != nil {
		return nil, "", err
	}
	bundle, err := inventory.PackSkillLimit(folder, inventory.ArchiveLimit)
	if err != nil {
		return nil, "", err
	}
	return bundle, hash, nil
}

// removeOldCTOSkillCopies deletes the copies that earlier builds wrote into the
// CTO's harness folders. The skill now reaches each harness only through the
// node's skill folder, so a copy there would be a second, unmanaged one.
func removeOldCTOSkillCopies(stateDir string) {
	for _, dir := range []string{
		filepath.Join(stateDir, "cto", "claude", ".claude", "skills", ctoskill.Name),
		filepath.Join(stateDir, "cto", ".claude", "skills", ctoskill.Name),
	} {
		_ = os.RemoveAll(dir)
	}
}
