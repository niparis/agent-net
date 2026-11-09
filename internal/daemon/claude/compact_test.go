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

// fakeCompactOutput appends the entries a real /compact writes to a
// transcript: the compact boundary, the summary, the local command entries
// (isMeta caveat, /compact command, its stdout) and an attachment. Checked
// against real transcripts; the JSON here is synthetic.
const fakeCompactOutput = "printf '%s\\n' " +
	"'{\"type\":\"system\",\"subtype\":\"compact_boundary\",\"compactMetadata\":{\"trigger\":\"manual\",\"preTokens\":300000,\"postTokens\":20000}}' " +
	"'{\"type\":\"user\",\"isCompactSummary\":true,\"message\":{\"role\":\"user\",\"content\":\"This session is being continued from a previous conversation.\"}}' " +
	"'{\"type\":\"user\",\"isMeta\":true,\"message\":{\"role\":\"user\",\"content\":\"<local-command-caveat>The command below was run directly in Claude Code, not sent to you as a message.</local-command-caveat>\"}}' " +
	"'{\"type\":\"user\",\"message\":{\"role\":\"user\",\"content\":\"<command-name>/compact</command-name> <command-message>compact</command-message> <command-args></command-args>\"}}' " +
	"'{\"type\":\"user\",\"message\":{\"role\":\"user\",\"content\":\"<local-command-stdout>Compacted (ctrl+o to see full summary)</local-command-stdout>\"}}' " +
	"'{\"type\":\"attachment\",\"attachment\":{\"type\":\"file\"}}' " +
	">> \"$COMPACT_TRANSCRIPT\"\n"

