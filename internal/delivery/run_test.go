package delivery

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// runFx is a small git repo with one commit and a scripts directory.
type runFx struct {
	t    *testing.T
	dir  string
	head string
}

func newRunFx(t *testing.T) *runFx {
	t.Helper()
	dir := t.TempDir()
	runTestGit(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, dir, "add", "README")
	runTestGit(t, dir, "commit", "-q", "-m", "first")
	return &runFx{t: t, dir: dir, head: runTestGit(t, dir, "rev-parse", "HEAD")}
}

func runTestGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{
		"-c", "user.name=Test", "-c", "user.email=test@example.com",
		"-c", "commit.gpgsign=false",
	}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// script writes an executable shell script at scripts/<name>, commits it so the
// tree stays clean, moves f.head to the new commit, and returns the relative
// path, as a repo would name its release command.
func (f *runFx) script(name, body string) string {
	f.t.Helper()
	dir := filepath.Join(f.dir, "scripts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		f.t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		f.t.Fatal(err)
	}
	runTestGit(f.t, f.dir, "add", "scripts/"+name)
	runTestGit(f.t, f.dir, "commit", "-q", "-m", "add "+name)
	f.head = runTestGit(f.t, f.dir, "rev-parse", "HEAD")
	return "scripts/" + name
}

func (f *runFx) cmd(argv ...string) StepCommand {
	return StepCommand{
		Step:    "release",
		Dir:     f.dir,
		Argv:    argv,
		Commit:  f.head,
		Timeout: 20 * time.Second,
	}
}

// runPidAlive reports whether the process is still running. A zombie that
// nobody reaped counts as gone.
func runPidAlive(pid int) bool {
	if b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid)); err == nil {
		s := string(b)
		if i := strings.LastIndex(s, ") "); i >= 0 && i+2 < len(s) {
			return s[i+2] != 'Z'
		}
	}
	return syscall.Kill(pid, 0) == nil
}

func runWaitFile(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil {
			if n, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				return n
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("file %s never appeared", path)
	return 0
}

func runWaitGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !runPidAlive(pid) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("process %d is still running", pid)
}

func TestRunStepSuccessWithFields(t *testing.T) {
	f := newRunFx(t)
	s := f.script("release.sh", `
echo "building $1"
echo "step=$AGENT_NET_STEP commit=$AGENT_NET_COMMIT extra=$EXTRA"
echo "cwd=$(pwd)"
echo "oops" >&2
echo "agent-net: tag=$1"
echo "agent-net: url=https://example.com/releases/$1"
`)
	cmd := f.cmd(s, "v1.2.3")
	cmd.Env = map[string]string{"EXTRA": "hello"}
	run, err := RunStep(context.Background(), cmd)
	if err != nil {
		t.Fatal(err)
	}
	if run.ExitCode != 0 || run.TimedOut {
		t.Fatalf("run = %+v", run)
	}
	if run.Fields["tag"] != "v1.2.3" || run.Fields["url"] != "https://example.com/releases/v1.2.3" {
		t.Fatalf("fields = %v", run.Fields)
	}
	for _, want := range []string{
		"building v1.2.3",
		"step=release commit=" + f.head + " extra=hello",
		"oops",
	} {
		if !strings.Contains(run.OutputTail, want) {
			t.Errorf("tail lacks %q:\n%s", want, run.OutputTail)
		}
	}
	// The command runs in Dir.
	wantDir, _ := filepath.EvalSymlinks(f.dir)
	if !strings.Contains(run.OutputTail, "cwd="+wantDir) {
		t.Errorf("tail lacks cwd %q:\n%s", wantDir, run.OutputTail)
	}
	if run.Duration <= 0 {
		t.Errorf("duration = %v", run.Duration)
	}
	if run.OutputDropped != 0 {
		t.Errorf("dropped = %d", run.OutputDropped)
	}
}

