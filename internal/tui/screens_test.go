package tui

import (
	"flag"
	"os"
	osexec "os/exec"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

var screens = flag.String("screens", "", "write coloured golden scenes to this directory")

func TestScreens(t *testing.T) {
	if *screens == "" || os.Getenv("AGENT_NET_SCREEN_CHILD") != "" {
		t.Skip("use -screens to render scenes")
	}
	dir, err := filepath.Abs(*screens)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	cmd := osexec.Command(os.Args[0], "-test.run", "^(TestGraphGoldens|TestQuotaViewGoldens|TestConversationScreens|TestQuotaLiveFixtureGoldens|TestLayoutGoldens|TestCTOLongErrorGolden|TestCTOMarkdownGoldens|TestViewGoldenNeeds|TestGoldenMainWithUsage|TestGoldenDetailWithTopProcesses|TestGoldenStaleUsage|TestGoldenOpenCodeSharedTree|TestGoldenHelp|TestGoldenFeedSnippets|TestGoldenMap|TestGoldenMapNavigation|TestGoldenMapCard|TestGoldenLargeOverview|TestViewGolden80|TestViewGolden120|TestViewGoldenOfflineAndFilter|TestViewGoldenFeedFocusAndBody|TestDaemonDownBanner|TestGatewayOfflineBanner|TestPolicyFormGolden|TestActionScreensGolden|TestGoldenConversation|TestTreeGolden|TestToolsTabGoldens|TestToolsPlanGoldens)$", "-screens", dir)
	cmd.Env = append(os.Environ(), "AGENT_NET_SCREEN_CHILD=1", "GORACE=atexit_sleep_ms=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("render scenes: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "cto_long_error_80x24.ansi")); err != nil {
		t.Fatalf("missing long CTO error preview: %v", err)
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.ansi"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no scenes: %v", err)
	}
}

func writeScreen(t *testing.T, name, got string) {
	t.Helper()
	if os.Getenv("AGENT_NET_SCREEN_CHILD") == "" {
		return
	}
	if err := os.WriteFile(filepath.Join(*screens, name+".ansi"), []byte(got), 0644); err != nil {
		t.Fatal(err)
	}
}

func initScreenColors() {
	if os.Getenv("AGENT_NET_SCREEN_CHILD") != "" {
		lipgloss.SetColorProfile(termenv.TrueColor)
		lipgloss.SetHasDarkBackground(true)
	}
}
