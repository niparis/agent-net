package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Standing orders are the owner's durable instructions to the CTO. The gateway
// keeps them, so they survive CTO restarts and machine changes. The CTO reads
// them at the start of every conversation (ctoSystemPrompt, ctoFirstTurn).

// standingOrdersOp runs one standing order call. source is who saves: the
// owner's own words from the TUI, or the CTO on the owner's instruction.
func (d *Daemon) standingOrdersOp(ctx context.Context, q protocol.StandingOrdersRequest, source string) (protocol.StandingOrdersResult, error) {
	var res protocol.StandingOrdersResult
	var frame string
	switch q.Action {
	case "search", "history", "update":
		frame = "standing." + q.Action
	case protocol.StandingActionSave:
		frame = "standing.save"
		q.Source = source
		q.Restore = nil
	case protocol.StandingActionList, protocol.StandingActionRead:
		frame = "standing.list"
	case protocol.StandingActionRemove:
		frame = "standing.remove"
		if strings.TrimSpace(q.ID) == "" {
			return res, errors.New("remove needs the id of the standing order")
		}
	case "":
		if q.Restore == nil {
			return res, errors.New("standing orders action must be save, read, list or remove")
		}
		// The undo of a removal puts the order back as it was.
		frame = "standing.save"
	default:
		return res, errors.New("standing orders action must be save, read, list or remove")
	}
	raw, err := d.call(ctx, frame, q)
	if err != nil {
		return res, oldGateway(err)
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return res, err
	}
	if q.Action == protocol.StandingActionRead {
		if strings.TrimSpace(q.ID) == "" {
			return res, nil
		}
		res.Order = nil
		for i := range res.Orders {
			if res.Orders[i].ID == q.ID {
				res.Order = &res.Orders[i]
			}
		}
		if res.Order == nil {
			return res, errors.New("no standing order " + q.ID)
		}
	}
	return res, nil
}

// ctoStandingOrders is the CTO's cto_standing_orders tool. A save or a remove
// leaves an Inbox notice with an undo, like the CTO's other changes.
func (d *Daemon) ctoStandingOrders(ctx context.Context, raw json.RawMessage) (any, error) {
	var q protocol.StandingOrdersRequest
	if json.Unmarshal(raw, &q) != nil {
		return nil, errors.New("bad standing orders arguments")
	}
	res, err := d.standingOrdersOp(ctx, q, protocol.StandingSourceCTO)
	if err != nil {
		return nil, err
	}
	var text, diff string
	var undo protocol.StandingOrdersRequest
	switch q.Action {
	case protocol.StandingActionSave:
		if res.Order == nil {
			return res, nil
		}
		text, diff = "CTO saved a standing order: "+cleanLine(res.Order.Text), "added: "+res.Order.Text
		undo = protocol.StandingOrdersRequest{Action: protocol.StandingActionRemove, ID: res.Order.ID}
	case protocol.StandingActionRemove:
		if res.Order == nil {
			return res, nil
		}
		text, diff = "CTO removed a standing order: "+cleanLine(res.Order.Text), "removed: "+res.Order.Text
		undo = protocol.StandingOrdersRequest{Restore: res.Order}
	default:
		return res, nil
	}
	undoRaw, _ := json.Marshal(undo)
	if _, err := d.call(ctx, "inbox.create", map[string]any{"session_id": d.ctoSessionID(), "kind": "check", "text": text, "recommendation": diff, "options": []string{protocol.OpStandingOrders, string(undoRaw)}}); err != nil {
		return res, fmt.Errorf("%s; could not add Inbox notice: %w", text, err)
	}
	return res, nil
}

// standingOrdersBrief reads the orders for a new CTO conversation. A gateway
// that is down or too old gives no orders and never blocks the CTO.
func (d *Daemon) standingOrdersBrief(ctx context.Context) string {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	res, err := d.standingOrdersOp(cctx, protocol.StandingOrdersRequest{Action: protocol.StandingActionList}, "")
	if err != nil {
		return ""
	}
	return protocol.StandingOrdersBrief(res.Orders)
}

// ctoSystemPrompt is the CTO brief plus the owner's standing orders, for a
// harness that starts a conversation with a system prompt.
func (d *Daemon) ctoSystemPrompt(ctx context.Context) string {
	if brief := d.standingOrdersBrief(ctx); brief != "" {
		return ctoBrief + "\n\n" + brief
	}
	return ctoBrief
}

// ctoFirstTurn puts the standing orders in front of the first message of a
// conversation, for a harness whose system prompt is fixed at process start.
func (d *Daemon) ctoFirstTurn(ctx context.Context, text string) string {
	if brief := d.standingOrdersBrief(ctx); brief != "" {
		return brief + "\n\n" + text
	}
	return text
}

func (d *Daemon) judgmentsFind(ctx context.Context, q protocol.JudgmentsFindRequest) (any, error) {
	raw, err := d.call(ctx, "judgment.find", q)
	if err != nil {
		return nil, oldGateway(err)
	}
	var res map[string]any
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	return res, nil
}
