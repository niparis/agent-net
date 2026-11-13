package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A current opencode (2.3.0) has no --variant flag: the effort rides on the
// model as model#variant. The installed binary's help decides, so the same
// guide works after an upgrade.
func TestOpenCodeRunModelFlagsUseHashOnCurrentBuild(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "opencode")
	script := "#!/bin/sh\nif [ \"$1\" = run ] && [ \"$2\" = --help ]; then echo 'opencode run'; exit 0; fi\nexit 0\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	selection := protocol.ParseModelSelection("opencode", "", "opencode-go/deepseek-v4.1-flash", "high")
	args := openCodeModelFlags(context.Background(), bin, selection)
	want := []string{"--model", "opencode-go/deepseek-v4.1-flash#high"}
	if strings.Join(args, " ") != strings.Join(want, " ") {
		t.Fatalf("current opencode flags = %q, want %q", args, want)
	}
}

// An older opencode still takes --variant. Its own help says so, and the
// builder keeps that form instead of guessing from a version number.
func TestOpenCodeRunModelFlagsKeepVariantOnOlderBuild(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "opencode")
	script := "#!/bin/sh\nif [ \"$1\" = run ] && [ \"$2\" = --help ]; then echo '--variant string'; exit 0; fi\nexit 0\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	selection := protocol.ParseModelSelection("opencode", "", "opencode-go/deepseek-v4.1-flash", "high")
	args := openCodeModelFlags(context.Background(), bin, selection)
	want := []string{"--model", "opencode-go/deepseek-v4.1-flash", "--variant", "high"}
	if strings.Join(args, " ") != strings.Join(want, " ") {
		t.Fatalf("older opencode flags = %q, want %q", args, want)
	}
}

// The judge goes through the same builder, so a judged run with an effort uses
// the form this opencode accepts.
func TestJudgeOpenCodePutsEffortOnTheModel(t *testing.T) {
	dir := t.TempDir()
	record := filepath.Join(dir, "record.json")
	reply := filepath.Join(dir, "reply.json")
	if err := os.WriteFile(reply, []byte(`{"reasoning":1,"scope":1,"coupling":1,"uncertainty":1,"verification":1,"risk":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envFakeOpenCode, "1")
	t.Setenv(envFakeOpenCodeReply, reply)
	t.Setenv("FAKE_OPENCODE_RUN_RECORD", record)
	a := testDifficultyAssessor(Config{HarnessPaths: map[string]string{"opencode": os.Args[0]}}, DifficultySources{})
	if _, err := a.runOpenCode(context.Background(), "opencode-go/deepseek-v4.1-flash:high", "prompt", 5*time.Second); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	var rec struct {
		Args []string `json:"args"`
	}
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(rec.Args, " ")
	if !strings.Contains(joined, "opencode-go/deepseek-v4.1-flash#high") {
		t.Fatalf("judge did not put the effort on the model: %q", rec.Args)
	}
	for _, arg := range rec.Args {
		if arg == "--variant" {
			t.Fatalf("judge passed --variant to a build that has no such flag: %q", rec.Args)
		}
	}
}
