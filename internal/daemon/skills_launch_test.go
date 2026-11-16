package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The launch side of decision 0169, task 4: every harness agent-net starts
// gets its node's effective skills from the daemon's own folder, and nothing is
// written into the project. The folder build itself is covered in
// skills_folder_test.go; here a fake gateway answers the launch read with one
// skill and records the target.

// skillLaunchGateway answers skills.folder with one skill and records every
// target it is asked for.
func skillLaunchGateway(t *testing.T, reqs *[]protocol.SkillFolderRequest, mu *sync.Mutex) func(context.Context, string, any) (json.RawMessage, bool, error) {
	t.Helper()
	set := &skillFolderGateway{folder: protocol.SkillFolder{Level: "repo", Scope: "shop"}}
	set.folder.Skills = []protocol.EffectiveSkill{addSkill(t, set, "alpha", 1, "alpha body")}
	return func(_ context.Context, typ string, params any) (json.RawMessage, bool, error) {
		if typ == protocol.FrameSkillsFolder {
			mu.Lock()
			if req, ok := params.(protocol.SkillFolderRequest); ok {
				*reqs = append(*reqs, req)
			}
			mu.Unlock()
		}
		return set.call(context.Background(), typ, params)
	}
}

// assertSkillFolder checks the folder a launch was given: the plugin root, and
// the skill content from the archive.
func assertSkillFolder(t *testing.T, dir string) {
	t.Helper()
	if dir == "" {
		t.Fatal("the launch got no skills folder")
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude-plugin", "plugin.json")); err != nil {
		t.Fatalf("plugin form: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "skills", "alpha", "SKILL.md"))
	if err != nil || string(body) != "alpha body" {
		t.Fatalf("skill content: %v %q", err, body)
	}
}

// An owner Claude session takes the set as one --plugin-dir folder.
func TestSessionStartClaudeGetsPluginDir(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	var mu sync.Mutex
	var reqs []protocol.SkillFolderRequest
	c := startClaudeNodeWith(t, g, shortSet, func(cfg *Config) {
		cfg.testGatewayCall = skillLaunchGateway(t, &reqs, &mu)
	})
	repo := newRepo(t, "shop")
	c.register(t, repo)
	trustClaudeFolder(t, repo)
	t.Setenv("AGENT_NET_CLAUDE_SESSIONS_DIR", c.sessions)
	var r sessionStartTestResult
	mustJSON(t, c.cli.mustCall("session_start", map[string]any{"project": "shop", "harness": "claude"}), &r)
	t.Cleanup(func() {
		raw, _ := os.ReadFile(filepath.Join(repo, "fake-claude.json"))
		var rec fakeClaudeRecord
		if json.Unmarshal(raw, &rec) == nil && rec.Pgid > 1 {
			syscall.Kill(-rec.Pgid, syscall.SIGKILL)
		}
	})
	eventually(t, "started Claude session", func() bool {
		mustJSON(t, c.cli.mustCall("session_start_get", map[string]string{"id": r.ID}), &r)
		if r.Status == "failed" {
			t.Fatalf("start failed: %+v\n%s", r, strings.Join(c.logs.lines(), "\n"))
		}
		return r.Status == "started" && r.SessionID != ""
	})
	rec := readClaude(t, repo)
	assertSkillFolder(t, argValue(rec.Args, "--plugin-dir"))
	mu.Lock()
	got := append([]protocol.SkillFolderRequest(nil), reqs...)
	mu.Unlock()
	if len(got) != 1 || got[0].Repo != "shop" || got[0].Agent != "" || got[0].By != "owner" {
		t.Fatalf("launch targets: %+v", got)
	}
}

// A Claude worker takes the same folder.
func TestClaudeWorkerGetsPluginDir(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	var mu sync.Mutex
	var reqs []protocol.SkillFolderRequest
	c := startClaudeNodeWith(t, g, shortSet, func(cfg *Config) {
		cfg.testGatewayCall = skillLaunchGateway(t, &reqs, &mu)
	})
	repo := newRepo(t, "shop")
	c.register(t, repo)

	c.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "claude", Model: "haiku", Task: "FAKE:handback say ok"})
	assertSkillFolder(t, argValue(readClaude(t, repo).Args, "--plugin-dir"))
}

// A Pi worker takes each skill folder as --skill.
func TestPiWorkerGetsSkillFlags(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	var mu sync.Mutex
	var reqs []protocol.SkillFolderRequest
	w := startPiNodeWith(t, g, shortSet, func(cfg *Config) {
		cfg.testGatewayCall = skillLaunchGateway(t, &reqs, &mu)
	})
	repo := newRepo(t, "shop")
	w.register(t, repo)

	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "pi", Model: "openai/gpt-4.1-nano", Task: "say ok", Isolation: protocol.IsolationWorktree})
	rec := readPi(t, res.Worktree)
	dir := argValue(rec.Args, "--skill")
	assertSkillFolder(t, filepath.Dir(filepath.Dir(dir)))
	if !contains(rec.Args, "--skill") || dir != filepath.Join(filepath.Dir(filepath.Dir(dir)), "skills", "alpha") {
		t.Fatalf("--skill: %v", rec.Args)
	}
}

