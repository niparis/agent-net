package tui

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"

	"github.com/niparis/agent-net/internal/claudetrust"
	"github.com/niparis/agent-net/internal/protocol"
)

// Paths in the Project field of the New session form
// (docs/design/start-sessions.md, build plan part 10). A path starts where it
// points. An existing folder that agent-net does not know is registered, a
// missing one is created with git init only after the owner says yes, and a
// path outside the home folder of the machine is refused. The daemon applies
// the same checks on the machine that runs the start.

type sessionAskKind int

const (
	askCreateFolder sessionAskKind = iota
	askTrustClaude
	askRaiseMax
)

// sessionAsk is the one extra question of a start: create a missing folder, or
// trust an existing one for Claude Code.
type sessionAsk struct {
	kind    sessionAskKind
	req     protocol.SessionStartRequest
	display string // the folder as the owner typed or sees it
	host    string
	change  *protocol.SettingsChange
}

func (a sessionAsk) question() string {
	switch a.kind {
	case askCreateFolder:
		return "Create " + a.display + " on " + a.host + " and run git init?"
	case askRaiseMax:
		return a.display
	case askTrustClaude:
		return "Trust " + a.display + " for Claude Code?"
	}
	return ""
}

// localHomeDir is the home folder of the local user, with links resolved.
func localHomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	if real, err := filepath.EvalSymlinks(home); err == nil {
		home = real
	}
	return filepath.Clean(home)
}

// expandHome turns ~ and ~/x into the local home folder.
func (m Model) expandHome(path string) string {
	if m.home == "" {
		return path
	}
	if path == "~" {
		return m.home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(m.home, strings.TrimPrefix(path, "~/"))
	}
	return path
}

// tildePath shows a local path with ~ in place of the home folder.
func (m Model) tildePath(path string) string {
	if m.home == "" || path == "" {
		return path
	}
	if path == m.home {
		return "~"
	}
	if strings.HasPrefix(path, m.home+string(filepath.Separator)) {
		return "~/" + filepath.ToSlash(strings.TrimPrefix(path, m.home+string(filepath.Separator)))
	}
	return path
}

// sessionPathState is what the Project field resolves to.
type sessionPathState struct {
	value     string
	isPath    bool
	bare      bool // a bare name, placed under the folder that holds most projects
	project   string
	machine   string
	node      *protocol.NodeView
	local     bool
	candidate string // the folder that is sent to the daemon
	exists    bool
	isGit     bool
	known     bool
	create    bool
	why       string
	note      string
}

