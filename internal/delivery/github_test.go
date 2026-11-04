package delivery

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const testSHA = "0123456789abcdef0123456789abcdef01234567"

func TestParseGitHubRemote(t *testing.T) {
	cases := []struct {
		url   string
		owner string
		repo  string
		ok    bool
	}{
		{"https://github.com/niparis/agent-net", "niparis", "agent-net", true},
		{"https://github.com/niparis/agent-net.git", "niparis", "agent-net", true},
		{"https://github.com/niparis/agent-net/", "niparis", "agent-net", true},
		{"https://x-access-token:abc@github.com/niparis/agent-net.git", "niparis", "agent-net", true},
		{"HTTPS://GitHub.com/niparis/agent-net.git", "niparis", "agent-net", true},
		{"git@github.com:niparis/agent-net.git", "niparis", "agent-net", true},
		{"git@github.com:niparis/agent-net", "niparis", "agent-net", true},
		{"ssh://git@github.com/niparis/agent-net.git", "niparis", "agent-net", true},
		{"ssh://git@github.com:22/niparis/agent-net.git", "niparis", "agent-net", true},
		{"  https://github.com/niparis/agent-net.git\n", "niparis", "agent-net", true},
		{"https://github.com/niparis/my.repo-1_x.git", "niparis", "my.repo-1_x", true},
		{"https://gitlab.com/niparis/agent-net.git", "", "", false},
		{"git@gitlab.com:niparis/agent-net.git", "", "", false},
		{"https://github.com.evil.example/niparis/agent-net.git", "", "", false},
		{"https://example.com/github.com/niparis/agent-net.git", "", "", false},
		{"ssh://git@example.com/niparis/agent-net.git", "", "", false},
		{"https://github.com/niparis", "", "", false},
		{"https://github.com/niparis/agent-net/tree/main", "", "", false},
		{"git@github.com:agent-net.git", "", "", false},
		{"/srv/git/agent-net.git", "", "", false},
		{"file:///srv/git/github.com/a/b.git", "", "", false},
		{"", "", "", false},
	}
	for _, c := range cases {
		owner, repo, ok := ParseGitHubRemote(c.url)
		if owner != c.owner || repo != c.repo || ok != c.ok {
			t.Errorf("ParseGitHubRemote(%q) = %q, %q, %v; want %q, %q, %v", c.url, owner, repo, ok, c.owner, c.repo, c.ok)
		}
	}
}

// fakeGitHub serves the two commit endpoints from fixed data.
type fakeGitHub struct {
	mu       sync.Mutex
	runs     []map[string]any
	statuses []map[string]any
	auth     []string
	requests []string
	posted   []map[string]any
	status   int // when not 0, every request gets this status
}