func TestRunStepEnvironment(t *testing.T) {
	t.Setenv("RUN_TEST_SECRET", "leak")
	t.Setenv("HOME", "/home/runtest")
	f := newRunFx(t)
	s := f.script("env.sh", `
echo "home=$HOME"
case "$PATH" in "") echo nopath;; *) echo path-ok;; esac
echo "inherited=[$RUN_TEST_SECRET]"
echo "step=$AGENT_NET_STEP"
echo "commit=$AGENT_NET_COMMIT"
`)
	cmd := f.cmd(s)
	cmd.Step = "deploy"
	// The caller cannot override the two variables agent-net owns.
	cmd.Env = map[string]string{"AGENT_NET_STEP": "evil", "AGENT_NET_COMMIT": "evil"}
	run, err := RunStep(context.Background(), cmd)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"home=/home/runtest", "path-ok", "inherited=[]", "step=deploy", "commit=" + f.head} {
		if !strings.Contains(run.OutputTail, want) {
			t.Errorf("tail lacks %q:\n%s", want, run.OutputTail)
		}
	}
}

func TestRunStepNonZeroExit(t *testing.T) {
	f := newRunFx(t)
	s := f.script("fail.sh", `
echo "agent-net: tag=v9"
echo "boom" >&2
exit 3
`)
	run, err := RunStep(context.Background(), f.cmd(s))
	if err != nil {
		t.Fatalf("a failing command is a result, not an error: %v", err)
	}
	if run.ExitCode != 3 || run.TimedOut {
		t.Fatalf("run = %+v", run)
	}
	if !strings.Contains(run.OutputTail, "boom") || run.Fields["tag"] != "v9" {
		t.Fatalf("run = %+v", run)
	}
}

func TestRunStepStartFailure(t *testing.T) {
	f := newRunFx(t)
	_, err := RunStep(context.Background(), f.cmd("scripts/does-not-exist.sh"))
	if err == nil {
		t.Fatal("want an error when the command cannot start")
	}
}

func TestRunStepTimeoutKillsChildren(t *testing.T) {
	f := newRunFx(t)
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	s := f.script("hang.sh", fmt.Sprintf(`
sleep 300 &
echo $! > %s
echo "started"
sleep 300
`, pidFile))
	cmd := f.cmd(s)
	cmd.Timeout = 1500 * time.Millisecond
	start := time.Now()
	run, err := RunStep(context.Background(), cmd)
	if err != nil {
		t.Fatal(err)
	}
	if !run.TimedOut {
		t.Fatalf("run = %+v", run)
	}
	if run.ExitCode == 0 {
		t.Errorf("exit code = 0 after a kill")
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("took %v", d)
	}
	if !strings.Contains(run.OutputTail, "started") {
		t.Errorf("tail = %q", run.OutputTail)
	}
	runWaitGone(t, runWaitFile(t, pidFile))
}

func TestRunStepCancelKillsChildren(t *testing.T) {
	f := newRunFx(t)
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	s := f.script("hang.sh", fmt.Sprintf(`
sleep 300 &
echo $! > %s
sleep 300
`, pidFile))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		// Only the test goroutine may call t.Fatal: this one just polls.
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if b, err := os.ReadFile(pidFile); err == nil && len(strings.TrimSpace(string(b))) > 0 {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
	}()
	run, err := RunStep(ctx, f.cmd(s))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, run = %+v", err, run)
	}
	if run.TimedOut {
		t.Errorf("a cancel is not a timeout")
	}
	runWaitGone(t, runWaitFile(t, pidFile))
}