// sessionPathState resolves the Project field: a known project, or a path.
func (m Model) sessionPathState(f sessionForm) sessionPathState {
	st := sessionPathState{value: f.value(sProject)}
	if st.value == "" {
		return st
	}
	for _, r := range st.value {
		if unicode.IsControl(r) {
			st.why = "the path has a control character"
			return st
		}
	}
	project, machine := splitProjectMachine(st.value)
	st.project, st.machine = project, machine
	if node := m.nodeByIDOrHost(machine); node != nil {
		st.node = node
	} else if node := m.nodeByIDOrHost(f.nodeID); node != nil {
		st.node = node
	}
	st.local = st.node == nil || st.node.NodeID == m.snap.NodeID

	knownName := strings.EqualFold(st.value, f.initialProject)
	for _, p := range m.projectRows() {
		if !strings.EqualFold(p.Name, project) {
			continue
		}
		if machine != "" && !strings.EqualFold(p.Hostname, machine) && p.NodeID != machine {
			continue
		}
		knownName = true
		break
	}
	if !knownName {
		// A project can be known from a session before it is in the registry.
		for _, a := range m.snap.Agents {
			if protocol.IsOwner(a.Harness) || !strings.EqualFold(firstNonEmpty(a.Repo.Project, a.Profile.Project), project) {
				continue
			}
			if st.node != nil && a.NodeID != st.node.NodeID {
				continue
			}
			knownName = true
			break
		}
	}
	explicit := strings.HasPrefix(st.value, "/") || strings.HasPrefix(st.value, "~") ||
		strings.HasPrefix(project, "/") || strings.HasPrefix(project, "~")
	switch {
	case explicit:
		st.isPath = true
		st.candidate = project
	case strings.ContainsAny(project, `/\`):
		st.isPath = true
		st.why = "the path must be absolute or start with ~"
		return st
	case !knownName && project != "" && !strings.ContainsAny(project, " \t"):
		// Open question 5: a bare name that is no project goes under the
		// folder that holds most registered projects on that machine.
		st.isPath, st.bare = true, true
		if parent := m.sessionProjectParent(st.node); parent != "" {
			st.candidate = filepath.Join(parent, project)
		} else {
			st.candidate = "~/code/" + project
		}
	default:
		return st
	}
	if st.machine != "" && m.nodeByIDOrHost(st.machine) == nil {
		return st
	}
	if !st.local {
		// Only the machine that holds the folder can see it. It applies the
		// same path, home and create checks there.
		return st
	}
	st.candidate = m.expandHome(st.candidate)
	if !filepath.IsAbs(st.candidate) {
		st.why = "the path must be absolute or start with ~"
		return st
	}
	st.candidate = filepath.Clean(st.candidate)
	if m.outsideHome(st.candidate) {
		st.why = "the path is outside your home folder"
		return st
	}
	st.exists, st.isGit = folderState(st.candidate, m.home)
	if st.exists {
		st.known = m.knownProjectPath(st.candidate)
	}
	switch {
	case !st.exists:
		st.create = true
		if st.bare {
			st.note = "! new folder: " + m.tildePath(st.candidate) + ", created with git init"
		} else {
			st.note = "! new folder: it will be created with git init"
		}
	case !st.isGit:
		st.note = "not a git repository: no worktrees, no branch shown"
	case !st.known:
		st.note = "not a known project yet: it will be registered"
	}
	return st
}

// sessionPathTrustNote is the Claude Code trust note of a typed path.
func (m Model) sessionPathTrustNote(st sessionPathState, harness string) string {
	if !st.isPath || st.why != "" || !st.exists || st.create || !st.local {
		return ""
	}
	if !strings.EqualFold(harness, "claude") {
		return ""
	}
	trusted, err := claudetrust.Trusted(st.candidate)
	if err != nil || trusted {
		return ""
	}
	return "! Claude Code has not trusted this folder: it will ask"
}

// knownProjectPath reports whether a folder is a project agent-net knows.
func (m Model) knownProjectPath(path string) bool {
	clean := filepath.Clean(path)
	for _, p := range m.projectRows() {
		if p.Path == "" {
			continue
		}
		if filepath.Clean(p.Path) == clean {
			return true
		}
		if real, err := filepath.EvalSymlinks(p.Path); err == nil && real == clean {
			return true
		}
	}
	return false
}

// sessionProjectParent is the folder that holds most registered projects on
// the machine (open question 5), or "" when no project is known there.
func (m Model) sessionProjectParent(node *protocol.NodeView) string {
	counts := map[string]int{}
	for _, p := range m.projectRows() {
		if p.Path == "" {
			continue
		}
		if node != nil && p.NodeID != node.NodeID {
			continue
		}
		counts[filepath.Dir(filepath.Clean(p.Path))]++
	}
	var best string
	bestN := 0
	dirs := make([]string, 0, len(counts))
	for dir := range counts {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		if counts[dir] > bestN {
			best, bestN = dir, counts[dir]
		}
	}
	return best
}

// folderState says whether a folder exists and is inside a git working tree.
// The search stops at the home folder: a repository above home is not the
// folder's project.
func folderState(path, home string) (exists, isGit bool) {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return false, false
	}
	return true, hasGitRoot(path, home)
}

func hasGitRoot(path, home string) bool {
	for {
		if _, err := os.Lstat(filepath.Join(path, ".git")); err == nil {
			return true
		}
		if home != "" && path == home {
			return false
		}
		parent := filepath.Dir(path)
		if parent == path {
			return false
		}
		path = parent
	}
}

// outsideHome applies the home-folder rule to a local path, including a link
// that leaves home and a missing folder under such a link.
func (m Model) outsideHome(path string) bool {
	if m.home == "" {
		return false
	}
	if !insideFolderTUI(m.home, path) {
		return true
	}
	if _, err := os.Lstat(path); err == nil {
		real, err := filepath.EvalSymlinks(path)
		return err != nil || !insideFolderTUI(m.home, real)
	}
	parent := filepath.Dir(path)
	for {
		if _, err := os.Lstat(parent); err == nil {
			real, err := filepath.EvalSymlinks(parent)
			return err != nil || !insideFolderTUI(m.home, real)
		}
		next := filepath.Dir(parent)
		if next == parent {
			return true
		}
		parent = next
	}
}

func insideFolderTUI(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// sessionPendingKey answers the one question a start is waiting on.
func (m Model) sessionPendingKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.sessionPending.kind == askRaiseMax {
		switch k.String() {
		case "esc":
			m.sessionPending = nil
			m.say("Start cancelled.")
			return m, nil
		case "y":
			ask := *m.sessionPending
			m.sessionPending = nil
			m.mode = modeNormal
			src, ok := m.src.(inboxCaller)
			if !ok {
				m.fail("Settings unavailable.")
				return m, nil
			}
			start := m.sessionStartCmd(ask.req)
			return m, func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), Timeout)
				defer cancel()
				_, err := src.Call(ctx, "settings_write", protocol.SettingsWrite{Changes: []protocol.SettingsChange{*ask.change}, Reason: "Raise max agents and start"})
				if err != nil {
					return sessionStartedMsg{req: ask.req, err: err}
				}
				return start()
			}
		default:
			return m, nil
		}
	}
	yes, no := confirmAnswer(k)
	if !yes && !no {
		return m, nil
	}
	ask := *m.sessionPending
	m.sessionPending = nil
	if no {
		if ask.kind == askTrustClaude {
			m.say("Not started: Claude Code does not trust this folder.")
		} else {
			m.say("Not created.")
		}
		return m, nil
	}
	req := ask.req
	switch ask.kind {
	case askCreateFolder:
		req.Create = true
		m.say("Creating " + sanitize(ask.display) + " on " + sanitize(ask.host) + "...")
	case askTrustClaude:
		req.TrustClaude = true
		m.say("Starting " + sanitize(ask.display) + "...")
	}
	m.mode = modeNormal
	return m, m.sessionStartCmd(req)
}
