package setup

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/agentfiles"
)

func claudeCalls(t *testing.T, logPath string) []string {
	t.Helper()
	b, err := os.ReadFile(logPath)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func callsContain(calls []string, want string) bool {
	for _, c := range calls {
		if c == want {
			return true
		}
	}
	return false
}

func TestClaudeWorkersModIsAskedAndInstalledOnYes(t *testing.T) {
	f := newFixture(t)
	logPath := fakeClaude(t, f.home)
	answer(t, "y\n")
	out := runAskingAll(t, Options{Join: f.newJoin(t), PeerMessages: PeerMessagesNo, HintHook: HintHookNo, PolicyHook: PolicyHookNo})
	for _, want := range []string{"Install the plugin? [y/N]", "claude workers mod on (plugin agent-net-workers@agent-net, user scope)"} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(agentfiles.ClaudeWorkersDir(), ".claude-plugin", "marketplace.json")); err != nil {
		t.Error("marketplace.json is missing")
	}
	if _, err := os.Stat(filepath.Join(agentfiles.ClaudeWorkersPluginDir(), "hooks", "register.tsx")); err != nil {
		t.Error("the plugin was not written")
	}
	if _, err := os.Stat(filepath.Join(agentfiles.ClaudeWorkersPluginDir(), "hooks", "register.test.tsx")); err == nil {
		t.Error("the test file was written")
	}
	st := loadWorkersModState()
	if !st.Asked || !st.Installed || st.Dir != agentfiles.ClaudeWorkersDir() {
		t.Errorf("state: %+v", st)
	}
	calls := claudeCalls(t, logPath)
	for _, want := range []string{
		"plugin marketplace add " + agentfiles.ClaudeWorkersDir(),
		"plugin install agent-net-workers@agent-net -s user",
	} {
		if !callsContain(calls, want) {
			t.Errorf("no %q in the claude calls:\n%s", want, strings.Join(calls, "\n"))
		}
	}

	// The owner agreed once: a later run refreshes the folder, does not ask,
	// and calls the CLI again so registration is repaired if it was removed.
	noTTY(t)
	out = runAskingAll(t, Options{PeerMessages: PeerMessagesNo, HintHook: HintHookNo, PolicyHook: PolicyHookNo})
	if strings.Contains(out, "[y/N]") || !strings.Contains(out, "claude workers mod ok (unchanged)") {
		t.Errorf("second run:\n%s", out)
	}
}

func TestClaudeWorkersModDefaultsToOffAndDoesNotAskAgain(t *testing.T) {
	f := newFixture(t)
	fakeClaude(t, f.home)
	answer(t, "\n")
	out := runAskingAll(t, Options{Join: f.newJoin(t), PeerMessages: PeerMessagesNo, HintHook: HintHookNo, PolicyHook: PolicyHookNo})
	if !strings.Contains(out, "claude workers mod off") {
		t.Errorf("output:\n%s", out)
	}
	if _, err := os.Stat(agentfiles.ClaudeWorkersDir()); err == nil {
		t.Error("the marketplace was written without consent")
	}
	if st := loadWorkersModState(); !st.Asked || st.Installed {
		t.Errorf("state: %+v", st)
	}
	answer(t, "y\n")
	out = runAskingAll(t, Options{PeerMessages: PeerMessagesNo, HintHook: HintHookNo, PolicyHook: PolicyHookNo})
	if strings.Contains(out, "[y/N]") || strings.Contains(out, "claude workers mod on") {
		t.Errorf("asked or installed after a no:\n%s", out)
	}
}

func TestClaudeWorkersModWithoutATerminal(t *testing.T) {
	f := newFixture(t)
	fakeClaude(t, f.home)
	out := runAskingAll(t, Options{Join: f.newJoin(t), PeerMessages: PeerMessagesNo, HintHook: HintHookNo, PolicyHook: PolicyHookNo})
	for _, want := range []string{"claude workers mod off (default)", "agent-net setup --claude-workers-mod"} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(workersModStatePath()); err == nil {
		t.Error("a no-terminal run recorded an answer")
	}
	if _, err := os.Stat(agentfiles.ClaudeWorkersDir()); err == nil {
		t.Error("the marketplace was written without an answer")
	}

	out = runAskingAll(t, Options{PeerMessages: PeerMessagesNo, HintHook: HintHookNo, PolicyHook: PolicyHookNo, WorkersMod: WorkersModYes})
	if !strings.Contains(out, "claude workers mod on (plugin agent-net-workers@agent-net, user scope)") {
		t.Errorf("flag did not install:\n%s", out)
	}
}