func TestRunStepWrongCommit(t *testing.T) {
	f := newRunFx(t)
	marker := filepath.Join(t.TempDir(), "ran")
	s := f.script("release.sh", "touch "+marker+"\n")
	// Move HEAD on after the evidence was checked.
	if err := os.WriteFile(filepath.Join(f.dir, "more"), []byte("y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, f.dir, "add", "more")
	runTestGit(t, f.dir, "commit", "-q", "-m", "second")
	newHead := runTestGit(t, f.dir, "rev-parse", "HEAD")

	_, err := RunStep(context.Background(), f.cmd(s)) // Commit is the first commit
	if !errors.Is(err, ErrWrongCommit) {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), f.head) || !strings.Contains(err.Error(), newHead) {
		t.Errorf("error should name both commits: %v", err)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Error("the command ran although HEAD was wrong")
	}
}

// dirtyFx is a clean fixture whose command writes a marker when it runs.
func dirtyFx(t *testing.T) (*runFx, string, string) {
	t.Helper()
	f := newRunFx(t)
	marker := filepath.Join(t.TempDir(), "ran")
	s := f.script("release.sh", "touch "+marker+"\n")
	return f, s, marker
}

func runWriteFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runExpectRefused(t *testing.T, f *runFx, s, marker string) error {
	t.Helper()
	_, err := RunStep(context.Background(), f.cmd(s))
	if !errors.Is(err, ErrDirtyTree) {
		t.Fatalf("err = %v, want ErrDirtyTree", err)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Error("the command ran although the tree was dirty")
	}
	return err
}

func TestRunStepRefusesModifiedTrackedFile(t *testing.T) {
	f, s, marker := dirtyFx(t)
	runWriteFile(t, f.dir, "README", "changed\n")
	err := runExpectRefused(t, f, s, marker)
	if !strings.Contains(err.Error(), "README") {
		t.Errorf("error should name the path: %v", err)
	}
}

func TestRunStepRefusesStagedChange(t *testing.T) {
	f, s, marker := dirtyFx(t)
	runWriteFile(t, f.dir, "README", "changed\n")
	runTestGit(t, f.dir, "add", "README")
	runExpectRefused(t, f, s, marker)
}

func TestRunStepRefusesUntrackedFile(t *testing.T) {
	f, s, marker := dirtyFx(t)
	runWriteFile(t, f.dir, "stray.txt", "x\n")
	err := runExpectRefused(t, f, s, marker)
	if !strings.Contains(err.Error(), "stray.txt") {
		t.Errorf("error should name the path: %v", err)
	}
}

func TestRunStepAllowsIgnoredFile(t *testing.T) {
	f := newRunFx(t)
	runWriteFile(t, f.dir, ".gitignore", "*.log\n")
	runTestGit(t, f.dir, "add", ".gitignore")
	runTestGit(t, f.dir, "commit", "-q", "-m", "ignore")
	marker := filepath.Join(t.TempDir(), "ran")
	s := f.script("release.sh", "touch "+marker+"\n")
	runWriteFile(t, f.dir, "build.log", "noise\n")
	run, err := RunStep(context.Background(), f.cmd(s))
	if err != nil {
		t.Fatalf("ignored file should be allowed: %v", err)
	}
	if run.ExitCode != 0 {
		t.Errorf("exit = %d", run.ExitCode)
	}
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Error("the command did not run")
	}
}

func TestRunStepCleanTreeRuns(t *testing.T) {
	f, s, marker := dirtyFx(t)
	if _, err := RunStep(context.Background(), f.cmd(s)); err != nil {
		t.Fatalf("clean tree: %v", err)
	}
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Error("the command did not run")
	}
}

func TestRunStepDirtyListIsCapped(t *testing.T) {
	f, s, marker := dirtyFx(t)
	for i := 0; i < 13; i++ {
		runWriteFile(t, f.dir, fmt.Sprintf("stray-%02d.txt", i), "x\n")
	}
	err := runExpectRefused(t, f, s, marker)
	msg := err.Error()
	if n := strings.Count(msg, "stray-"); n != 10 {
		t.Errorf("error lists %d paths, want 10: %v", n, msg)
	}
	if !strings.Contains(msg, "3 more") {
		t.Errorf("error should count the rest (3 more): %v", msg)
	}
	if strings.Contains(msg, "stray-12") {
		t.Errorf("error lists a path beyond the first 10: %v", msg)
	}
}

func TestRunStepWrongCommitBeatsDirtyTree(t *testing.T) {
	f, s, _ := dirtyFx(t)
	runWriteFile(t, f.dir, "stray.txt", "x\n")
	cmd := f.cmd(s)
	cmd.Commit = strings.Repeat("a", 40)
	if _, err := RunStep(context.Background(), cmd); !errors.Is(err, ErrWrongCommit) {
		t.Fatalf("err = %v, want ErrWrongCommit", err)
	}
}

