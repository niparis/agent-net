package setup

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/guidance"
)

func codexFixture(t *testing.T) (*fixture, string) {
	t.Helper()
	f := newFixture(t)
	dir := filepath.Join(f.home, ".codex")
	os.MkdirAll(dir, 0o755)
	return f, filepath.Join(dir, "config.toml")
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return string(b)
}

const codexMine = "model = \"gpt-x\"\n\n[projects.\"/work\"]\ntrust_level = \"trusted\"\n"

func TestCodexMCPPromptYes(t *testing.T) {
	f, cfg := codexFixture(t)
	os.WriteFile(cfg, []byte(codexMine), 0o600)
	answer(t, "y\n")
	out := run(t, Options{Join: f.newJoin(t)})
	for _, want := range []string{"Let Codex call agent-net tools without asking each time? [y/N]", "codex mcp ok (calls need no approval)", "codex guidance ok"} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
	want := codexMine + "\n" + codexBegin + "\n[mcp_servers.agent-net]\ncommand = '/opt/test/agent-net'\nargs = [\"mcp\", \"--codex\"]\ndefault_tools_approval_mode = \"approve\"\n" + codexEnd + "\n"
	if got := readFile(t, cfg); got != want {
		t.Errorf("config:\n%s\nwant:\n%s", got, want)
	}
	if st, _ := os.Stat(cfg); st.Mode().Perm() != 0o600 {
		t.Errorf("mode changed to %v", st.Mode())
	}
	if am := readFile(t, filepath.Join(f.home, ".codex", "AGENTS.md")); am != guidance.Block(guidance.Other) {
		t.Errorf("AGENTS.md:\n%s", am)
	}
	// A rerun asks nothing and changes nothing.
	noTTY(t)
	out = run(t, Options{Join: f.newJoin(t)})
	if !strings.Contains(out, "codex mcp ok (unchanged; calls need no approval)") || readFile(t, cfg) != want {
		t.Errorf("rerun:\n%s\n%s", out, readFile(t, cfg))
	}
}

func TestCodexMCPPromptNoAndNoTerminal(t *testing.T) {
	f, cfg := codexFixture(t)
	answer(t, "n\n")
	out := run(t, Options{Join: f.newJoin(t)})
	if !strings.Contains(out, "codex mcp ok (asks before each call)") {
		t.Errorf("output:\n%s", out)
	}
	got := readFile(t, cfg)
	if !strings.Contains(got, "args = [\"mcp\", \"--codex\"]") || strings.Contains(got, "default_tools_approval_mode") {
		t.Errorf("config:\n%s", got)
	}

	// Another machine, no terminal: the server is registered, nothing is approved,
	// and the hint names the flag.
	f2, cfg2 := codexFixture(t)
	out = run(t, Options{Join: f2.newJoin(t)})
	if !strings.Contains(out, "agent-net setup --accept-codex-tools") || strings.Contains(readFile(t, cfg2), "default_tools_approval_mode") {
		t.Errorf("no terminal:\n%s\n%s", out, readFile(t, cfg2))
	}
}

func TestCodexMCPFlagsAndUpdate(t *testing.T) {
	f, cfg := codexFixture(t)
	out := run(t, Options{Join: f.newJoin(t), CodexTools: CodexToolsApprove})
	if !strings.Contains(out, "calls need no approval") || !strings.Contains(readFile(t, cfg), "default_tools_approval_mode") {
		t.Fatalf("approve flag:\n%s\n%s", out, readFile(t, cfg))
	}
	// --no-accept-codex-tools takes the setting out again, in the same block.
	run(t, Options{Join: f.newJoin(t), CodexTools: CodexToolsNo})
	got := readFile(t, cfg)
	if strings.Contains(got, "default_tools_approval_mode") || strings.Count(got, codexBegin) != 1 {
		t.Fatalf("no flag:\n%s", got)
	}
	// Without a flag the earlier answer holds (the owner is not asked again).
	run(t, Options{Join: f.newJoin(t)})
	if strings.Contains(readFile(t, cfg), "default_tools_approval_mode") {
		t.Fatal("the answer was forgotten")
	}
}

func TestCodexMCPLeavesAHandWrittenServerAlone(t *testing.T) {
	f, cfg := codexFixture(t)
	mine := "[mcp_servers.agent-net]\ncommand = \"/my/own\"\n"
	os.WriteFile(cfg, []byte(mine), 0o644)
	out := run(t, Options{Join: f.newJoin(t)})
	if !strings.Contains(out, "codex mcp skipped (agent-net is already defined") || readFile(t, cfg) != mine {
		t.Errorf("output:\n%s\nconfig:\n%s", out, readFile(t, cfg))
	}
}

func TestCodexMCPSkippedWithoutCodex(t *testing.T) {
	f := newFixture(t)
	out := run(t, Options{Join: f.newJoin(t)})
	if !strings.Contains(out, "codex mcp skipped (codex not found)") {
		t.Errorf("output:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(f.home, ".codex")); err == nil {
		t.Error("setup made ~/.codex")
	}
}

func TestCodexUninstallRemovesOnlyTheBlock(t *testing.T) {
	f, cfg := codexFixture(t)
	os.WriteFile(cfg, []byte(codexMine), 0o600)
	run(t, Options{Join: f.newJoin(t), CodexTools: CodexToolsApprove})
	var buf bytes.Buffer
	if err := Uninstall(Options{Out: &buf, Exe: "/x"}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, cfg); got != codexMine {
		t.Errorf("config after uninstall:\n%q\nwant:\n%q", got, codexMine)
	}
	if !strings.Contains(buf.String(), "codex mcp") || !strings.Contains(buf.String(), "ok (removed)") {
		t.Errorf("output:\n%s", buf.String())
	}
	if am := readFile(t, filepath.Join(f.home, ".codex", "AGENTS.md")); strings.Contains(am, guidance.Begin) {
		t.Errorf("guidance left:\n%s", am)
	}
	if _, err := os.Stat(codexStatePath()); err == nil {
		t.Error("state file left")
	}
}

func TestTomlString(t *testing.T) {
	for in, want := range map[string]string{
		"/opt/x/agent-net": `'/opt/x/agent-net'`,
		"/o'dd/agent-net":  `"/o'dd/agent-net"`,
		`/a"b\c'd`:         `"/a\"b\\c'd"`,
	} {
		if got := tomlString(in); got != want {
			t.Errorf("tomlString(%q) = %s, want %s", in, got, want)
		}
	}
}
