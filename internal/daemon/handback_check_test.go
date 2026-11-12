package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/harnesses"
	"github.com/niparis/agent-net/internal/protocol"
)

// The project's pre-hand-back check (owner decision 2026-11-12).

func setHandbackCheck(t *testing.T, g *testGateway, project, command string, timeout string) {
	t.Helper()
	changes := []protocol.SettingsChange{{Key: "delivery.handback_check", Level: "repo", Scope: project, Value: mustRawJSON(command)}}
	if timeout != "" {
		changes = append(changes, protocol.SettingsChange{Key: "delivery.handback_check_timeout", Level: "repo", Scope: project, Value: mustRawJSON(timeout)})
	}
	if _, err := g.gw.Store().WriteSettings(changes, "owner", "test hand-back check"); err != nil {
		t.Fatal(err)
	}
}

func mustRawJSON(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return raw
}

// handbackCheckScript writes a check script in its own folder and returns the
// command line that runs it. The script lives outside the worktree, so it does
// not add a commit to the worker's branch.
func handbackCheckScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "check.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return "sh " + fmt.Sprintf("%q", path)
}

func noticeChangeID(t *testing.T, body string) string {
	t.Helper()
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "change: ch_") {
			return strings.TrimSpace(strings.TrimPrefix(line, "change: "))
		}
	}
	t.Fatalf("notice names no change:\n%s", body)
	return ""
}

func changeView(t *testing.T, g *testGateway, id string) protocol.ChangeView {
	t.Helper()
	view, err := g.gw.Store().GetChange(id)
	if err != nil {
		t.Fatalf("change %s: %v", id, err)
	}
	return view
}

func hasSignal(signals []protocol.QualitySignal, code string) bool {
	for _, s := range signals {
		if s.Code == code {
			return true
		}
	}
	return false
}

// The setting empty starts no check, and the change says so.
func TestHandbackCheckNotConfiguredRunsNothing(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, queueSet, withLoad(&fakeLoad{l: roomyLoad()}))
	repo := newRepo(t, "shop")
	w.register(t, repo)

	w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1",
		Task: "FAKE:handback FAKE:edit", Isolation: protocol.IsolationWorktree})
	n := w.waitNotice(t, 1)[0]
	if !strings.Contains(n.Body, "hand-back check: not configured") {
		t.Fatalf("notice:\n%s", n.Body)
	}
	view := changeView(t, g, noticeChangeID(t, n.Body))
	if view.HandbackCheck == nil || view.HandbackCheck.Status != protocol.HandbackCheckNotConfigured {
		t.Fatalf("change hand-back check: %+v", view.HandbackCheck)
	}
}

// A passing check is recorded on the change with its output tail and the
// parallelism the machine's test slot granted.
func TestHandbackCheckPassed(t *testing.T) {
	// The output assertion below expects only the check's granted parallelism.
	t.Setenv("GOFLAGS", "")
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, queueSet, withLoad(&fakeLoad{l: roomyLoad()}))
	repo := newRepo(t, "shop")
	w.register(t, repo)
	command := handbackCheckScript(t, "echo check ok\n"+
		"echo GOMAXPROCS=$GOMAXPROCS\n"+
		"echo GOFLAGS=$GOFLAGS\n")
	setHandbackCheck(t, g, "shop", command, "")
	eventually(t, "the daemon has the hand-back check", func() bool {
		cmd, _ := w.d.handbackCheckSettings("shop")
		return cmd == command
	})
	w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1",
		Task: "FAKE:handback FAKE:edit", Isolation: protocol.IsolationWorktree})
	n := w.waitNotice(t, 1)[0]
	if !strings.Contains(n.Body, "hand-back check: passed in ") {
		t.Fatalf("notice:\n%s", n.Body)
	}
	view := changeView(t, g, noticeChangeID(t, n.Body))
	check := view.HandbackCheck
	if check == nil || check.Status != protocol.HandbackCheckPassed {
		t.Fatalf("change hand-back check: %+v", check)
	}
	for _, want := range []string{"check ok", "GOMAXPROCS=", "GOFLAGS=-p="} {
		if !strings.Contains(check.OutputTail, want) {
			t.Errorf("output tail lacks %q: %s", want, check.OutputTail)
		}
	}
	procs := regexp.MustCompile(`GOMAXPROCS=(\d+)`).FindStringSubmatch(check.OutputTail)
	if procs == nil || !strings.Contains(check.OutputTail, "GOFLAGS=-p="+procs[1]) {
		t.Errorf("parallelism does not agree between GOMAXPROCS and GOFLAGS: %s", check.OutputTail)
	}
	if hasSignal(view.Signals, protocol.SignalHandbackCheckFailed) {
		t.Errorf("a passing check raised a failure signal: %+v", view.Signals)
	}
}