func TestRunStepRejectsBadInput(t *testing.T) {
	f := newRunFx(t)
	s := f.script("ok.sh", "echo ok\n")
	cases := map[string]func(c *StepCommand){
		"bad step":        func(c *StepCommand) { c.Step = "merge" },
		"empty step":      func(c *StepCommand) { c.Step = "" },
		"no argv":         func(c *StepCommand) { c.Argv = nil },
		"empty argv0":     func(c *StepCommand) { c.Argv = []string{""} },
		"no dir":          func(c *StepCommand) { c.Dir = "" },
		"no commit":       func(c *StepCommand) { c.Commit = "" },
		"non-hex commit":  func(c *StepCommand) { c.Commit = "main" },
		"option commit":   func(c *StepCommand) { c.Commit = "--all" },
		"short commit":    func(c *StepCommand) { c.Commit = f.head[:3] },
		"abbreviated":     func(c *StepCommand) { c.Commit = f.head[:12] },
		"upper case":      func(c *StepCommand) { c.Commit = strings.ToUpper(f.head) },
		"negative time":   func(c *StepCommand) { c.Timeout = -time.Second },
		"env key empty":   func(c *StepCommand) { c.Env = map[string]string{"": "x"} },
		"env key equals":  func(c *StepCommand) { c.Env = map[string]string{"A=B": "x"} },
		"env value NUL":   func(c *StepCommand) { c.Env = map[string]string{"A": "x\x00y"} },
		"dir not a repo":  func(c *StepCommand) { c.Dir = t.TempDir() },
		"dir is missing":  func(c *StepCommand) { c.Dir = filepath.Join(t.TempDir(), "nope") },
		"commit is ahead": func(c *StepCommand) { c.Commit = strings.Repeat("a", 40) },
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			cmd := f.cmd(s)
			mut(&cmd)
			if _, err := RunStep(context.Background(), cmd); err == nil {
				t.Fatal("want an error")
			}
		})
	}
}

func TestRunStepOutputCapKeepsTail(t *testing.T) {
	f := newRunFx(t)
	// 3000 lines of about 100 bytes each is about 300 KB, over the 64 KB cap.
	pad := strings.Repeat("0123456789", 8)
	s := f.script("loud.sh", fmt.Sprintf(`
echo "agent-net: tag=v-first"
i=0
while [ $i -lt 3000 ]; do
  printf 'line %%05d %s\n' $i
  i=$((i+1))
done
echo "last line"
`, pad))
	run, err := RunStep(context.Background(), f.cmd(s))
	if err != nil {
		t.Fatal(err)
	}
	if len(run.OutputTail) > runTailBytes {
		t.Fatalf("tail is %d bytes, cap is %d", len(run.OutputTail), runTailBytes)
	}
	if len(run.OutputTail) < runTailBytes-200 {
		t.Fatalf("tail is only %d bytes", len(run.OutputTail))
	}
	if !strings.HasSuffix(run.OutputTail, "last line\n") {
		t.Errorf("tail does not end with the last line: %q", run.OutputTail[len(run.OutputTail)-40:])
	}
	if !strings.HasPrefix(run.OutputTail, "line ") {
		t.Errorf("tail should start at a line start: %q", run.OutputTail[:40])
	}
	total := int64(len("agent-net: tag=v-first\n")) + 3000*int64(len("line 00000 ")+len(pad)+1) + int64(len("last line\n"))
	if run.OutputDropped <= 0 || run.OutputDropped+int64(len(run.OutputTail)) != total {
		t.Errorf("dropped %d + kept %d != total %d", run.OutputDropped, len(run.OutputTail), total)
	}
	// A field printed before the cut still counts.
	if run.Fields["tag"] != "v-first" {
		t.Errorf("fields = %v", run.Fields)
	}
}

func TestRunCollectorFieldParsing(t *testing.T) {
	feed := func(s string) map[string]string {
		c := newRunCollector(runRedactor{})
		_, _ = c.Write([]byte(s))
		return c.finish().Fields
	}
	got := feed("" +
		"agent-net: tag=v1.2.3\n" +
		"agent-net: url=https://example.com/a?b=c&d=e\n" + // '=' in the value is kept
		"agent-net: note=hello world  \r\n" + // value runs to the end of the line, trimmed, CR dropped
		"agent-net: tag=v1.2.4\n" + // the last value wins
		"agent-net: empty=\n" + // empty values are skipped
		"agent-net: Upper=1\n" + // key must be [a-z_]+
		"agent-net: with-dash=1\n" +
		"agent-net: digit2=1\n" +
		"agent-net: =1\n" +
		"agent-net: noequals\n" +
		"  agent-net: indented=1\n" + // must start the line
		"x agent-net: inline=1\n" +
		"agent-net:nospace=1\n" +
		"agent-net: snake_case_name=ok\n")
	want := map[string]string{
		"tag":             "v1.2.4",
		"url":             "https://example.com/a?b=c&d=e",
		"note":            "hello world",
		"snake_case_name": "ok",
	}
	if len(got) != len(want) {
		t.Fatalf("fields = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("field %q = %q, want %q (all: %v)", k, got[k], v, got)
		}
	}
}