func TestIdleCompact(t *testing.T) {
	for _, tc := range []struct {
		name, status, kind                             string
		tokens                                         int
		age                                            time.Duration
		attached, noPrompt, disabled, scanError, noJob bool
		clients                                        string // ps output; the clients' pids are asked to the host
		viewing                                        bool   // the host says the client's herdr pane is focused, or outside herdr
		wantLog                                        string
		wantScans                                      int // herdr scans: none when no candidate passes the token check
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
		{name: "attach client in an unfocused herdr pane", status: "idle", kind: "bg", tokens: 300000, age: time.Hour, clients: "77 claude attach job-test", want: 1, wantLog: "event=idle_compact session=test result=ok", wantScans: 2},
		{name: "attach client in a focused herdr pane or outside herdr", status: "idle", kind: "bg", tokens: 300000, age: time.Hour, clients: "77 claude attach job-test", viewing: true, wantLog: "event=idle_compact session=test result=skipped:viewing", wantScans: 2},
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
			script += "IFS= read -r line\n[ \"$1\" = attach ] && [ \"$2\" = job-test ] && [ \"$line\" = \"$COMPACT_WANT\" ] || exit 12\n" + fakeCompactOutput + "IFS= read -r line\n"
			if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("COMPACT_TRANSCRIPT", transcript)
			t.Setenv("COMPACT_WANT", "/compact "+compactInstructions)
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
				h.mu.Lock()
				scans := h.scans
				h.mu.Unlock()
				if scans != tc.wantScans {
					t.Errorf("%d herdr scans, want %d", scans, tc.wantScans)
				}
				if n != 1 {
					t.Errorf("%d event lines for one idle stretch, want 1; logs=%v", n, h.logs)
				}
				if tc.want > 0 {
					if got := h.current()[0].IdleCompactedAt; got != started.Unix() {
						t.Fatalf("note timestamp=%d", got)
					}
					// A real busy observation starts a new stretch; polling idle alone did not.
					// A busy turn writes to the transcript, so write one too: the
					// transcript, not memory, is what survives a restart.
					if f, err := os.OpenFile(transcript, os.O_APPEND|os.O_WRONLY, 0600); err != nil {
						t.Fatal(err)
					} else {
						_, err = f.WriteString(fmt.Sprintf("{\"type\":\"user\",\"message\":{\"role\":\"user\",\"content\":\"again\"}}\n{\"type\":\"assistant\",\"message\":{\"usage\":{\"input_tokens\":10,\"cache_creation_input_tokens\":20,\"cache_read_input_tokens\":%d}}}\n", tc.tokens-30))
						f.Close()
						if err != nil {
							t.Fatal(err)
						}
					}
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
	scans   int // calls of AttachClientsViewing: one per scan
}

func (h *compactHost) AttachClientsViewing(pids []int) map[int]bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.scans++
	out := map[int]bool{}
	for _, pid := range pids {
		out[pid] = h.viewing[pid]
	}
	return out
}

func (h *compactHost) SettingsSnapshot() settings.Snapshot { return h.snap }

// A fresh daemon (an empty compactStates) must read the "already compacted,
// nothing since" answer from the transcript, so a restart does not compact a
// session that has had no turn since its last compaction. These transcripts are
// synthetic copies of the shapes Claude Code writes (type=system
// subtype=compact_boundary, then a type=user entry with isCompactSummary).
func TestIdleCompactNoActivitySinceCompact(t *testing.T) {
	const (
		assistant   = `{"type":"assistant","message":{"usage":{"input_tokens":10,"cache_creation_input_tokens":20,"cache_read_input_tokens":299970}}}` + "\n"
		boundary    = `{"type":"system","subtype":"compact_boundary","compactMetadata":{"trigger":"manual","preTokens":300000,"postTokens":20000}}` + "\n"
		summary     = `{"type":"user","isCompactSummary":true,"message":{"role":"user","content":"This session is being continued from a previous conversation."}}` + "\n"
		attachment  = `{"type":"attachment","attachment":{"type":"file"}}` + "\n"
		bookkeeping = `{"type":"last-prompt","lastPrompt":"older prompt"}` + "\n"
		// The exact entries a real /compact writes after the boundary and the
		// summary (checked against transcripts on this machine, read-only).
		compactCommand = summary +
			`{"type":"user","isMeta":true,"message":{"role":"user","content":"<local-command-caveat>The command below was run directly in Claude Code, not sent to you as a message.</local-command-caveat>"}}` + "\n" +
			`{"type":"user","message":{"role":"user","content":"<command-name>/compact</command-name>             <command-message>compact</command-message>             <command-args></command-args>"}}` + "\n" +
			`{"type":"user","message":{"role":"user","content":"<local-command-stdout>\u001b[2mCompacted (ctrl+o to see full summary)\u001b[22m</local-command-stdout>"}}` + "\n"
		newTurn = `{"type":"user","message":{"role":"user","content":"a new question"}}` + "\n" + assistant
	)
	skip := "event=idle_compact session=test result=skipped:no_activity_since_compact"
	for _, tc := range []struct {
		name       string
		body       string
		wantLog    string
		want       int   // new boundaries written by the fake claude
		wantTokens int64 // contextTokens before compaction
	}{
		{name: "transcript ends at the compact summary", body: assistant + boundary + summary, wantLog: skip, wantTokens: 20000},
		{name: "transcript ends at the compact boundary", body: assistant + boundary, wantLog: skip, wantTokens: 20000},
		{name: "bookkeeping after the compaction is not a turn", body: assistant + boundary + summary + attachment + bookkeeping, wantLog: skip, wantTokens: 20000},
		{name: "the command entries of a daemon /compact are not a turn", body: assistant + boundary + compactCommand + attachment, wantLog: skip, wantTokens: 20000},
		{name: "a turn after the compaction compacts again", body: assistant + boundary + summary + newTurn, wantLog: "event=idle_compact session=test result=ok", want: 1, wantTokens: 300000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			sessions := filepath.Join(dir, "sessions")
			t.Setenv("AGENT_NET_CLAUDE_SESSIONS_DIR", sessions)
			projects := filepath.Join(dir, "projects")
			td := filepath.Join(projects, transcriptDir(dir))
			if err := os.MkdirAll(sessions, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(td, 0700); err != nil {
				t.Fatal(err)
			}
			transcript := filepath.Join(td, "test.jsonl")
			if err := os.WriteFile(transcript, []byte(tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			sf := filepath.Join(sessions, fmt.Sprintf("%d.json", os.Getpid()))
			if err := os.WriteFile(sf, []byte(fmt.Sprintf(`{"pid":%d,"sessionId":"test","cwd":%q,"kind":"bg","jobId":"job-test","status":"idle"}`, os.Getpid(), dir)), 0600); err != nil {
				t.Fatal(err)
			}
			old := time.Now().Add(-2 * time.Hour)
			for _, p := range []string{sf, transcript} {
				if err := os.Chtimes(p, old, old); err != nil {
					t.Fatal(err)
				}
			}
			bin := filepath.Join(dir, "claude")
			script := "#!/bin/sh\nstty -echo\nprintf x >> \"$COMPACT_STARTS\"\nprintf '❯'\nIFS= read -r line\n[ \"$1\" = attach ] && [ \"$2\" = job-test ] && [ \"$line\" = \"$COMPACT_WANT\" ] || exit 12\n" + fakeCompactOutput + "IFS= read -r line\n"
			if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("COMPACT_TRANSCRIPT", transcript)
			t.Setenv("COMPACT_WANT", "/compact "+compactInstructions)
			starts := filepath.Join(dir, "starts")
			t.Setenv("COMPACT_STARTS", starts)
			h := &compactHost{fakeHost: &fakeHost{}, snap: settings.Snapshot{}}
			// A low threshold, so the old in-memory code would compact here: this
			// test is about the activity check, not the token check.
			h.snap.Values = []settings.Value{{Key: "sessions.idle_compaction_min_tokens", Level: "network", Scope: "network", Value: []byte("1000")}}
			a := New(Config{SessionsDir: sessions, ProjectsDir: projects}) // empty compactStates: a fresh daemon
			a.host = h
			a.compactBinary = bin
			a.compactPromptTimeout = 100 * time.Millisecond
			a.compactTimeout = time.Second
			a.compactAttached = func(context.Context) (map[string]bool, error) { return map[string]bool{}, nil }
			if got := contextTokens(transcript); got != tc.wantTokens {
				t.Fatalf("contextTokens=%d, want %d", got, tc.wantTokens)
			}
			a.poll()
			a.compactOnce(context.Background())
			data, err := os.ReadFile(transcript)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Count(string(data), `"compact_boundary"`) - strings.Count(tc.body, `"compact_boundary"`); got != tc.want {
				t.Fatalf("new boundaries=%d, want %d; logs=%v", got, tc.want, h.logs)
			}
			startBytes, _ := os.ReadFile(starts)
			if len(startBytes) != tc.want {
				t.Fatalf("attach attempts=%d, want %d", len(startBytes), tc.want)
			}
			found := false
			for _, l := range h.logs {
				if strings.Contains(l, tc.wantLog) {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing log %q; logs=%v", tc.wantLog, h.logs)
			}
		})
	}
}

// The daemon types "/compact " and the fixed instructions, then a carriage
// return, into the attach terminal. The fake claude reads the raw bytes (raw
// mode keeps the \r) and checks them byte for byte.
func TestIdleCompactTypesFixedInstructions(t *testing.T) {
	const assistant = `{"type":"assistant","message":{"usage":{"input_tokens":10,"cache_creation_input_tokens":20,"cache_read_input_tokens":299970}}}` + "\n"
	dir := t.TempDir()
	sessions := filepath.Join(dir, "sessions")
	t.Setenv("AGENT_NET_CLAUDE_SESSIONS_DIR", sessions)
	projects := filepath.Join(dir, "projects")
	td := filepath.Join(projects, transcriptDir(dir))
	if err := os.MkdirAll(sessions, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(td, 0700); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(td, "test.jsonl")
	if err := os.WriteFile(transcript, []byte(assistant), 0600); err != nil {
		t.Fatal(err)
	}
	sf := filepath.Join(sessions, fmt.Sprintf("%d.json", os.Getpid()))
	if err := os.WriteFile(sf, []byte(fmt.Sprintf(`{"pid":%d,"sessionId":"test","cwd":%q,"kind":"bg","jobId":"job-test","status":"idle"}`, os.Getpid(), dir)), 0600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	for _, p := range []string{sf, transcript} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	want := "/compact " + compactInstructions + "\r"
	bin := filepath.Join(dir, "claude")
	script := fmt.Sprintf("#!/bin/sh\nstty raw -echo\nprintf '❯'\ndd bs=1 count=%d of=\"$COMPACT_LINE\" 2>/dev/null\n", len(want)) + fakeCompactOutput + "IFS= read -r line\n"
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COMPACT_TRANSCRIPT", transcript)
	line := filepath.Join(dir, "typed")
	t.Setenv("COMPACT_LINE", line)
	h := &compactHost{fakeHost: &fakeHost{}, snap: settings.Snapshot{}}
	h.snap.Values = []settings.Value{{Key: "sessions.idle_compaction_min_tokens", Level: "network", Scope: "network", Value: []byte("1000")}}
	a := New(Config{SessionsDir: sessions, ProjectsDir: projects})
	a.host = h
	a.compactBinary = bin
	a.compactPromptTimeout = 100 * time.Millisecond
	a.compactTimeout = time.Second
	a.compactAttached = func(context.Context) (map[string]bool, error) { return map[string]bool{}, nil }
	a.poll()
	a.compactOnce(context.Background())
	got, err := os.ReadFile(line)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("typed %q, want %q; logs=%v", got, want, h.logs)
	}
}

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