// An upgrade rewrites the folder, then asks running sessions to reload it.
func TestClaudeWorkersModUpgradeRefreshesAndAsksForReload(t *testing.T) {
	f := newFixture(t)
	logPath := fakeClaude(t, f.home)
	runAskingAll(t, Options{Join: f.newJoin(t), PeerMessages: PeerMessagesNo, HintHook: HintHookNo, PolicyHook: PolicyHookNo, WorkersMod: WorkersModYes})
	register := filepath.Join(agentfiles.ClaudeWorkersPluginDir(), "hooks", "register.tsx")
	if err := os.WriteFile(register, []byte("old mod"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := runAskingAll(t, Options{PeerMessages: PeerMessagesNo, HintHook: HintHookNo, PolicyHook: PolicyHookNo})
	for _, want := range []string{"claude workers mod updated (plugin agent-net-workers@agent-net)", "hint: open Claude Code sessions: run /reload-plugins"} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
	b, err := os.ReadFile(register)
	if err != nil || string(b) == "old mod" || string(b) == "" {
		t.Errorf("register.tsx was not refreshed: %q, %v", b, err)
	}
	calls := claudeCalls(t, logPath)
	for _, want := range []string{"plugin marketplace update agent-net", "plugin update agent-net-workers@agent-net -s user"} {
		if !callsContain(calls, want) {
			t.Errorf("no %q in the claude calls:\n%s", want, strings.Join(calls, "\n"))
		}
	}
}

func TestClaudeWorkersModCLIFailureIsAStepResult(t *testing.T) {
	f := newFixture(t)
	bin := t.TempDir()
	script := "#!/bin/sh\necho \"$*\" >> '" + filepath.Join(f.home, "claude.log") + "'\n" +
		"case \"$1 $2\" in\n" +
		"  'plugin marketplace') echo 'boom'; exit 1 ;;\n" +
		"  'mcp add') touch '" + filepath.Join(f.home, "claude.log") + ".added' ;;\n" +
		"esac\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	var out bytes.Buffer
	err := Setup(context.Background(), Options{
		Join: f.newJoin(t), PeerMessages: PeerMessagesNo, HintHook: HintHookNo,
		PolicyHook: PolicyHookNo, WorkersMod: WorkersModYes, Out: &out, Exe: "/opt/test/agent-net",
	})
	if err != nil {
		t.Fatalf("setup failed on a claude error: %v\n%s", err, out.String())
	}
	if got := norm(out.String()); !strings.Contains(got, "claude workers mod files written, not registered (claude plugin marketplace add: boom)") {
		t.Errorf("output:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(agentfiles.ClaudeWorkersPluginDir(), ".claude-plugin", "plugin.json")); err != nil {
		t.Error("the files were not written")
	}
	if st := loadWorkersModState(); st.Installed {
		t.Errorf("state says installed: %+v", st)
	}
}

func TestUninstallRemovesTheClaudeWorkersMod(t *testing.T) {
	f := newFixture(t)
	logPath := fakeClaude(t, f.home)
	runAskingAll(t, Options{Join: f.newJoin(t), PeerMessages: PeerMessagesNo, HintHook: HintHookNo, PolicyHook: PolicyHookNo, WorkersMod: WorkersModYes})

	var out bytes.Buffer
	if err := Uninstall(Options{Out: &out, NoService: true}); err != nil {
		t.Fatalf("uninstall: %v\n%s", err, out.String())
	}
	if !strings.Contains(norm(out.String()), "claude workers mod ok (removed)") {
		t.Errorf("output:\n%s", out.String())
	}
	calls := claudeCalls(t, logPath)
	for _, want := range []string{
		"plugin uninstall agent-net-workers@agent-net -s user",
		"plugin marketplace remove agent-net",
	} {
		if !callsContain(calls, want) {
			t.Errorf("no %q in the claude calls:\n%s", want, strings.Join(calls, "\n"))
		}
	}
	if _, err := os.Stat(agentfiles.ClaudeWorkersDir()); err == nil {
		t.Error("the marketplace folder is still there")
	}
	if _, err := os.Stat(workersModStatePath()); err == nil {
		t.Error("the state file is still there")
	}
}

func TestUninstallSkipsTheClaudeWorkersModWhenSetupNeverWroteIt(t *testing.T) {
	f := newFixture(t)
	fakeClaude(t, f.home)
	var out bytes.Buffer
	if err := Uninstall(Options{Out: &out, NoService: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(norm(out.String()), "claude workers mod skipped (not installed)") {
		t.Errorf("output:\n%s", out.String())
	}
}