// An OpenCode worker gets the skills in its own process config.
func TestOpenCodeWorkerGetsSkillsConfig(t *testing.T) {
	// A config the launch already carries is kept, and the skills are added.
	t.Setenv("OPENCODE_CONFIG_CONTENT", `{"theme":"dark"}`)
	g := startGateway(t, t.TempDir(), "")
	var mu sync.Mutex
	var reqs []protocol.SkillFolderRequest
	w := startWorkerNode(t, g, shortSet, func(cfg *Config) {
		cfg.testGatewayCall = skillLaunchGateway(t, &reqs, &mu)
	})
	repo := newRepo(t, "shop")
	w.register(t, repo)

	w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1", Task: "say ok"})
	rec := readFake(t, repo)
	var cfg struct {
		Theme  string   `json:"theme"`
		Skills []string `json:"skills"`
	}
	if err := json.Unmarshal([]byte(rec.Env["OPENCODE_CONFIG_CONTENT"]), &cfg); err != nil {
		t.Fatalf("OPENCODE_CONFIG_CONTENT %q: %v", rec.Env["OPENCODE_CONFIG_CONTENT"], err)
	}
	if cfg.Theme != "dark" || len(cfg.Skills) != 1 {
		t.Fatalf("merged config: %q", rec.Env["OPENCODE_CONFIG_CONTENT"])
	}
	assertSkillFolder(t, filepath.Dir(cfg.Skills[0]))
}

// A Codex worker gets the skills root on its own app server.
func TestCodexWorkerGetsSkillRoots(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	var mu sync.Mutex
	var reqs []protocol.SkillFolderRequest
	w := startWorkerNode(t, g, shortSet, func(cfg *Config) {
		cfg.HarnessPaths = map[string]string{"codex": os.Args[0]}
		cfg.testGatewayCall = skillLaunchGateway(t, &reqs, &mu)
	})
	repo := newRepo(t, "shop")
	w.register(t, repo)

	w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "codex", Model: "gpt-fake", Task: "say ok"})
	rec := readCodex(t, repo, func(rec fakeCodexRecord) bool { return len(rec.SkillRoots) > 0 })
	if len(rec.SkillRoots) != 1 {
		t.Fatalf("skill roots: %v", rec.SkillRoots)
	}
	assertSkillFolder(t, filepath.Dir(rec.SkillRoots[0]))
	if filepath.Base(rec.SkillRoots[0]) != "skills" {
		t.Fatalf("the root is not the set's skills folder: %v", rec.SkillRoots)
	}
}

// A resume asks for the session's own node, not its project's: the gateway
// resolves the session to its Agent, and a worker that changed Agent keeps the
// skills of that Agent.
func TestClaudeResumeAsksTheSessionNode(t *testing.T) {
	g := &skillFolderGateway{folder: protocol.SkillFolder{Level: "agent", Scope: "ag"}}
	g.folder.Skills = []protocol.EffectiveSkill{addSkill(t, g, "alpha", 1, "alpha body")}
	d, _ := skillFolderDaemon(t, g)
	spec := d.claudeResumeSpec(protocol.AgentView{SessionID: "s1", NativeID: "n1", Cwd: t.TempDir()})
	assertSkillFolder(t, argValue(spec.Flags, "--plugin-dir"))
	if len(g.reqs) != 1 || g.reqs[0].Session != "s1" || g.reqs[0].Repo != "" {
		t.Fatalf("resume target: %+v", g.reqs)
	}
}

// The owner's shared Codex app server is one server for every thread, and
// skills/extraRoots/set has no thread argument. A call from one owner session
// would replace the roots of every other session there, so owner sessions are
// left alone: they keep the skills of the user folder, like a hand-started one
// (decision 0169). Only a worker's own app server is told.
func TestOwnerCodexSessionsLeaveSharedSkillRootsAlone(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	f := startFakeShared(t)
	bins := fakeBin(t, "codex")
	// Each project has its own set, so a shared-server call would visibly
	// replace one project's skills with the other's.
	store := &skillFolderGateway{}
	sets := map[string]protocol.EffectiveSkill{
		"shop-one": addSkill(t, store, "alpha", 1, "shop one body"),
		"shop-two": addSkill(t, store, "alpha", 2, "shop two body"),
	}
	// The fake reads a bundle's hash from this list.
	store.folder.Skills = []protocol.EffectiveSkill{sets["shop-one"], sets["shop-two"]}
	var mu sync.Mutex
	var asked []string
	n := startWorkerNode(t, g, shortSet, func(c *Config) {
		c.HarnessPaths = bins
		c.CodexHome = f.home
		c.CodexInterval = 20 * time.Millisecond
		c.testGatewayCall = func(_ context.Context, typ string, params any) (json.RawMessage, bool, error) {
			if typ != protocol.FrameSkillsFolder {
				return store.call(context.Background(), typ, params)
			}
			req, _ := params.(protocol.SkillFolderRequest)
			mu.Lock()
			asked = append(asked, req.Repo)
			mu.Unlock()
			set := protocol.SkillFolder{Level: "repo", Scope: req.Repo}
			if skill, ok := sets[req.Repo]; ok {
				set.Skills = []protocol.EffectiveSkill{skill}
			}
			raw, _ := json.Marshal(set)
			return raw, true, nil
		}
	})
	for _, repo := range []string{newRepo(t, "shop-one"), newRepo(t, "shop-two")} {
		n.register(t, repo)
		var r protocol.StartedSession
		mustJSON(t, n.cli.mustCall("session_start", protocol.SessionStartRequest{Project: repo, Harness: "codex"}), &r)
		if r.Status != "started" || r.SessionID == "" {
			t.Fatalf("start %s: %+v", repo, r)
		}
	}
	if calls := f.extraRootCalls(); calls != 0 {
		t.Fatalf("an owner Codex session set skill roots on the shared server: %d call(s)", calls)
	}
	if len(asked) != 0 {
		t.Fatalf("an owner Codex session read a skill set it cannot receive: %v", asked)
	}
}
