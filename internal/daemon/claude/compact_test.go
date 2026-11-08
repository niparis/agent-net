package claude

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/settings"
)

func TestIdleCompact(t *testing.T) {
	for _, tc := range []struct {
		name, status, kind                             string
		tokens                                         int
		age                                            time.Duration
		attached, noPrompt, disabled, scanError, noJob bool
		clients                                        string // ps output; the clients' pids are asked to the host
		viewing                                        bool   // the host says the client's herdr pane is focused, or outside herdr
		wantLog                                        string
		want                                           int
	}{
		{name: "eligible", status: "idle", kind: "bg", tokens: 200000, age: 50 * time.Minute, want: 1},
		{name: "busy", status: "busy", kind: "bg", tokens: 300000, age: time.Hour},
		{name: "shell", status: "shell", kind: "bg", tokens: 300000, age: time.Hour},
		{name: "interactive", status: "idle", kind: "interactive", tokens: 300000, age: time.Hour},
		{name: "attached", status: "idle", kind: "bg", tokens: 300000, age: time.Hour, attached: true},
		{name: "small", status: "idle", kind: "bg", tokens: 199999, age: time.Hour},
		{name: "recent", status: "idle", kind: "bg", tokens: 300000, age: 49 * time.Minute},
		{name: "disabled", status: "idle", kind: "bg", tokens: 300000, age: time.Hour, disabled: true},
		{name: "process scan failure", status: "idle", kind: "bg", tokens: 300000, age: time.Hour, scanError: true},
		{name: "missing job ID", status: "idle", kind: "bg", tokens: 300000, age: time.Hour, noJob: true},
		{name: "attach client in an unfocused herdr pane", status: "idle", kind: "bg", tokens: 300000, age: time.Hour, clients: "77 claude attach job-test", want: 1, wantLog: "event=idle_compact session=test result=ok"},
		{name: "attach client in a focused herdr pane or outside herdr", status: "idle", kind: "bg", tokens: 300000, age: time.Hour, clients: "77 claude attach job-test", viewing: true, wantLog: "event=idle_compact session=test result=skipped:viewing"},
		{name: "small context is logged once", status: "idle", kind: "bg", tokens: 100000, age: time.Hour, wantLog: "event=idle_compact session=test result=skipped:small_context"},
		{name: "prompt timeout", status: "idle", kind: "bg", tokens: 300000, age: time.Hour, noPrompt: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			sessions := filepath.Join(dir, "sessions")
			t.Setenv("AGENT_NET_CLAUDE_SESSIONS_DIR", sessions)
			projects := filepath.Join(dir, "projects")
			if err := os.MkdirAll(sessions, 0700); err != nil {
				t.Fatal(err)
			}
			td := filepath.Join(projects, transcriptDir(dir))
			if err := os.MkdirAll(td, 0700); err != nil {
				t.Fatal(err)
			}
			transcript := filepath.Join(td, "test.jsonl")
			before := fmt.Sprintf("{\"type\":\"system\",\"subtype\":\"compact_boundary\"}\n{\"type\":\"assistant\",\"message\":{\"usage\":{\"input_tokens\":10,\"cache_creation_input_tokens\":20,\"cache_read_input_tokens\":%d}}}\n", tc.tokens-30)
			if err := os.WriteFile(transcript, []byte(before), 0600); err != nil {
				t.Fatal(err)
			}
			sf := filepath.Join(sessions, fmt.Sprintf("%d.json", os.Getpid()))
			if err := os.WriteFile(sf, []byte(fmt.Sprintf(`{"pid":%d,"sessionId":"test","cwd":%q,"kind":%q,"jobId":"job-test","status":%q}`, os.Getpid(), dir, tc.kind, tc.status)), 0600); err != nil {
				t.Fatal(err)
			}
			if tc.noJob {
				raw, _ := os.ReadFile(sf)
				if err := os.WriteFile(sf, []byte(strings.Replace(string(raw), "job-test", "", 1)), 0600); err != nil {
					t.Fatal(err)
				}
			}
			started := time.Now()
			now := started
			old := now.Add(-tc.age - time.Second)
			for _, p := range []string{sf, transcript} {
				if err := os.Chtimes(p, old, old); err != nil {
					t.Fatal(err)
				}
			}
			bin := filepath.Join(dir, "claude")
			script := "#!/bin/sh\nstty -echo\nprintf x >> \"$COMPACT_STARTS\"\n"
			if !tc.noPrompt {
				script += "printf '❯'\n"
			}
			script += "IFS= read -r line\n[ \"$1\" = attach ] && [ \"$2\" = job-test ] && [ \"$line\" = /compact ] || exit 12\nprintf '%s\\n' '{\"type\":\"system\",\"subtype\":\"compact_boundary\"}' >> \"$COMPACT_TRANSCRIPT\"\nIFS= read -r line\n"
			if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("COMPACT_TRANSCRIPT", transcript)
			starts := filepath.Join(dir, "starts")
			t.Setenv("COMPACT_STARTS", starts)
			h := &compactHost{fakeHost: &fakeHost{}, snap: settings.Snapshot{}, viewing: map[int]bool{77: tc.viewing}}
			if tc.disabled {
				h.snap.Values = []settings.Value{{Key: "sessions.idle_compaction", Level: "network", Scope: "network", Value: []byte("false")}}
			}
			a := New(Config{SessionsDir: sessions, ProjectsDir: projects})
			a.host = h
			a.compactNow = func() time.Time { return now }
			a.compactBinary = bin
			a.compactPromptTimeout = 100 * time.Millisecond
			a.compactTimeout = time.Second
			if tc.clients != "" {
				a.compactPS = func(context.Context) ([]byte, error) { return []byte(tc.clients), nil }
			} else {
				a.compactAttached = func(context.Context) (map[string]bool, error) {
					if tc.scanError {
						return nil, fmt.Errorf("process scan failed")
					}
					return map[string]bool{"job-test": tc.attached}, nil
				}
			}
			a.poll()
			a.compactOnce(context.Background())
			if tc.want > 0 || tc.noPrompt {
				now = now.Add(2 * time.Hour)
			} // Still the same idle stretch after another cache lifetime.
			a.poll()
			a.compactOnce(context.Background())
			data, err := os.ReadFile(transcript)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Count(string(data), `"compact_boundary"`) - 1; got != tc.want {
				t.Fatalf("new boundaries=%d, want %d; logs=%v", got, tc.want, h.logs)
			}
			startBytes, _ := os.ReadFile(starts)
			wantStarts := tc.want
			if tc.noPrompt {
				wantStarts = 1
			}
			if len(startBytes) != wantStarts {
				t.Fatalf("attach attempts=%d want %d", len(startBytes), wantStarts)
			}
			if tc.wantLog != "" {
				n := 0
				for _, l := range h.logs {
					if strings.Contains(l, "event=idle_compact") {
						n++
						if l != tc.wantLog {
							t.Errorf("event line %q, want %q", l, tc.wantLog)
						}
					}
				}
				if n != 1 {
					t.Errorf("%d event lines for one idle stretch, want 1; logs=%v", n, h.logs)
				}
				if tc.want > 0 {
					if got := h.current()[0].IdleCompactedAt; got != started.Unix() {
						t.Fatalf("note timestamp=%d", got)
					}
					// A real busy observation starts a new stretch; polling idle alone did not.
					a.mu.Lock()
					f := a.byNative["test"]
					f.info.Status = "busy"
					a.byNative["test"] = f
					a.observeCompactStateLocked(f.info)
					f.info.Status = "idle"
					f.info.LastActive = old.Unix()
					a.byNative["test"] = f
					a.mu.Unlock()
					a.compactOnce(context.Background())
					data, _ = os.ReadFile(transcript)
					if strings.Count(string(data), `"compact_boundary"`) != 3 {
						t.Fatalf("next idle stretch did not compact: %s logs=%v", data, h.logs)
					}
				}
				if tc.noPrompt && len(h.logs) == 0 {
					t.Fatal("timeout was not logged")
				}
			}
		})
	}
}

type compactHost struct {
	*fakeHost
	snap    settings.Snapshot
	viewing map[int]bool
}

func (h *compactHost) AttachClientViewing(pid int) bool { return h.viewing[pid] }

func (h *compactHost) SettingsSnapshot() settings.Snapshot { return h.snap }

func TestCompactAttachProcess(t *testing.T) {
	if attachedJobsExcept([]byte("42 claude attach job-test"), 42)["job-test"] {
		t.Fatal("counted own attach client")
	}
	if !attachedJobsExcept([]byte("42 claude attach job-test\n43 claude attach job-test"), 42)["job-test"] {
		t.Fatal("missed second attach client")
	}
	for _, tc := range []struct {
		line string
		want bool
	}{
		{"42 /usr/bin/claude attach job-test", true},
		{"42 /opt/claude attach job-test --verbose", true},
		{"42 claude attach job-test-other", false},
		{"42 sh -c claude attach job-test", false},
		{"42 claude --bg job-test", false},
	} {
		if got := attachedJobs([]byte(tc.line))["job-test"]; got != tc.want {
			t.Errorf("%q: %v", tc.line, got)
		}
	}
}