func TestRunCollectorFieldLimits(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 40; i++ {
		// letters only: key i as a-z pairs
		fmt.Fprintf(&b, "agent-net: k%s=v%d\n", strings.Repeat("a", i+1), i)
	}
	c := newRunCollector(runRedactor{})
	_, _ = c.Write([]byte(b.String()))
	got := c.finish().Fields
	// keys contain only a and k: valid. Only the first 32 are kept.
	if len(got) != runMaxFields {
		t.Fatalf("got %d fields, want %d", len(got), runMaxFields)
	}
	if _, ok := got["k"+strings.Repeat("a", 1)]; !ok {
		t.Errorf("first field missing: %v", got)
	}
	if _, ok := got["k"+strings.Repeat("a", 33)]; ok {
		t.Errorf("33rd field kept")
	}
	// A known key can still be updated once the cap is reached.
	c = newRunCollector(runRedactor{})
	_, _ = c.Write([]byte(b.String() + "agent-net: ka=updated\n"))
	if got := c.finish().Fields; got["ka"] != "updated" || len(got) != runMaxFields {
		t.Errorf("update after cap: %d fields, ka=%q", len(got), got["ka"])
	}
	// Overlong values and overlong lines are ignored.
	c = newRunCollector(runRedactor{})
	_, _ = c.Write([]byte("agent-net: big=" + strings.Repeat("x", runMaxFieldValue+1) + "\n"))
	_, _ = c.Write([]byte("agent-net: ok=" + strings.Repeat("y", runMaxFieldValue) + "\n"))
	_, _ = c.Write([]byte("agent-net: huge=" + strings.Repeat("z", 3*runMaxLine) + "\n"))
	_, _ = c.Write([]byte("agent-net: after=1\n"))
	got = c.finish().Fields
	if _, ok := got["big"]; ok {
		t.Errorf("overlong value kept")
	}
	if _, ok := got["huge"]; ok {
		t.Errorf("overlong line kept")
	}
	if len(got["ok"]) != runMaxFieldValue || got["after"] != "1" {
		t.Errorf("fields = %d bytes ok, after=%q", len(got["ok"]), got["after"])
	}
}

func TestRunCollectorSplitWrites(t *testing.T) {
	c := newRunCollector(runRedactor{})
	for _, part := range []string{"agent-", "net: ta", "g=v", "1\nagent-net: last=no-newline"} {
		_, _ = c.Write([]byte(part))
	}
	got := c.finish().Fields
	if got["tag"] != "v1" || got["last"] != "no-newline" {
		t.Errorf("fields = %v", got)
	}
}

func TestRunCollectorURLDetection(t *testing.T) {
	const u1 = "https://github.com/niparis/agent-net/actions/runs/123456789"
	const u2 = "https://github.com/niparis/agent-net/actions/runs/987654321"
	cases := []struct {
		name, in, want string
	}{
		{"plain", "Run started: " + u1 + "\n", u1},
		{"with suffix", "see " + u1 + "/job/55 now\n", u1},
		{"last one wins", u1 + "\nlater\n" + u2 + "\n", u2},
		{"explicit url field wins", u1 + "\nagent-net: url=https://example.com/r\n", "https://example.com/r"},
		{"explicit url before", "agent-net: url=https://example.com/r\n" + u1 + "\n", "https://example.com/r"},
		{"not an actions run", "https://github.com/niparis/agent-net/releases/tag/v1\n", ""},
		{"not a number", "https://github.com/niparis/agent-net/actions/runs/abc\n", ""},
		{"http is ignored", "http://github.com/niparis/agent-net/actions/runs/1\n", ""},
		{"other host", "https://example.com/niparis/agent-net/actions/runs/1\n", ""},
		{"no trailing newline", "x " + u1, u1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newRunCollector(runRedactor{})
			_, _ = c.Write([]byte(tc.in))
			if got := c.finish().Fields["url"]; got != tc.want {
				t.Errorf("url = %q, want %q", got, tc.want)
			}
		})
	}
	// Split across writes.
	c := newRunCollector(runRedactor{})
	_, _ = c.Write([]byte("go " + u1[:30]))
	_, _ = c.Write([]byte(u1[30:] + "\n"))
	if got := c.finish().Fields["url"]; got != u1 {
		t.Errorf("split url = %q", got)
	}
}

