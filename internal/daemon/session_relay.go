package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The owner session frames (docs/design/start-sessions.md, build plan part 8).
// The gateway sends them to the machine that holds the project or session.
// That machine checks the owner flag again: it must name the owner
// pseudo-session of the sending machine, as the gateway saw it.
func (d *Daemon) sessionOwnerRelay(ctx context.Context, typ string, raw []byte) (any, error) {
	var p protocol.SessionOwnerRequest
	if json.Unmarshal(raw, &p) != nil {
		return nil, errors.New("bad session relay")
	}
	if err := d.checkOwnerRelay(ctx, p.Owner, p.FromNode); err != nil {
		return nil, err
	}
	switch typ {
	case protocol.FrameSessionOwnerProposalStart:
		p.CTOProposal = true
		p.TrustClaude = false
		ctx = context.WithValue(ctx, proposalRemoteOwnerKey{}, true)
		return d.sessionStartLocal(ctx, p.SessionStartRequest)
	case protocol.FrameSessionOwnerStart:
		return d.sessionStartLocal(ctx, p.SessionStartRequest)
	case protocol.FrameSessionOwnerResume:
		return d.sessionResumeLocal(ctx, p.SessionID)
	case protocol.FrameSessionOwnerStop:
		return d.sessionStopLocal(ctx, p.SessionID)
	case protocol.FrameSessionOwnerGet:
		return d.sessionStartGetLocal(p.StartID)
	}
	return nil, errors.New("unknown session relay")
}

func (d *Daemon) onSessionOwnerFrame(ctx context.Context, l *link, h protocol.Header, raw []byte) {
	data, err := d.sessionOwnerRelay(ctx, h.Type, raw)
	if res, eerr := protocol.EncodeResult(h.ID, data, err); eerr == nil {
		l.write(ctx, res)
	}
}

// checkOwnerRelay asks the gateway who the owner flag names. It must be the
// owner pseudo-session of the sending machine, never an agent or a worker.
func (d *Daemon) checkOwnerRelay(ctx context.Context, owner, fromNode string) error {
	no := errors.New("only the owner session of the sending machine may start, resume or stop a session")
	if owner == "" || fromNode == "" {
		return no
	}
	cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	a, err := d.agentView(cctx, owner)
	if err != nil {
		return errors.New("the owner session of the sending machine is not known to the gateway")
	}
	if a.NodeID != fromNode || !protocol.IsOwner(a.Harness) || a.Worker != nil {
		return no
	}
	return nil
}
