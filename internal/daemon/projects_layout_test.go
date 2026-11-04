package daemon

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func initTestGitRepo(t *testing.T, dir, remote string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %v\n%s", args, dir, err, string(out))
		}
	}
	run("init")
	run("config", "user.name", "Test User")
	run("config", "user.email", "test@example.com")
	if remote != "" {
		run("config", "remote.origin.url", remote)
	}
	run("commit", "--allow-empty", "-m", "init")
}

func TestProjectsLayoutScanAndNoiseDrop(t *testing.T) {
	tempHome := t.TempDir()

	// 1. Grouped root under ~/code
	repoA := filepath.Join(tempHome, "code", "niparis", "a2a-ocs")
	initTestGitRepo(t, repoA, "git@github.com:niparis/a2a-ocs.git")

	repoB := filepath.Join(tempHome, "code", "code-artisans", "flood-wargame")
	initTestGitRepo(t, repoB, "https://github.com/code-artisans/flood-wargame.git")

	repoC := filepath.Join(tempHome, "code", "homelab") // loose project at depth 1 in ~/code
	initTestGitRepo(t, repoC, "https://github.com/niparis/homelab.git")

	// 2. Flat root under ~/projects
	repoFlat1 := filepath.Join(tempHome, "projects", "app1")
	initTestGitRepo(t, repoFlat1, "git@gitlab.com:myorg/app1.git")

	repoFlat2 := filepath.Join(tempHome, "projects", "app2")
	initTestGitRepo(t, repoFlat2, "git@gitlab.com:myorg/app2.git")

	// 3. Isolated single repo (must NOT become a root)
	repoSingle := filepath.Join(tempHome, "single", "lonely")
	initTestGitRepo(t, repoSingle, "https://github.com/niparis/lonely.git")

	// 4. Noise: Home folder itself has a .git folder (must not be treated as a project)
	if err := os.MkdirAll(filepath.Join(tempHome, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	// 5. Noise: Git worktree (a .git FILE, not directory)
	wtDir := filepath.Join(tempHome, "code", "niparis", "my-worktree")
	if err := os.MkdirAll(wtDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wtDir, ".git"), []byte("gitdir: /fake/path\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 6. Noise: Bare .git file in another folder
	bareDir := filepath.Join(tempHome, "other", "bare-file")
	if err := os.MkdirAll(bareDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bareDir, ".git"), []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 7. Noise: node_modules with a .git repo inside
	nmRepo := filepath.Join(tempHome, "code", "niparis", "a2a-ocs", "node_modules", "some-pkg")
	initTestGitRepo(t, nmRepo, "")

	// 8. Noise: .claude/worktrees
	claudeWt := filepath.Join(tempHome, ".claude", "worktrees", "wt-1")
	initTestGitRepo(t, claudeWt, "")

	// 9. Noise: agent-net worker worktree path
	anWt := filepath.Join(tempHome, ".local", "state", "agent-net", "daemon", "worktrees", "w_123")
	initTestGitRepo(t, anWt, "")

	d := &Daemon{
		cfg: Config{
			HomeDir:  tempHome,
			Hostname: "test-box",
		},
	}

	layout, err := d.ProjectsLayoutLocal(context.Background())
	if err != nil {
		t.Fatalf("ProjectsLayoutLocal: %v", err)
	}

	rawJSON, _ := json.MarshalIndent(layout, "", "  ")
	t.Logf("Tool output on test fixture:\n%s", string(rawJSON))

	if layout.Machine != "test-box" {
		t.Errorf("Machine = %q, want test-box", layout.Machine)
	}
	if layout.Home != tempHome {
		t.Errorf("Home = %q, want %q", layout.Home, tempHome)
	}
	if layout.Truncated {
		t.Errorf("Truncated is true, want false")
	}

	if len(layout.Roots) != 2 {
		t.Fatalf("got %d roots, want 2: %+v", len(layout.Roots), layout.Roots)
	}

	// Root 1: ~/code (grouped)
	codeRoot := layout.Roots[0]
	if codeRoot.Path != "~/code" {
		t.Errorf("Roots[0].Path = %q, want ~/code", codeRoot.Path)
	}
	if codeRoot.Kind != "grouped" {
		t.Errorf("Roots[0].Kind = %q, want grouped", codeRoot.Kind)
	}
	if len(codeRoot.Groups) != 2 {
		t.Fatalf("Roots[0].Groups has %d groups, want 2: %+v", len(codeRoot.Groups), codeRoot.Groups)
	}

	// Group "code-artisans"
	grp0 := codeRoot.Groups[0]
	if grp0.Name != "code-artisans" || grp0.Path != "~/code/code-artisans" || grp0.Projects != 1 {
		t.Errorf("Group 0 mismatch: %+v", grp0)
	}
	if len(grp0.Owners) != 1 || grp0.Owners[0] != "code-artisans (1)" {
		t.Errorf("Group 0 owners = %v, want ['code-artisans (1)']", grp0.Owners)
	}
	if grp0.LastCommit <= 0 {
		t.Errorf("Group 0 LastCommit = %d, want > 0", grp0.LastCommit)
	}

	// Group "niparis"
	grp1 := codeRoot.Groups[1]
	if grp1.Name != "niparis" || grp1.Path != "~/code/niparis" || grp1.Projects != 1 {
		t.Errorf("Group 1 mismatch: %+v", grp1)
	}
	if len(grp1.Owners) != 1 || grp1.Owners[0] != "niparis (1)" {
		t.Errorf("Group 1 owners = %v, want ['niparis (1)']", grp1.Owners)
	}

	// Loose project in ~/code
	if len(codeRoot.Projects) != 1 {
		t.Fatalf("Roots[0].Projects has %d projects, want 1 (homelab): %+v", len(codeRoot.Projects), codeRoot.Projects)
	}
	pLoose := codeRoot.Projects[0]
	if pLoose.Name != "homelab" || pLoose.Path != "~/code/homelab" {
		t.Errorf("Loose project = %+v, want homelab", pLoose)
	}

	// Root 2: ~/projects (flat)
	projRoot := layout.Roots[1]
	if projRoot.Path != "~/projects" {
		t.Errorf("Roots[1].Path = %q, want ~/projects", projRoot.Path)
	}
	if projRoot.Kind != "flat" {
		t.Errorf("Roots[1].Kind = %q, want flat", projRoot.Kind)
	}
	if len(projRoot.Groups) != 0 {
		t.Errorf("Roots[1].Groups has %d groups, want 0", len(projRoot.Groups))
	}
	if len(projRoot.Projects) != 2 {
		t.Fatalf("Roots[1].Projects has %d projects, want 2: %+v", len(projRoot.Projects), projRoot.Projects)
	}
	if projRoot.Projects[0].Name != "app1" || projRoot.Projects[0].Path != "~/projects/app1" {
		t.Errorf("app1 project mismatch: %+v", projRoot.Projects[0])
	}
	if projRoot.Projects[1].Name != "app2" || projRoot.Projects[1].Path != "~/projects/app2" {
		t.Errorf("app2 project mismatch: %+v", projRoot.Projects[1])
	}

	// Test 10-minute caching
	cached, err := d.ProjectsLayoutLocal(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cached.ScannedAt != layout.ScannedAt {
		t.Errorf("cached ScannedAt = %d, want %d", cached.ScannedAt, layout.ScannedAt)
	}

	// After invalidation, rescan happens
	d.InvalidateLayoutCache()
	if d.layoutCache != nil {
		t.Errorf("layoutCache not nil after InvalidateLayoutCache")
	}
}

func TestCTOProjectsLayout(t *testing.T) {
	tempHome := t.TempDir()
	repoA := filepath.Join(tempHome, "code", "niparis", "a2a-ocs")
	initTestGitRepo(t, repoA, "git@github.com:niparis/a2a-ocs.git")
	repoB := filepath.Join(tempHome, "code", "niparis", "flood")
	initTestGitRepo(t, repoB, "git@github.com:niparis/flood.git")

	d := &Daemon{
		cfg: Config{
			HomeDir:  tempHome,
			Hostname: "box-test",
		},
	}

	val, err := d.readCTOTool(context.Background(), "projects_layout", []byte(`{}`))
	if err != nil {
		t.Fatalf("readCTOTool projects_layout: %v", err)
	}
	encoded, err := json.Marshal(val)
	if err != nil {
		t.Fatal(err)
	}
	var res protocol.ProjectsLayoutResult
	if err := json.Unmarshal(encoded, &res); err != nil {
		t.Fatalf("unmarshal result: %v (raw: %s)", err, string(encoded))
	}
	if len(res.Machines) != 1 {
		t.Fatalf("got %d machines, want 1: %+v", len(res.Machines), res.Machines)
	}
	m := res.Machines[0]
	if m.Machine != "box-test" || len(m.Roots) != 1 || m.Roots[0].Path != "~/code" {
		t.Errorf("machine layout mismatch: %+v", m)
	}
}

func TestProjectsLayoutNonNilRoots(t *testing.T) {
	// 1. One-repo machine
	tempHome1 := t.TempDir()
	initTestGitRepo(t, filepath.Join(tempHome1, "solo", "myrepo"), "git@github.com:test/myrepo.git")

	d1 := &Daemon{
		cfg: Config{
			HomeDir:  tempHome1,
			Hostname: "box-solo",
		},
	}
	layout1, err := d1.ProjectsLayoutLocal(context.Background())
	if err != nil {
		t.Fatalf("ProjectsLayoutLocal 1: %v", err)
	}
	if layout1.Roots == nil {
		t.Fatalf("layout1.Roots is nil for one-repo machine")
	}
	data1, _ := json.Marshal(layout1)
	if strings.Contains(string(data1), `"roots":null`) {
		t.Fatalf("JSON marshaled roots as null for one-repo machine: %s", string(data1))
	}

	// 2. Machine with only top-level repos directly under home
	tempHome2 := t.TempDir()
	initTestGitRepo(t, filepath.Join(tempHome2, "repo1"), "git@github.com:test/repo1.git")
	initTestGitRepo(t, filepath.Join(tempHome2, "repo2"), "git@github.com:test/repo2.git")

	d2 := &Daemon{
		cfg: Config{
			HomeDir:  tempHome2,
			Hostname: "box-toplevel",
		},
	}
	layout2, err := d2.ProjectsLayoutLocal(context.Background())
	if err != nil {
		t.Fatalf("ProjectsLayoutLocal 2: %v", err)
	}
	if layout2.Roots == nil {
		t.Fatalf("layout2.Roots is nil for top-level repos machine")
	}
	data2, _ := json.Marshal(layout2)
	if strings.Contains(string(data2), `"roots":null`) {
		t.Fatalf("JSON marshaled roots as null for top-level repos machine: %s", string(data2))
	}
}

func TestProjectsLayoutLoneAndHomeRepos(t *testing.T) {
	// 1. Lone repos: ~/toprepo, ~/code/solo, ~/deep/x/y/z
	tempHome := t.TempDir()
	initTestGitRepo(t, filepath.Join(tempHome, "toprepo"), "git@github.com:test/toprepo.git")
	initTestGitRepo(t, filepath.Join(tempHome, "code", "solo"), "git@github.com:test/solo.git")
	initTestGitRepo(t, filepath.Join(tempHome, "deep", "x", "y", "z"), "git@github.com:test/z.git")

	d := &Daemon{
		cfg: Config{
			HomeDir:  tempHome,
			Hostname: "box-lone",
		},
	}
	layout, err := d.ProjectsLayoutLocal(context.Background())
	if err != nil {
		t.Fatalf("ProjectsLayoutLocal: %v", err)
	}

	if len(layout.Roots) != 0 {
		t.Errorf("got %d roots, want 0: %+v", len(layout.Roots), layout.Roots)
	}
	if len(layout.Loose) != 3 {
		t.Fatalf("got %d loose repos, want 3: %+v", len(layout.Loose), layout.Loose)
	}
	wantPaths := []string{"~/code/solo", "~/deep/x/y/z", "~/toprepo"}
	for i, want := range wantPaths {
		if layout.Loose[i].Path != want {
			t.Errorf("Loose[%d].Path = %q, want %q", i, layout.Loose[i].Path, want)
		}
	}

	// 2. Direct home repos: ~/p1 and ~/p2 form a flat root at ~
	tempHome2 := t.TempDir()
	initTestGitRepo(t, filepath.Join(tempHome2, "p1"), "git@github.com:test/p1.git")
	initTestGitRepo(t, filepath.Join(tempHome2, "p2"), "git@github.com:test/p2.git")

	d2 := &Daemon{
		cfg: Config{
			HomeDir:  tempHome2,
			Hostname: "box-flat-home",
		},
	}
	layout2, err := d2.ProjectsLayoutLocal(context.Background())
	if err != nil {
		t.Fatalf("ProjectsLayoutLocal 2: %v", err)
	}
	if len(layout2.Roots) != 1 {
		t.Fatalf("got %d roots, want 1: %+v", len(layout2.Roots), layout2.Roots)
	}
	homeRoot := layout2.Roots[0]
	if homeRoot.Path != "~" || homeRoot.Kind != "flat" {
		t.Errorf("homeRoot mismatch: Path=%q Kind=%q", homeRoot.Path, homeRoot.Kind)
	}
	if len(homeRoot.Projects) != 2 {
		t.Fatalf("homeRoot.Projects count = %d, want 2: %+v", len(homeRoot.Projects), homeRoot.Projects)
	}
	if homeRoot.Projects[0].Name != "p1" || homeRoot.Projects[1].Name != "p2" {
		t.Errorf("homeRoot projects mismatch: %+v", homeRoot.Projects)
	}
	if len(layout2.Loose) != 0 {
		t.Errorf("layout2.Loose count = %d, want 0: %+v", len(layout2.Loose), layout2.Loose)
	}
}

func TestProjectsLayoutGitWaitDelay(t *testing.T) {
	fakeBinDir := t.TempDir()
	fakeGit := filepath.Join(fakeBinDir, "git")
	// Fake git that spawns a background child inheriting stdout and sleeping
	script := "#!/bin/sh\n(sleep 5 >&1) &\nexit 0\n"
	if err := os.WriteFile(fakeGit, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBinDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_ = readRepoRemote(ctx, t.TempDir())
	elapsed := time.Since(start)
	if elapsed > 1*time.Second {
		t.Fatalf("readRepoRemote took %v; bounds did not hold", elapsed)
	}
}

func TestExtractRemoteOwner(t *testing.T) {
	cases := []struct {
		remote string
		want   string
	}{
		{"file:///home/x/repos/foo", ""},
		{"file://localhost/tmp/repo.git", ""},
		{"/home/x/repos/foo", ""},
		{"../relative/repo.git", ""},
		{"https://github.com/niparis/a2a-ocs.git", "niparis"},
		{"git@github.com:niparis/a2a-ocs.git", "niparis"},
		{"https://gitlab.com/myorg/app1.git", "gitlab.com/myorg"},
		{"git@gitlab.com:myorg/app1.git", "gitlab.com/myorg"},
		{"", ""},
	}
	for _, tc := range cases {
		got := extractRemoteOwner(tc.remote)
		if got != tc.want {
			t.Errorf("extractRemoteOwner(%q) = %q, want %q", tc.remote, got, tc.want)
		}
	}
}
