package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/repoinfo"
)

// The project registry, on the daemon. Registering a folder happens on the
// machine that holds it, because only this daemon can see the folder. The
// daemon checks every path (it exists, it is a git working tree), resolves it
// to its main tree, strips credentials from the remote, and sends the checked
// entries to the gateway. The gateway registers them on THIS node: there is no
// way to name another machine, for agents or for the command line.

// expandPath turns ~ and ~/x into the daemon user's home folder and requires
// an absolute path.
func expandPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", errors.New("empty path")
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", errors.New("cannot find the home folder")
		}
		p = filepath.Join(home, strings.TrimPrefix(p, "~"))
	} else if strings.HasPrefix(p, "~") {
		return "", errors.New("only ~ and ~/path are supported, not ~user")
	}
	if !filepath.IsAbs(p) {
		return "", errors.New("the path must be absolute or start with ~")
	}
	return filepath.Clean(p), nil
}

// projectSession says who a project call is made for: "cli" for the command
// line, else the bound session.
func (lc *localConn) projectCaller(client string) (session, harness string, err error) {
	if client == "cli" {
		return "", "cli", nil
	}
	lc.mu.Lock()
	ls := lc.ls
	lc.mu.Unlock()
	if ls == nil {
		return "", "", errors.New("this method needs a bound session")
	}
	return lc.d.currentSessionID(ls), ls.info.Harness, nil
}

// oldGateway makes the error of a gateway that has no project frames clear.
func oldGateway(err error) error {
	if err != nil && strings.Contains(err.Error(), "unknown frame type") {
		return errors.New("the gateway is too old for project registration; upgrade it with `agent-net upgrade` on the gateway machine")
	}
	return err
}

// dispatchProject serves project_add and project_remove. handled is false for
// other methods. Listing uses the existing "projects" method.
func (lc *localConn) dispatchProject(ctx context.Context, client, method string, raw json.RawMessage) (res any, handled bool, err error) {
	if method != "project_add" && method != "project_remove" {
		return nil, false, nil
	}
	d := lc.d
	switch method {
	case "project_add":
		var req protocol.ProjectAddRequest
		if json.Unmarshal(raw, &req) != nil {
			return nil, true, errors.New("bad project_add params")
		}
		session, harness, err := lc.projectCaller(client)
		if err != nil {
			return nil, true, err
		}
		out, err := d.projectAdd(ctx, req, session, harness)
		return out, true, err

	case "project_remove":
		// Removing changes where workers may start, so only the owner at the
		// command line does it.
		if client != "cli" {
			return nil, true, errors.New("project_remove is only available from the command line: ask your user to run `agent-net projects remove`")
		}
		var p protocol.ProjectRemove
		if json.Unmarshal(raw, &p) != nil || (p.Name == "" && p.Path == "") {
			return nil, true, errors.New("project_remove needs name or path")
		}
		if p.Path != "" {
			if p.Path, err = expandPath(p.Path); err != nil {
				return nil, true, err
			}
		}
		res, err = d.call(ctx, "project.remove", p)
		return res, true, oldGateway(err)
	}
	return nil, true, nil
}

// projectAdd checks the folders of a request and registers them.
func (d *Daemon) projectAdd(ctx context.Context, req protocol.ProjectAddRequest, session, harness string) (*protocol.ProjectAddOutcome, error) {
	out := &protocol.ProjectAddOutcome{Machine: d.hostName(), DryRun: req.DryRun,
		Added: []protocol.ProjectEntry{}, Existing: []protocol.ProjectEntry{}, Skipped: []protocol.ProjectSkip{}}
	var entries []protocol.ProjectEntry
	seen := map[string]bool{}
	take := func(e protocol.ProjectEntry) {
		if !seen[e.Path] {
			seen[e.Path] = true
			entries = append(entries, e)
		}
	}
	switch {
	case req.Dir != "" && len(req.Paths) > 0:
		return nil, errors.New("give paths, or a dir to scan, not both")
	case req.Dir != "":
		if !req.Scan {
			return nil, errors.New("dir needs scan=true; give paths to register single folders")
		}
		dir, err := expandPath(req.Dir)
		if err != nil {
			return nil, err
		}
		if req.Depth < 0 || req.Depth > protocol.MaxScanDepth {
			return nil, fmt.Errorf("depth must be 1 to %d", protocol.MaxScanDepth)
		}
		sc, err := d.repo.Scan(ctx, dir, repoinfo.ScanOptions{Depth: req.Depth})
		if err != nil {
			return nil, fmt.Errorf("%s: %w", req.Dir, err)
		}
		for _, e := range sc.Projects {
			take(e)
		}
		out.Skipped, out.Overflow = append(out.Skipped, sc.Skipped...), sc.Overflow
	case len(req.Paths) > 0:
		if len(req.Paths) > protocol.ProjectMaxPerCall {
			return nil, fmt.Errorf("at most %d paths per call; use scan for a folder with many repositories", protocol.ProjectMaxPerCall)
		}
		for _, p := range req.Paths {
			path, err := expandPath(p)
			if err == nil {
				var e protocol.ProjectEntry
				if e, err = d.repo.Inspect(path); err == nil {
					take(e)
					continue
				}
			}
			out.Skipped = append(out.Skipped, protocol.ProjectSkip{Path: p, Reason: err.Error()})
		}
	default:
		return nil, errors.New("give paths, or a dir with scan=true")
	}
	if len(entries) == 0 {
		return out, nil
	}
	data, err := d.call(ctx, "project.add", protocol.ProjectAdd{Projects: entries, DryRun: req.DryRun,
		AddedBySession: session, AddedByHarness: harness})
	if err != nil {
		return nil, oldGateway(err)
	}
	var r protocol.ProjectAddResult
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	if r.Added != nil {
		out.Added = r.Added
	}
	if r.Existing != nil {
		out.Existing = r.Existing
	}
	return out, nil
}
