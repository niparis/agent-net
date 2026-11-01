package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func (d *Daemon) sessionStart(ctx context.Context, p protocol.SessionStartRequest) (protocol.StartedSession, error) {
	p.Project = strings.TrimSpace(p.Project)
	if p.Project == "" {
		return protocol.StartedSession{}, errors.New("session_start needs project")
	}
	if p.Harness == "" {
		p.Harness = "claude"
	}
	if p.Harness != "claude" && p.Harness != "opencode" {
		return protocol.StartedSession{}, errors.New("session_start currently supports only local Claude and OpenCode sessions")
	}
	if len(p.FirstMessage) > protocol.MaxBodyBytes {
		return protocol.StartedSession{}, errors.New("first_message is too long")
	}
	bin, err := d.harnessBinary(p.Harness)
	if err != nil {
		return protocol.StartedSession{}, err
	}
	raw, err := d.call(ctx, "query.projects", protocol.QueryProjects{})
	if err != nil {
		return protocol.StartedSession{}, err
	}
	var projects protocol.ProjectsResult
	if err = json.Unmarshal(raw, &projects); err != nil {
		return protocol.StartedSession{}, err
	}
	var found *protocol.ProjectView
	for _, project := range projects.Projects {
		if project.NodeID == d.cfg.NodeID && (project.Name == p.Project || project.Path == p.Project) {
			if found != nil {
				return protocol.StartedSession{}, errors.New("project is ambiguous: use its local path")
			}
			copy := project
			found = &copy
		}
	}
	if found == nil {
		return protocol.StartedSession{}, errors.New("project is not known on this machine")
	}
	info, err := os.Stat(found.Path)
	if err != nil {
		return protocol.StartedSession{}, err
	}
	if !info.IsDir() {
		return protocol.StartedSession{}, errors.New("project path is not a folder")
	}
	r := protocol.StartedSession{ID: protocol.NewID("start"), Status: "starting", NodeID: d.cfg.NodeID, Harness: p.Harness, Project: found.Name, Path: found.Path, Model: p.Model, StartedAt: time.Now().Unix()}
	r.Branch, _ = gitOut(ctx, r.Path, "branch", "--show-current")
	if p.Harness == "opencode" {
		return d.launchOwnerOpenCode(ctx, r, p.FirstMessage)
	}
	args := []string{"--bg", "--name", r.Project}
	if p.Model != "" {
		args = append(args, "--model", p.Model)
	}
	if p.FirstMessage != "" {
		args = append(args, "--", p.FirstMessage)
	}
	return d.launchOwnerClaude(r, bin, args, ""), nil
}

func (d *Daemon) sessionResume(ctx context.Context, p protocol.SessionResumeRequest) (protocol.StartedSession, error) {
	raw, err := d.call(ctx, "query.agent", protocol.QueryAgent{SessionID: p.SessionID})
	if err != nil {
		return protocol.StartedSession{}, err
	}
	var result struct {
		Agent protocol.AgentView `json:"agent"`
	}
	if err = json.Unmarshal(raw, &result); err != nil {
		return protocol.StartedSession{}, err
	}
	a := result.Agent
	if a.NodeID != d.cfg.NodeID || a.Harness != "claude" || a.NativeID == "" {
		return protocol.StartedSession{}, errors.New("resume works for Claude on this machine for now")
	}
	if a.Online {
		return protocol.StartedSession{}, errors.New("session is already online")
	}
	info, err := os.Stat(a.Cwd)
	if err != nil || !info.IsDir() {
		return protocol.StartedSession{}, fmt.Errorf("session folder is gone: %s", a.Cwd)
	}
	bin, err := d.harnessBinary("claude")
	if err != nil {
		return protocol.StartedSession{}, err
	}
	r := protocol.StartedSession{ID: protocol.NewID("start"), Status: "starting", NodeID: d.cfg.NodeID, Harness: "claude", Project: a.Profile.Name, Path: a.Cwd, StartedAt: time.Now().Unix()}
	return d.launchOwnerClaude(r, bin, []string{"--bg", "--resume", a.NativeID}, a.NativeID), nil
}

func (d *Daemon) launchOwnerClaude(r protocol.StartedSession, bin string, args []string, resume string) protocol.StartedSession {
	d.mu.Lock()
	if d.startedSessions == nil {
		d.startedSessions = make(map[string]protocol.StartedSession)
	}
	d.startedSessions[r.ID] = r
	d.mu.Unlock()
	// The launch belongs to the daemon, not the requesting socket. Cancelling a
	// request or closing the TUI must not stop an owner's background session.
	d.wg.Add(1)
	go func(r protocol.StartedSession) {
		defer d.wg.Done()
		cctx, cancel := context.WithTimeout(d.ctoCtx, d.cfg.Workers.filled().StartTimeout)
		defer cancel()
		d.claudeStartMu.Lock()
		sf, err := d.launchClaudeBackground(cctx, bin, r.Path, args, "", resume, nil)
		d.claudeStartMu.Unlock()
		if err == nil {
			r.NativeID, r.JobID = sf.SessionID, sf.JobID
			d.mu.Lock()
			d.startedSessions[r.ID] = r
			d.mu.Unlock()
			ticker := time.NewTicker(25 * time.Millisecond)
			defer ticker.Stop()
			for {
				d.mu.Lock()
				if ls := d.sessions[key("claude", sf.SessionID)]; ls != nil {
					r.SessionID = ls.sessionID
				}
				d.mu.Unlock()
				if r.SessionID != "" {
					break
				}
				select {
				case <-cctx.Done():
					err = fmt.Errorf("the daemon did not register the Claude session: %w", cctx.Err())
				case <-ticker.C:
				}
				if err != nil {
					break
				}
			}
		}
		if err != nil {
			r.Status = "failed"
			r.Error = err.Error()
		} else {
			r.Status = "started"
		}
		d.mu.Lock()
		d.startedSessions[r.ID] = r
		d.mu.Unlock()
	}(r)
	return r
}

func (d *Daemon) sessionStartGet(id string) (protocol.StartedSession, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	r, ok := d.startedSessions[id]
	if !ok {
		return r, errors.New("unknown session start")
	}
	return r, nil
}