// A failing check keeps its exit code and output tail in the change and the
// notice, and raises one signal for the accept step.
func TestHandbackCheckFailedRaisesSignal(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, queueSet, withLoad(&fakeLoad{l: roomyLoad()}))
	repo := newRepo(t, "shop")
	w.register(t, repo)
	command := handbackCheckScript(t, "echo 'the check found a problem'\nexit 3\n")
	setHandbackCheck(t, g, "shop", command, "")
	eventually(t, "the daemon has the hand-back check", func() bool {
		cmd, _ := w.d.handbackCheckSettings("shop")
		return cmd == command
	})
	w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1",
		Task: "FAKE:handback FAKE:edit", Isolation: protocol.IsolationWorktree})
	n := w.waitNotice(t, 1)[0]
	for _, want := range []string{"hand-back check: failed (exit 3)", "hand-back check output:", "the check found a problem"} {
		if !strings.Contains(n.Body, want) {
			t.Errorf("notice lacks %q:\n%s", want, n.Body)
		}
	}
	view := changeView(t, g, noticeChangeID(t, n.Body))
	check := view.HandbackCheck
	if check == nil || check.Status != protocol.HandbackCheckFailed || check.ExitCode != 3 {
		t.Fatalf("change hand-back check: %+v", check)
	}
	if !strings.Contains(check.OutputTail, "the check found a problem") {
		t.Errorf("output tail: %q", check.OutputTail)
	}
	if !hasSignal(view.Signals, protocol.SignalHandbackCheckFailed) {
		t.Fatalf("no failure signal: %+v", view.Signals)
	}
	for _, s := range view.Signals {
		if s.Code == protocol.SignalHandbackCheckFailed && !strings.Contains(s.Evidence, "exit 3") {
			t.Errorf("signal evidence: %q", s.Evidence)
		}
	}
}

// A check that runs past its timeout is killed, root of the process group
// included, and recorded as timed out.
func TestHandbackCheckTimeout(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, queueSet, withLoad(&fakeLoad{l: roomyLoad()}))
	repo := newRepo(t, "shop")
	w.register(t, repo)
	command := handbackCheckScript(t, "echo started\nsleep 10\n")
	setHandbackCheck(t, g, "shop", command, "1s")
	eventually(t, "the daemon has the hand-back check", func() bool {
		cmd, timeout := w.d.handbackCheckSettings("shop")
		return cmd == command && timeout == time.Second
	})
	w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1",
		Task: "FAKE:handback FAKE:edit", Isolation: protocol.IsolationWorktree})
	n := w.waitNotice(t, 1)[0]
	if !strings.Contains(n.Body, "hand-back check: timed out after ") {
		t.Fatalf("notice:\n%s", n.Body)
	}
	view := changeView(t, g, noticeChangeID(t, n.Body))
	check := view.HandbackCheck
	if check == nil || check.Status != protocol.HandbackCheckTimedOut {
		t.Fatalf("change hand-back check: %+v", check)
	}
	if check.DurationMS < 400 || check.DurationMS > 3000 {
		t.Errorf("duration %dms, want about 1s", check.DurationMS)
	}
	if !hasSignal(view.Signals, protocol.SignalHandbackCheckTimedOut) {
		t.Fatalf("no timeout signal: %+v", view.Signals)
	}
}

// A check finds go and gofmt where a worker finds them, not on the daemon's
// own PATH: the daemon service starts with a minimal PATH that lacks the
// owner's Go (defect ch_b2551b718b8a14b1).
func TestHandbackCheckFindsToolchainGo(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, queueSet, withLoad(&fakeLoad{l: roomyLoad()}))
	repo := newRepo(t, "shop")
	w.register(t, repo)

	base := t.TempDir()
	bin, root, cache := filepath.Join(base, "bin"), filepath.Join(base, "goroot"), filepath.Join(base, "modcache")
	for _, dir := range []string{bin, filepath.Join(root, "bin"), cache} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	goScript := "#!/bin/sh\ncase \"$*\" in\n'env GOROOT GOMODCACHE') printf '%s\\n%s\\n' '" + root + "' '" + cache + "';;\n" +
		"*) echo 'go version fake';;\nesac\n"
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte(goScript), 0o700); err != nil {
		t.Fatal(err)
	}
	// gofmt lives where go says GOROOT is, not beside the go binary.
	if err := os.WriteFile(filepath.Join(root, "bin", "gofmt"), []byte("#!/bin/sh\necho 'gofmt fake'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	// The daemon's PATH has no Go; only the worker toolchain lookup does.
	t.Setenv("PATH", "/usr/bin:/bin")
	w.d.cfg.HarnessSearch = &harnesses.Options{PATH: bin, Dirs: []string{}, Home: base,
		LoginShellPATH: func(context.Context) string { return "" }}
	command := handbackCheckScript(t, "go version\ngofmt fake\n")
	setHandbackCheck(t, g, "shop", command, "")
	eventually(t, "the daemon has the hand-back check", func() bool {
		cmd, _ := w.d.handbackCheckSettings("shop")
		return cmd == command
	})
	w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1",
		Task: "FAKE:handback FAKE:edit", Isolation: protocol.IsolationWorktree})
	n := w.waitNotice(t, 1)[0]
	if !strings.Contains(n.Body, "hand-back check: passed in ") {
		t.Fatalf("notice:\n%s", n.Body)
	}
	view := changeView(t, g, noticeChangeID(t, n.Body))
	check := view.HandbackCheck
	if check == nil || check.Status != protocol.HandbackCheckPassed {
		t.Fatalf("change hand-back check: %+v", check)
	}
	for _, want := range []string{"go version fake", "gofmt fake"} {
		if !strings.Contains(check.OutputTail, want) {
			t.Errorf("output tail lacks %q: %s", want, check.OutputTail)
		}
	}
}