func TestRunStepURLFromOutput(t *testing.T) {
	f := newRunFx(t)
	s := f.script("wf.sh", `echo "Watching https://github.com/niparis/agent-net/actions/runs/42"`+"\n")
	run, err := RunStep(context.Background(), f.cmd(s))
	if err != nil {
		t.Fatal(err)
	}
	if run.Fields["url"] != "https://github.com/niparis/agent-net/actions/runs/42" {
		t.Errorf("fields = %v", run.Fields)
	}
}

func TestRunRedact(t *testing.T) {
	cases := []struct{ in, want string }{
		{"hello", "hello"},
		{"token=abc123", "[redacted]"},
		{"curl -d TOKEN=abc", "[redacted]"},
		{"export password=hunter2", "[redacted]"},
		{"PASSWORD=hunter2", "[redacted]"},
		{"Authorization: Bearer abc", "[redacted]"},
		{"authorization: basic abc", "[redacted]"},
		{"using ghp_abcdefghijklmnopqrstuvwxyz0123456789 now", "[redacted]"},
		{"github_pat_11AAAA_bbbb", "[redacted]"},
		{"key is sk-ant-abc123", "[redacted]"},
		{"sk-abc", "[redacted]"},
		{"(sk-abc)", "[redacted]"},
		{"task-1 done", "task-1 done"},
		{"disk-usage 10%", "disk-usage 10%"},
		{"tokens used: 5", "tokens used: 5"},
		{"auth ok", "auth ok"},
		{"ghs_abc123", "[redacted]"},
		{"GHO_abc123", "[redacted]"},
		{"ghu_abc123 ghr_x", "[redacted]"},
		{"gha_abc is not a token kind", "gha_abc is not a token kind"},
		{"API_KEY: x", "[redacted]"},
		{"client secret = x", "[redacted]"},
		{"ssh key:x", "[redacted]"},
		{"Bearer abc", "[redacted]"},
		{"aws_secret_access_key abc", "[redacted]"},
		{"-----BEGIN CERTIFICATE-----", "[redacted]"},
		{"keyboard layout set", "keyboard layout set"},
		{"secrets found: none", "secrets found: none"},
	}
	for _, tc := range cases {
		if got := runRedact(tc.in); got != tc.want {
			t.Errorf("runRedact(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestRunRedactLines(t *testing.T) {
	in := "start\r\nAuthorization: Bearer abc\r\nmiddle\ntoken=x\n\nend"
	want := "start\r\n[redacted]\nmiddle\n[redacted]\n\nend"
	// CRLF lines keep their ending; check the secret line is gone either way.
	got := runRedactLines(in)
	if strings.Contains(got, "Bearer") || strings.Contains(got, "token=x") {
		t.Fatalf("secret left in: %q", got)
	}
	if !strings.Contains(got, "start") || !strings.Contains(got, "middle") || !strings.HasSuffix(got, "\n\nend") {
		t.Fatalf("got %q, want like %q", got, want)
	}
	if runRedactLines("") != "" {
		t.Errorf("empty input")
	}
}

func TestRunStepRedactsTailAndFields(t *testing.T) {
	f := newRunFx(t)
	s := f.script("leak.sh", `
echo "all good"
echo "token=supersecret"
echo "curl -H 'Authorization: Bearer abc'" >&2
echo "agent-net: url=https://example.com/x?token=abc"
echo "agent-net: tag=v1"
`)
	run, err := RunStep(context.Background(), f.cmd(s))
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"supersecret", "Bearer", "token=abc"} {
		if strings.Contains(run.OutputTail, bad) {
			t.Errorf("tail still has %q:\n%s", bad, run.OutputTail)
		}
	}
	if !strings.Contains(run.OutputTail, "all good") || !strings.Contains(run.OutputTail, "[redacted]") {
		t.Errorf("tail = %q", run.OutputTail)
	}
	if _, ok := run.Fields["url"]; ok {
		t.Errorf("field with a secret was kept: %v", run.Fields)
	}
	if run.Fields["tag"] != "v1" {
		t.Errorf("fields = %v", run.Fields)
	}
}

func TestRunStepTailStartsAtLineBoundary(t *testing.T) {
	// A cut in the middle of a line must not leave a half secret such as
	// "ken=abc" in the tail.
	c := newRunCollector(runRedactor{})
	line := "token=" + strings.Repeat("a", 200) + "\n"
	for c.total < int64(2*runTailBytes) {
		_, _ = c.Write([]byte(line))
	}
	res := c.finish()
	if !strings.HasPrefix(res.Tail, "token=") {
		t.Errorf("tail starts mid-line: %q", res.Tail[:30])
	}
	if strings.Contains(runRedactLines(res.Tail), "token=") {
		t.Errorf("secret left after redaction")
	}
}

// TimedOut means the runner killed the command. A command that exited by
// itself is not timed out, even when a child it left behind kept the output
// open past the deadline.
func TestRunStepTimedOutOnlyWhenKilled(t *testing.T) {
	f := newRunFx(t)
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	s := f.script("bg.sh", fmt.Sprintf("sleep 3 &\necho $! > %s\necho done\n", pidFile))
	cmd := f.cmd(s)
	cmd.Timeout = 300 * time.Millisecond
	run, err := RunStep(context.Background(), cmd)
	if pid := runWaitFile(t, pidFile); pid > 0 {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
	if err != nil {
		t.Fatal(err)
	}
	if run.ExitCode != 0 || run.TimedOut {
		t.Fatalf("run = %+v: the command exited 0 by itself", run)
	}
}

// S14: git status would run the fsmonitor hook; the clean check must not.
func TestRunStepNeverRunsFsmonitor(t *testing.T) {
	f := newRunFx(t)
	marker := filepath.Join(t.TempDir(), "fsmonitor-ran")
	s := f.script("ok.sh", "echo ok\n")
	hook := filepath.Join(t.TempDir(), "fsmonitor.sh")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\ntouch "+marker+"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, f.dir, "config", "core.fsmonitor", hook)
	runTestGit(t, f.dir, "status", "--porcelain")
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("control: git status did not run the fsmonitor hook, so this test proves nothing")
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	if _, err := RunStep(context.Background(), f.cmd(s)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("RunStep ran the fsmonitor hook")
	}
}

// S12: the shapes of secrets the output can show.
func TestRunStepRedactsSecretShapes(t *testing.T) {
	f := newRunFx(t)
	s := f.script("leak.sh", `
echo "start"
echo "using ghs_server0token0abcdefghijklmnop"
echo "using gho_oauth0token0abcdefghijklmnop"
echo "using ghu_user0token0abcdefghijklmnopq"
echo "using ghr_refresh0token0abcdefghijklmn"
echo "api_key: leak-1"
echo "CLIENT_SECRET = leak-2"
echo "x-api-key:leak-3"
echo "curl -H 'X: Bearer leak-4'"
echo "AWS_SECRET_ACCESS_KEY leak-5"
echo "-----BEGIN OPENSSH PRIVATE KEY-----"
echo "b3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQ"
echo "leak-6-base64-body"
echo "-----END OPENSSH PRIVATE KEY-----"
echo "deploying with $DEPLOY_CRED"
echo "agent-net: note=$DEPLOY_CRED"
echo "agent-net: short=$SHORT"
echo "end"
`)
	cmd := f.cmd(s)
	cmd.Env = map[string]string{"DEPLOY_CRED": "leak-7-exact-value", "SHORT": "v1"}
	run, err := RunStep(context.Background(), cmd)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"ghs_", "gho_", "ghu_", "ghr_", "leak-", "b3BlbnNz", "PRIVATE KEY"} {
		if strings.Contains(run.OutputTail, bad) {
			t.Errorf("tail still has %q:\n%s", bad, run.OutputTail)
		}
	}
	for _, keep := range []string{"start", "end"} {
		if !strings.Contains(run.OutputTail, keep) {
			t.Errorf("tail lost %q:\n%s", keep, run.OutputTail)
		}
	}
	if _, ok := run.Fields["note"]; ok {
		t.Errorf("a field with an Env value was kept: %v", run.Fields)
	}
	if run.Fields["short"] != "v1" {
		t.Errorf("a short Env value is not treated as a secret: %v", run.Fields)
	}
}

// S12: a tail that starts inside a PEM block has lost its BEGIN line.
func TestRunStepRedactsPEMCutByTheTail(t *testing.T) {
	f := newRunFx(t)
	s := f.script("pem.sh", `
echo "-----BEGIN RSA PRIVATE KEY-----"
i=0
while [ $i -lt 2000 ]; do
  echo "MIIEpAIBAAKCAQEA0123456789abcdefghijklmnopqrstuvwxyz"
  i=$((i+1))
done
echo "-----END RSA PRIVATE KEY-----"
echo "after"
`)
	run, err := RunStep(context.Background(), f.cmd(s))
	if err != nil {
		t.Fatal(err)
	}
	if run.OutputDropped == 0 {
		t.Fatal("the BEGIN line was not cut off: test needs more output")
	}
	if strings.Contains(run.OutputTail, "MIIE") {
		t.Fatalf("key body left in the tail: %q", run.OutputTail[:80])
	}
	if !strings.HasSuffix(run.OutputTail, "after\n") {
		t.Fatalf("tail end = %q", run.OutputTail[len(run.OutputTail)-40:])
	}
}

func TestRunRedactorLines(t *testing.T) {
	r := newRunRedactor(map[string]string{"A": "exact-secret-1", "B": "short"})
	in := strings.Join([]string{
		"ok 1",
		"-----BEGIN EC PRIVATE KEY-----",
		"MHcCAQEEIBody",
		"-----END EC PRIVATE KEY-----",
		"ok 2",
		"value is exact-secret-1 here",
		"short is fine",
	}, "\n")
	want := strings.Join([]string{
		"ok 1", runRedacted, runRedacted, runRedacted, "ok 2", runRedacted, "short is fine",
	}, "\n")
	if got := r.lines(in); got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	// The tail starts inside a block: its BEGIN line was cut off.
	if got := r.lines("AAAA\nBBBB\n-----END EC PRIVATE KEY-----\nafter"); got != runRedacted+"\n"+runRedacted+"\n"+runRedacted+"\nafter" {
		t.Fatalf("cut block: %q", got)
	}
	// An END line before a BEGIN line belongs to a block that was cut.
	if got := r.lines("x\n-----END A-----\n-----BEGIN B-----\ny"); got != runRedacted+"\n"+runRedacted+"\n"+runRedacted+"\n"+runRedacted {
		t.Fatalf("cut then open block: %q", got)
	}
}

// The ring keeps exactly the last runTailBytes for any mix of write sizes.
func TestRunCollectorRing(t *testing.T) {
	sizes := []int{1, 7, 100, 4096, runTailBytes - 1, runTailBytes, runTailBytes + 1, 3 * runTailBytes, 13, 65000}
	for round := 0; round < 3; round++ {
		c := newRunCollector(runRedactor{})
		var all []byte
		n := 0
		for i, sz := range sizes {
			p := make([]byte, sz)
			for j := range p {
				p[j] = byte('a' + (n+j)%26)
				if (n+j)%97 == 0 {
					p[j] = '\n'
				}
			}
			n += sz
			_, _ = c.Write(p)
			all = append(all, p...)
			want := all
			if len(want) > runTailBytes {
				want = want[len(want)-runTailBytes:]
			}
			if got := c.tail(); !bytes.Equal(got, want) {
				t.Fatalf("round %d write %d (%d bytes): tail differs (got %d bytes, want %d)", round, i, sz, len(got), len(want))
			}
			if c.dropped != int64(len(all)-len(want)) {
				t.Fatalf("dropped = %d, want %d", c.dropped, len(all)-len(want))
			}
			if len(all) > runTailBytes && c.lastDropped != all[len(all)-runTailBytes-1] {
				t.Fatalf("lastDropped = %q, want %q", c.lastDropped, all[len(all)-runTailBytes-1])
			}
		}
		sizes = append(sizes[1:], sizes[0])
	}
}
