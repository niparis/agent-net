package gateway

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// phraseWorker uses the same gateway chooser and launch path as agent_start.
// The request has no model or harness and adds a mandatory read-only constraint.
type phraseWorker struct{ g *Gateway }

func (w phraseWorker) Run(ctx context.Context, req protocol.WorkerStartRequest) (string, error) {
	if len(protocol.WorkerTaskBodyWithCharter("", req.Task, "", "", "", "", protocol.WorkersReadOnly)) > protocol.MaxBodyBytes {
		return "", errors.New("phrasing batch exceeds the worker message limit")
	}
	projects, err := w.g.store.Projects("")
	if err != nil {
		return "", err
	}
	agents, err := w.g.store.Agents(protocol.QueryAgents{OnlineOnly: true, IncludeOwners: true})
	if err != nil {
		return "", err
	}
	var owner protocol.AgentView
	var project protocol.ProjectView
	var nc *nodeConn
	for _, p := range projects {
		if p.Path == "" {
			continue
		}
		for _, a := range agents {
			if a.NodeID != p.NodeID || !protocol.IsOwner(a.Harness) || !a.Online {
				continue
			}
			w.g.mu.Lock()
			conn := w.g.conns[a.NodeID]
			w.g.mu.Unlock()
			if conn != nil {
				owner = a
				project = p
				nc = conn
				break
			}
		}
		if nc != nil {
			break
		}
	}
	if nc == nil {
		return "", errors.New("no connected owner and project for phrasing")
	}
	req.Project = project.Path
	req.Machine = project.NodeID
	started, err := w.g.workerStart(ctx, nc, protocol.WorkerStartFrame{WorkerStartRequest: req, Owner: owner.SessionID, RequireReadOnly: true})
	if err != nil {
		return "", err
	}
	if started.Queued {
		return "", errors.New("background worker was unexpectedly queued")
	}
	completed := false
	defer func() {
		if !completed && started.SessionID != "" {
			stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, _ = w.g.workerStop(stopCtx, nc, protocol.WorkerStopFrame{Requester: owner.SessionID, Session: started.SessionID})
		}
	}()
	body := protocol.WorkerTaskBodyWithCharter(started.Harness, req.Task, "", "", "", "", protocol.WorkersReadOnly)
	m, created, err := w.g.store.StoreMessage(protocol.MsgSend{Sender: owner.SessionID, Recipient: started.SessionID, Body: body, Type: protocol.TypeRequest})
	if err != nil {
		return "", err
	}
	if !created || m.State == protocol.StateHeld {
		return "", errors.New("phrasing task could not be delivered")
	}
	w.g.dispatch(ctx, m)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		var text string
		err = w.g.store.db.QueryRow(`SELECT body FROM messages WHERE sender=? AND recipient=? AND parent_id=? AND body LIKE '[agent-net worker notice]%' ORDER BY created_at DESC LIMIT 1`, started.SessionID, owner.SessionID, m.ID).Scan(&text)
		if err != nil && err != sql.ErrNoRows {
			return "", err
		}
		if err == nil {
			completed = true
			if !strings.HasPrefix(text, "[agent-net worker notice]\nstatus: done\n") {
				return "", errors.New("phrasing worker failed")
			}
			_, report, ok := strings.Cut(text, protocol.ReportMarker)
			if !ok {
				return "", errors.New("phrasing worker returned no report")
			}
			report, _, _ = strings.Cut(report, protocol.ChecklistMarker)
			report = strings.TrimSpace(report)
			// Codex worker briefs require this hand-back suffix outside the report.
			report = strings.TrimSpace(strings.TrimSuffix(report, "\ncomplete: yes"))
			return report, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
		}
	}
}