// A check whose tool is missing proves nothing: the notice says it could not
// run, in one line, instead of reporting a failed test.
func TestHandbackCheckMissingToolCouldNotRun(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, queueSet, withLoad(&fakeLoad{l: roomyLoad()}))
	repo := newRepo(t, "shop")
	w.register(t, repo)
	command := handbackCheckScript(t, "no-such-tool-xyz --version\n")
	setHandbackCheck(t, g, "shop", command, "")
	eventually(t, "the daemon has the hand-back check", func() bool {
		cmd, _ := w.d.handbackCheckSettings("shop")
		return cmd == command
	})
	w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1",
		Task: "FAKE:handback FAKE:edit", Isolation: protocol.IsolationWorktree})
	n := w.waitNotice(t, 1)[0]
	if !strings.Contains(n.Body, "hand-back check: could not run: no-such-tool-xyz not found") {
		t.Fatalf("notice:\n%s", n.Body)
	}
	if strings.Contains(n.Body, "hand-back check output:") {
		t.Errorf("a check that could not run printed output:\n%s", n.Body)
	}
	view := changeView(t, g, noticeChangeID(t, n.Body))
	check := view.HandbackCheck
	if check == nil || check.Status != protocol.HandbackCheckCouldNotRun {
		t.Fatalf("change hand-back check: %+v", check)
	}
	if hasSignal(view.Signals, protocol.SignalHandbackCheckFailed) {
		t.Errorf("a check that could not run raised a failure signal: %+v", view.Signals)
	}
}

// A hand-back with no commit changed nothing, so there is no check to run.
func TestHandbackCheckNoCommitsRunsNothing(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, queueSet, withLoad(&fakeLoad{l: roomyLoad()}))
	repo := newRepo(t, "shop")
	w.register(t, repo)
	marker := filepath.Join(t.TempDir(), "ran")
	command := handbackCheckScript(t, "echo ran > "+fmt.Sprintf("%q", marker)+"\n")
	setHandbackCheck(t, g, "shop", command, "")
	eventually(t, "the daemon has the hand-back check", func() bool {
		cmd, _ := w.d.handbackCheckSettings("shop")
		return cmd == command
	})
	w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1",
		Task: "FAKE:handback", Isolation: protocol.IsolationWorktree})
	n := w.waitNotice(t, 1)[0]
	if strings.Contains(n.Body, "hand-back check:") {
		t.Fatalf("a check ran with no commit:\n%s", n.Body)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a check ran with no commit")
	}
}

// The check waits while another suite holds the machine's test slot, then
// runs when the slot is freed.
func TestHandbackCheckWaitsForTestSlot(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, queueSet, withLoad(&fakeLoad{l: roomyLoad()}))
	repo := newRepo(t, "shop")
	w.register(t, repo)
	marker := filepath.Join(t.TempDir(), "ran")
	command := handbackCheckScript(t, "echo ran > "+fmt.Sprintf("%q", marker)+"\n")
	setHandbackCheck(t, g, "shop", command, "")
	eventually(t, "the daemon has the hand-back check", func() bool {
		cmd, _ := w.d.handbackCheckSettings("shop")
		return cmd == command
	})

	holder := cliClient(t, w.node)
	first := acquireTestSlot(t, holder, "")
	if !first.Admitted {
		t.Fatalf("the held suite was not admitted: %+v", first)
	}
	w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1",
		Task: "FAKE:handback FAKE:edit", Isolation: protocol.IsolationWorktree})
	time.Sleep(400 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the check ran while another suite held the slot")
	}
	if len(w.notices(t)) != 0 {
		t.Fatal("the notice went out before the check could run")
	}
	holder.c.Close()
	n := w.waitNotice(t, 1)[0]
	if !strings.Contains(n.Body, "hand-back check: passed in ") {
		t.Fatalf("notice:\n%s", n.Body)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the check did not run after the slot freed: %v", err)
	}
}