func (f *fakeGitHub) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.auth = append(f.auth, r.Header.Get("Authorization"))
		f.requests = append(f.requests, r.Method+" "+r.URL.RequestURI())
		if f.status != 0 {
			w.WriteHeader(f.status)
			fmt.Fprint(w, `{"message":"nope"}`)
			return
		}
		base := "/repos/o/r/"
		switch {
		case r.Method == http.MethodGet && r.URL.Path == base+"commits/"+testSHA+"/check-runs":
			page, per := pageOf(r)
			json.NewEncoder(w).Encode(map[string]any{"total_count": len(f.runs), "check_runs": slice(f.runs, page, per)})
		case r.Method == http.MethodGet && r.URL.Path == base+"commits/"+testSHA+"/status":
			page, per := pageOf(r)
			json.NewEncoder(w).Encode(map[string]any{"state": "pending", "total_count": len(f.statuses), "statuses": slice(f.statuses, page, per)})
		case r.Method == http.MethodPost && r.URL.Path == base+"statuses/"+testSHA:
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			f.posted = append(f.posted, body)
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"message":"Not Found"}`)
		}
	})
}

func pageOf(r *http.Request) (page, per int) {
	page, _ = strconv.Atoi(r.URL.Query().Get("page"))
	per, _ = strconv.Atoi(r.URL.Query().Get("per_page"))
	if page < 1 {
		page = 1
	}
	if per < 1 {
		per = 30
	}
	return
}

func slice(all []map[string]any, page, per int) []map[string]any {
	start := (page - 1) * per
	if start >= len(all) {
		return []map[string]any{}
	}
	end := min(start+per, len(all))
	return all[start:end]
}

func run(name, status, conclusion string) map[string]any {
	m := map[string]any{"name": name, "status": status, "html_url": "https://github.com/o/r/runs/" + name}
	if conclusion != "" {
		m["conclusion"] = conclusion
	}
	return m
}

func statusEntry(context, state string) map[string]any {
	return map[string]any{"context": context, "state": state, "target_url": "https://ci.example/" + context}
}

func newClient(t *testing.T, f *fakeGitHub) *GitHubClient {
	t.Helper()
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	return &GitHubClient{
		BaseURL: srv.URL,
		Token:   func() (string, error) { return "tok-secret", nil },
		HTTP:    srv.Client(),
	}
}

func TestCommitChecksStates(t *testing.T) {
	cases := []struct {
		name     string
		runs     []map[string]any
		statuses []map[string]any
		want     CIState
		wantURL  string
	}{
		{
			name: "pass",
			runs: []map[string]any{run("build", "completed", "success"), run("lint", "completed", "neutral"), run("docs", "completed", "skipped")},
			want: CIPass,
		},
		{
			name:     "pass with status",
			runs:     []map[string]any{run("build", "completed", "success")},
			statuses: []map[string]any{statusEntry("ci/legacy", "success")},
			want:     CIPass,
		},
		{
			name:    "fail",
			runs:    []map[string]any{run("build", "completed", "success"), run("test", "completed", "failure")},
			want:    CIFail,
			wantURL: "https://github.com/o/r/runs/test",
		},
		{
			name: "cancelled is fail",
			runs: []map[string]any{run("test", "completed", "cancelled")},
			want: CIFail,
		},
		{
			name: "timed_out is fail",
			runs: []map[string]any{run("test", "completed", "timed_out")},
			want: CIFail,
		},
		{
			name: "action_required is fail",
			runs: []map[string]any{run("test", "completed", "action_required")},
			want: CIFail,
		},
		{
			name:     "failed status is fail",
			runs:     []map[string]any{run("build", "completed", "success")},
			statuses: []map[string]any{statusEntry("ci/legacy", "failure")},
			want:     CIFail,
			wantURL:  "https://ci.example/ci/legacy",
		},
		{
			name:     "error status is fail",
			statuses: []map[string]any{statusEntry("ci/legacy", "error")},
			want:     CIFail,
		},
		{
			name:    "pending in progress",
			runs:    []map[string]any{run("build", "completed", "success"), run("test", "in_progress", "")},
			want:    CIPending,
			wantURL: "https://github.com/o/r/runs/test",
		},
		{
			name: "pending queued",
			runs: []map[string]any{run("test", "queued", "")},
			want: CIPending,
		},
		{
			name:     "pending status",
			runs:     []map[string]any{run("build", "completed", "success")},
			statuses: []map[string]any{statusEntry("ci/legacy", "pending")},
			want:     CIPending,
		},
		{
			name: "fail beats pending",
			runs: []map[string]any{run("a", "queued", ""), run("b", "completed", "failure")},
			want: CIFail,
		},
		{
			name:     "fail in status beats pending run",
			runs:     []map[string]any{run("a", "in_progress", "")},
			statuses: []map[string]any{statusEntry("ci/legacy", "failure")},
			want:     CIFail,
		},
		{
			name: "none",
			want: CINone,
		},
		{
			name:     "agent-net own statuses are not CI",
			statuses: []map[string]any{statusEntry("agent-net/review", "success")},
			want:     CINone,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeGitHub{runs: c.runs, statuses: c.statuses}
			got, err := newClient(t, f).CommitChecks(context.Background(), "o", "r", testSHA)
			if err != nil {
				t.Fatal(err)
			}
			if got.State != c.want {
				t.Fatalf("state = %q, want %q (runs %+v)", got.State, c.want, got.Runs)
			}
			if c.wantURL != "" && got.URL != c.wantURL {
				t.Fatalf("url = %q, want %q", got.URL, c.wantURL)
			}
			for _, a := range f.auth {
				if a != "Bearer tok-secret" {
					t.Fatalf("Authorization = %q", a)
				}
			}
		})
	}
}

func TestCommitChecksReportsRuns(t *testing.T) {
	f := &fakeGitHub{
		runs:     []map[string]any{run("build", "completed", "success"), run("test", "in_progress", "")},
		statuses: []map[string]any{statusEntry("ci/legacy", "failure")},
	}
	got, err := newClient(t, f).CommitChecks(context.Background(), "o", "r", testSHA)
	if err != nil {
		t.Fatal(err)
	}
	want := []CIRun{
		{Name: "build", Conclusion: "success", URL: "https://github.com/o/r/runs/build"},
		{Name: "test", Conclusion: "in_progress", URL: "https://github.com/o/r/runs/test"},
		{Name: "ci/legacy", Conclusion: "failure", URL: "https://ci.example/ci/legacy"},
	}
	if len(got.Runs) != len(want) {
		t.Fatalf("runs = %+v", got.Runs)
	}
	for i := range want {
		if got.Runs[i] != want[i] {
			t.Errorf("run %d = %+v, want %+v", i, got.Runs[i], want[i])
		}
	}
}

func TestCommitChecksPagination(t *testing.T) {
	var runs, statuses []map[string]any
	for i := range 5 {
		runs = append(runs, run(fmt.Sprintf("r%d", i), "completed", "success"))
	}
	runs[4] = run("r4", "completed", "failure") // only on the last page
	for i := range 3 {
		statuses = append(statuses, statusEntry(fmt.Sprintf("s%d", i), "success"))
	}
	f := &fakeGitHub{runs: runs, statuses: statuses}
	c := newClient(t, f)
	c.pageSize = 2
	got, err := c.CommitChecks(context.Background(), "o", "r", testSHA)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != CIFail {
		t.Fatalf("state = %q, want fail from the last page", got.State)
	}
	if len(got.Runs) != 8 {
		t.Fatalf("got %d runs, want 8: %+v", len(got.Runs), got.Runs)
	}
	pages := 0
	for _, r := range f.requests {
		if strings.Contains(r, "/check-runs?") {
			pages++
			if !strings.Contains(r, "per_page=2") {
				t.Errorf("request without per_page: %s", r)
			}
		}
	}
	if pages < 3 {
		t.Fatalf("read %d check-run pages, want at least 3: %v", pages, f.requests)
	}
}

func TestCommitChecksRequiredNames(t *testing.T) {
	runs := []map[string]any{
		run("build", "completed", "success"),
		run("flaky", "completed", "failure"),
		run("slow", "in_progress", ""),
	}
	cases := []struct {
		name     string
		required []string
		want     CIState
	}{
		{"no filter sees the failure", nil, CIFail},
		{"only passing required", []string{"build"}, CIPass},
		{"failing required", []string{"build", "flaky"}, CIFail},
		{"pending required", []string{"build", "slow"}, CIPending},
		{"missing required has not reported", []string{"build", "deploy"}, CIPending},
		{"missing required, another failed", []string{"flaky", "deploy"}, CIFail},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeGitHub{runs: runs}
			got, err := newClient(t, f).CommitChecks(context.Background(), "o", "r", testSHA, c.required...)
			if err != nil {
				t.Fatal(err)
			}
			if got.State != c.want {
				t.Fatalf("state = %q, want %q", got.State, c.want)
			}
			if len(c.required) > 0 {
				for _, r := range got.Runs {
					found := false
					for _, n := range c.required {
						found = found || n == r.Name
					}
					if !found {
						t.Errorf("run %q is not required but is in the result", r.Name)
					}
				}
			}
		})
	}
}

func TestCommitChecksRequiredWithNoChecksIsNone(t *testing.T) {
	got, err := newClient(t, &fakeGitHub{}).CommitChecks(context.Background(), "o", "r", testSHA, "build")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != CINone {
		t.Fatalf("state = %q, want none", got.State)
	}
}

func TestCommitChecksRequiredFromStatus(t *testing.T) {
	f := &fakeGitHub{statuses: []map[string]any{statusEntry("ci/legacy", "success"), statusEntry("other", "failure")}}
	got, err := newClient(t, f).CommitChecks(context.Background(), "o", "r", testSHA, "ci/legacy")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != CIPass {
		t.Fatalf("state = %q, want pass", got.State)
	}
}

func TestCommitChecksNotFound(t *testing.T) {
	f := &fakeGitHub{status: http.StatusNotFound}
	_, err := newClient(t, f).CommitChecks(context.Background(), "o", "r", testSHA)
	if err == nil {
		t.Fatal("want an error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "o/r") || !strings.Contains(msg, "not found") {
		t.Fatalf("error should say the repo or commit was not found: %v", err)
	}
	if strings.Contains(msg, "tok-secret") {
		t.Fatal("error leaks the token")
	}
}

func TestCommitChecksUnauthorized(t *testing.T) {
	f := &fakeGitHub{status: http.StatusUnauthorized}
	_, err := newClient(t, f).CommitChecks(context.Background(), "o", "r", testSHA)
	if err == nil {
		t.Fatal("want an error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "token") || !strings.Contains(msg, "access") {
		t.Fatalf("error should say the token lacks access: %v", err)
	}
	if strings.Contains(msg, "tok-secret") {
		t.Fatal("error leaks the token")
	}
}

func TestCommitChecksForbidden(t *testing.T) {
	f := &fakeGitHub{status: http.StatusForbidden}
	_, err := newClient(t, f).CommitChecks(context.Background(), "o", "r", testSHA)
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("want a 403 error, got %v", err)
	}
}

func TestCommitChecksServerError(t *testing.T) {
	f := &fakeGitHub{status: http.StatusBadGateway}
	_, err := newClient(t, f).CommitChecks(context.Background(), "o", "r", testSHA)
	if err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("want a 502 error, got %v", err)
	}
}

func TestCommitChecksTokenError(t *testing.T) {
	f := &fakeGitHub{}
	c := newClient(t, f)
	c.Token = func() (string, error) { return "", fmt.Errorf("no token here") }
	_, err := c.CommitChecks(context.Background(), "o", "r", testSHA)
	if err == nil || !strings.Contains(err.Error(), "no token here") {
		t.Fatalf("want the token error, got %v", err)
	}
	if len(f.requests) != 0 {
		t.Fatalf("no request should be sent without a token: %v", f.requests)
	}
}

func TestCommitChecksRejectsBadInput(t *testing.T) {
	c := newClient(t, &fakeGitHub{})
	for _, sha := range []string{"", "main", "../x", "abc/../../x", "zzzzzzz", testSHA[:7], strings.ToUpper(testSHA)} {
		if _, err := c.CommitChecks(context.Background(), "o", "r", sha); err == nil {
			t.Errorf("sha %q: want an error", sha)
		}
	}
	for _, o := range [][2]string{{"", "r"}, {"o", ""}, {"o/x", "r"}, {"o", "r?x=1"}} {
		if _, err := c.CommitChecks(context.Background(), o[0], o[1], testSHA); err == nil {
			t.Errorf("owner/repo %q/%q: want an error", o[0], o[1])
		}
	}
}

func TestPostCommitStatus(t *testing.T) {
	f := &fakeGitHub{}
	c := newClient(t, f)
	err := c.PostCommitStatus(context.Background(), "o", "r", testSHA, "agent-net/review", "success", "review passed", "https://example.com/evidence/1")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.posted) != 1 {
		t.Fatalf("posted %d statuses", len(f.posted))
	}
	got := f.posted[0]
	want := map[string]string{"state": "success", "context": "agent-net/review", "description": "review passed", "target_url": "https://example.com/evidence/1"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %q", k, got[k], v)
		}
	}
	if f.auth[0] != "Bearer tok-secret" {
		t.Errorf("Authorization = %q", f.auth[0])
	}
}

func TestPostCommitStatusOmitsEmptyTargetURL(t *testing.T) {
	f := &fakeGitHub{}
	if err := newClient(t, f).PostCommitStatus(context.Background(), "o", "r", testSHA, "agent-net/test", "pending", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.posted[0]["target_url"]; ok {
		t.Fatalf("empty target_url should be left out: %v", f.posted[0])
	}
}

func TestPostCommitStatusTruncatesDescription(t *testing.T) {
	f := &fakeGitHub{}
	long := strings.Repeat("é", 300)
	if err := newClient(t, f).PostCommitStatus(context.Background(), "o", "r", testSHA, "agent-net/test", "success", long, ""); err != nil {
		t.Fatal(err)
	}
	d, _ := f.posted[0]["description"].(string)
	if n := len([]rune(d)); n == 0 || n > 140 {
		t.Fatalf("description has %d runes, want 1..140", n)
	}
}

func TestPostCommitStatusValidation(t *testing.T) {
	f := &fakeGitHub{}
	c := newClient(t, f)
	bad := []struct{ context, state string }{
		{"ci/build", "success"},
		{"agent-net", "success"},
		{"agent-net/", "success"},
		{"agent-net/review", "passed"},
		{"agent-net/review", ""},
	}
	for _, b := range bad {
		if err := c.PostCommitStatus(context.Background(), "o", "r", testSHA, b.context, b.state, "d", ""); err == nil {
			t.Errorf("context %q state %q: want an error", b.context, b.state)
		}
	}
	if len(f.requests) != 0 {
		t.Fatalf("invalid calls must not reach GitHub: %v", f.requests)
	}
}

func TestPostCommitStatusErrors(t *testing.T) {
	for code, want := range map[int]string{http.StatusUnauthorized: "access", http.StatusNotFound: "not found", http.StatusInternalServerError: "500"} {
		f := &fakeGitHub{status: code}
		err := newClient(t, f).PostCommitStatus(context.Background(), "o", "r", testSHA, "agent-net/test", "success", "d", "")
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("status %d: error %v should contain %q", code, err, want)
		}
	}
}

func TestDefaultTokenFromEnv(t *testing.T) {
	t.Setenv("GH_TOKEN", "from-gh-token")
	t.Setenv("GITHUB_TOKEN", "from-github-token")
	got, err := DefaultToken()
	if err != nil || got != "from-gh-token" {
		t.Fatalf("got %q, %v; GH_TOKEN should win", got, err)
	}
	t.Setenv("GH_TOKEN", "")
	got, err = DefaultToken()
	if err != nil || got != "from-github-token" {
		t.Fatalf("got %q, %v; want GITHUB_TOKEN", got, err)
	}
}

func fakeGH(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

func TestDefaultTokenFromGh(t *testing.T) {
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	fakeGH(t, `[ "$1 $2" = "auth token" ] && [ $# -eq 2 ] && echo "gh-token-123" && exit 0
exit 1
`)
	got, err := DefaultToken()
	if err != nil || got != "gh-token-123" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestDefaultTokenGhFails(t *testing.T) {
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	fakeGH(t, "echo 'not logged in' >&2\nexit 1\n")
	_, err := DefaultToken()
	if err == nil || !strings.Contains(err.Error(), "GH_TOKEN") {
		t.Fatalf("want an error that names GH_TOKEN, got %v", err)
	}
}

func TestDefaultTokenNoGh(t *testing.T) {
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("PATH", t.TempDir())
	if _, err := DefaultToken(); err == nil {
		t.Fatal("want an error when there is no token and no gh")
	}
}

func TestDefaultTokenGhTimeout(t *testing.T) {
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	fakeGH(t, "exec /bin/sleep 30\n")
	old := ghTokenTimeout
	ghTokenTimeout = 200 * time.Millisecond
	defer func() { ghTokenTimeout = old }()
	start := time.Now()
	if _, err := DefaultToken(); err == nil {
		t.Fatal("want an error when gh hangs")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("gh was not stopped by the timeout")
	}
}

func TestNewGitHubClientDefaults(t *testing.T) {
	c := NewGitHubClient()
	if c.BaseURL != "https://api.github.com" || c.Token == nil || c.HTTP == nil || c.HTTP.Timeout == 0 {
		t.Fatalf("bad defaults: %+v", c)
	}
}
