package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// CTO proposals live in the daemon's own database. The owner's Desk reads one
// list of asks from the gateway (decision 0090), so the daemon sends it the
// open proposals of this machine with ask.sync. The call replaces the whole
// list, so a call lost while the link was down is repaired by the next one.

const proposalAskEvery = time.Minute

type proposalAskState struct {
	mu   sync.Mutex
	sent string // hash of the list the gateway last accepted
}

// proposalAskLines builds the asks for the open proposals.
func (d *Daemon) proposalAskLines() ([]protocol.AskOpen, error) {
	items, err := d.proposalRunner().list()
	if err != nil {
		return nil, err
	}
	var asks []protocol.AskOpen
	for _, v := range items {
		if v.Status != "open" {
			continue
		}
		reason, _, _ := strings.Cut(strings.TrimSpace(v.Reason), "\n")
		question := "CTO proposes " + strings.ReplaceAll(v.Call.Tool, "_", " ")
		if v.Call.Action != "" {
			question += " " + v.Call.Action
		}
		if reason = protocol.CleanWaitingText(reason); reason != "" {
			question += ": " + reason
		}
		asks = append(asks, protocol.AskOpen{
			Kind: protocol.AskKindProposal, Ref: v.ID, From: "CTO",
			Question: question, Options: []string{"approve", "decline"}, Detail: v.Reason,
		})
	}
	return asks, nil
}

// syncProposalAsks sends the open proposals to the gateway when the list
// changed since the last accepted call. Failure is quiet: the next tick tries
// again. No lock is held during the gateway call.
func (d *Daemon) syncProposalAsks(ctx context.Context) {
	asks, err := d.proposalAskLines()
	if err != nil {
		return
	}
	h := sha256.New()
	for _, a := range asks {
		h.Write([]byte(a.Ref + "\x00" + a.Question + "\x00" + a.Detail + "\x00"))
	}
	sum := hex.EncodeToString(h.Sum(nil))
	d.proposalAsks.mu.Lock()
	same := d.proposalAsks.sent == sum
	d.proposalAsks.mu.Unlock()
	if same {
		return
	}
	if _, err := d.call(ctx, "ask.sync", protocol.AskSync{Kind: protocol.AskKindProposal, Asks: asks}); err != nil {
		return
	}
	d.proposalAsks.mu.Lock()
	d.proposalAsks.sent = sum
	d.proposalAsks.mu.Unlock()
}

// proposalAskLoop keeps the gateway's proposal asks right, and resends the
// list after a reconnect.
func (d *Daemon) proposalAskLoop(ctx context.Context) {
	ticker := time.NewTicker(proposalAskEvery)
	defer ticker.Stop()
	for {
		d.syncProposalAsks(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
