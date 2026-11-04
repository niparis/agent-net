package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// CIState is the verdict of GitHub's CI on one commit.
type CIState string

const (
	CIPass    CIState = "pass"
	CIFail    CIState = "fail"
	CIPending CIState = "pending"
	// CINone: the commit has no checks at all and no names were required.
	// None is never a pass: a step that needs CI must wait or refuse.
	CINone CIState = "none"
)

// CIRun is one check run or one commit status. For a check run, Conclusion is
// its conclusion, or its status while it has not finished. For a commit
// status, it is the status state.
type CIRun struct {
	Name       string
	Conclusion string
	URL        string
}

// CIResult is what GitHub's CI says about one commit.
type CIResult struct {
	State CIState
	Runs  []CIRun
	// URL is the page to open for the verdict: the first failed run, else the
	// first pending run, else the first run. Empty when there are no runs.
	URL string
}

// StatusContextPrefix starts the name of every status agent-net posts.
const StatusContextPrefix = "agent-net/"

const (
	defaultGitHubAPI  = "https://api.github.com"
	defaultPageSize   = 100
	maxPages          = 50
	maxResponseBytes  = 8 << 20
	maxDescriptionLen = 140 // GitHub's limit for a status description
)

// ghTokenTimeout bounds `gh auth token`. A variable so tests can shorten it.
var ghTokenTimeout = 10 * time.Second

// GitHubClient reads CI results from GitHub and posts commit statuses.
type GitHubClient struct {
	// BaseURL is the API root, https://api.github.com by default.
	BaseURL string
	// Token returns the API token. It is called once per CommitChecks or
	// PostCommitStatus call, however many pages that call reads. A nil Token
	// sends requests without one.
	Token func(ctx context.Context) (string, error)
	// HTTP is the HTTP client. Nil means a client with a 30 second timeout.
	HTTP *http.Client

	pageSize int // 0 means defaultPageSize; tests lower it
}

// NewGitHubClient returns a client for api.github.com that takes its token
// from DefaultToken.
func NewGitHubClient() *GitHubClient {
	return &GitHubClient{
		BaseURL: defaultGitHubAPI,
		Token:   DefaultToken,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

// DefaultToken returns the token in GH_TOKEN, else in GITHUB_TOKEN, else the
// one `gh auth token` prints, bounded by ctx and ghTokenTimeout. The token is
// never logged or put in an error.
func DefaultToken(ctx context.Context) (string, error) {
	for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			return v, nil
		}
	}
	ctx, cancel := context.WithTimeout(ctx, ghTokenTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "auth", "token")
	cmd.WaitDelay = time.Second
	var out bytes.Buffer
	cmd.Stdout = &out // stderr is dropped: it is not needed and could echo secrets
	if err := cmd.Run(); err == nil {
		if v := strings.TrimSpace(out.String()); v != "" {
			return v, nil
		}
	}
	return "", errors.New("no GitHub token: set GH_TOKEN or GITHUB_TOKEN, or run `gh auth login`")
}

var (
	scpRemote   = regexp.MustCompile(`^[A-Za-z0-9._-]+@github\.com:([^/]+)/([^/]+)$`)
	namePattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
)

// ParseGitHubRemote returns the owner and repo of a github.com remote URL in
// https, scp-style ssh (git@github.com:o/r.git) or ssh:// form. Any other
// host gives ok=false.
func ParseGitHubRemote(remote string) (owner, repo string, ok bool) {
	remote = strings.TrimSpace(remote)
	var path string
	if m := scpRemote.FindStringSubmatch(remote); m != nil {
		path = m[1] + "/" + m[2]
	} else {
		u, err := url.Parse(remote)
		if err != nil {
			return "", "", false
		}
		switch strings.ToLower(u.Scheme) {
		case "https", "ssh":
		default:
			return "", "", false
		}
		if !strings.EqualFold(u.Hostname(), "github.com") {
			return "", "", false
		}
		path = strings.Trim(u.Path, "/")
	}
	path = strings.TrimSuffix(path, "/")
	path = strings.TrimSuffix(path, ".git")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || !validName(parts[0]) || !validName(parts[1]) {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func validName(s string) bool {
	return namePattern.MatchString(s) && s != "." && s != ".."
}

func checkTarget(owner, repo, sha string) error {
	if !validName(owner) || !validName(repo) {
		return fmt.Errorf("github: invalid repository %q/%q", owner, repo)
	}
	if err := checkCommitID("github: commit", sha); err != nil {
		return err
	}
	return nil
}

// CommitChecks reads the check runs and the commit statuses of one commit and
// reduces them to a verdict.
//
//   - Any failure, error, cancelled, timed_out or action_required is fail.
//   - Otherwise any run that has not finished (or is stale) is pending.
//   - A commit with no checks at all is none.
//   - Otherwise it is pass.
//
// Statuses whose context starts with "agent-net/" are agent-net's own
// evidence and are not counted as CI, so such a name cannot be required.
//
// When required names are given, only runs and statuses with those names
// count, and a required name that has not reported yet makes the result
// pending, unless something already failed. With required names the result
// is never none. Without them, a commit with no checks at all is none, and
// none is never a pass.
func (c *GitHubClient) CommitChecks(ctx context.Context, owner, repo, sha string, required ...string) (CIResult, error) {
	if err := checkTarget(owner, repo, sha); err != nil {
		return CIResult{}, err
	}
	for _, n := range required {
		if n == "" || strings.HasPrefix(n, StatusContextPrefix) {
			return CIResult{}, fmt.Errorf("github: required check %q: names starting with %q are agent-net's own statuses, never CI", n, StatusContextPrefix)
		}
	}
	tok, err := c.token(ctx)
	if err != nil {
		return CIResult{}, err
	}
	base := "/repos/" + owner + "/" + repo + "/commits/" + sha
	subject := owner + "/" + repo + "@" + sha

	var all []checkEntry

	err = c.paginate(ctx, tok, base+"/check-runs", subject, func(body []byte) (int, error) {
		var page struct {
			CheckRuns []struct {
				Name       string `json:"name"`
				Status     string `json:"status"`
				Conclusion string `json:"conclusion"`
				HTMLURL    string `json:"html_url"`
			} `json:"check_runs"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			return 0, err
		}
		for _, r := range page.CheckRuns {
			e := checkEntry{run: CIRun{Name: r.Name, Conclusion: r.Conclusion, URL: r.HTMLURL}}
			if r.Status != "completed" {
				e.run.Conclusion = r.Status
				e.state = CIPending
			} else {
				e.state = runState(r.Conclusion)
			}
			all = append(all, e)
		}
		return len(page.CheckRuns), nil
	})
	if err != nil {
		return CIResult{}, err
	}

	err = c.paginate(ctx, tok, base+"/status", subject, func(body []byte) (int, error) {
		var page struct {
			Statuses []struct {
				Context   string `json:"context"`
				State     string `json:"state"`
				TargetURL string `json:"target_url"`
			} `json:"statuses"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			return 0, err
		}
		for _, s := range page.Statuses {
			if strings.HasPrefix(s.Context, StatusContextPrefix) {
				continue
			}
			e := checkEntry{run: CIRun{Name: s.Context, Conclusion: s.State, URL: s.TargetURL}}
			switch s.State {
			case "success":
				e.state = CIPass
			case "failure", "error":
				e.state = CIFail
			default:
				e.state = CIPending
			}
			all = append(all, e)
		}
		return len(page.Statuses), nil
	})
	if err != nil {
		return CIResult{}, err
	}

	return reduceChecks(all, required), nil
}

type checkEntry struct {
	run   CIRun
	state CIState // pass, fail or pending
}

func runState(conclusion string) CIState {
	switch conclusion {
	case "success", "neutral", "skipped":
		return CIPass
	case "failure", "cancelled", "timed_out", "action_required":
		return CIFail
	default: // stale, or a conclusion we do not know: do not call it a pass
		return CIPending
	}
}

func reduceChecks(all []checkEntry, required []string) CIResult {
	if len(all) == 0 && len(required) == 0 {
		return CIResult{State: CINone}
	}
	var counted []checkEntry
	missing := false
	if len(required) == 0 {
		counted = all
	} else {
		want := map[string]bool{}
		for _, n := range required {
			want[n] = true
		}
		seen := map[string]bool{}
		for _, e := range all {
			if want[e.run.Name] {
				counted = append(counted, e)
				seen[e.run.Name] = true
			}
		}
		for n := range want {
			if !seen[n] {
				missing = true
			}
		}
	}

	res := CIResult{State: CIPass}
	var failURL, pendURL string
	failed, pending := false, false
	for i, e := range counted {
		res.Runs = append(res.Runs, e.run)
		if i == 0 {
			res.URL = e.run.URL
		}
		switch e.state {
		case CIFail:
			if !failed {
				failURL = e.run.URL
			}
			failed = true
		case CIPending:
			if !pending {
				pendURL = e.run.URL
			}
			pending = true
		}
	}
	switch {
	case failed:
		res.State = CIFail
		res.URL = failURL
	case pending || missing:
		res.State = CIPending
		if pending {
			res.URL = pendURL
		}
	}
	return res
}

// paginate fetches pages of path until a page is short or empty. handle gets
// each body and returns how many items it held.
func (c *GitHubClient) paginate(ctx context.Context, tok, path, subject string, handle func(body []byte) (int, error)) error {
	size := c.pageSize
	if size <= 0 {
		size = defaultPageSize
	}
	for page := 1; page <= maxPages; page++ {
		body, err := c.do(ctx, tok, http.MethodGet, fmt.Sprintf("%s?per_page=%d&page=%d", path, size, page), nil, subject)
		if err != nil {
			return err
		}
		n, err := handle(body)
		if err != nil {
			return fmt.Errorf("github: bad response for %s: %w", subject, err)
		}
		if n < size {
			return nil
		}
	}
	return fmt.Errorf("github: more than %d pages of checks for %s", maxPages, subject)
}

// PostCommitStatus posts a commit status, so branch protection can require
// it. The context must start with "agent-net/". The state is one of error,
// failure, pending or success. A description over 140 characters is cut. An
// empty targetURL is left out.
func (c *GitHubClient) PostCommitStatus(ctx context.Context, owner, repo, sha, statusContext, state, description, targetURL string) error {
	if err := checkTarget(owner, repo, sha); err != nil {
		return err
	}
	if !strings.HasPrefix(statusContext, StatusContextPrefix) || len(statusContext) == len(StatusContextPrefix) {
		return fmt.Errorf("github: status context %q must start with %q and name the step", statusContext, StatusContextPrefix)
	}
	switch state {
	case "error", "failure", "pending", "success":
	default:
		return fmt.Errorf("github: status state %q must be error, failure, pending or success", state)
	}
	if r := []rune(description); len(r) > maxDescriptionLen {
		description = string(r[:maxDescriptionLen-1]) + "…"
	}
	payload := map[string]string{"state": state, "context": statusContext, "description": description}
	if targetURL != "" {
		payload["target_url"] = targetURL
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	tok, err := c.token(ctx)
	if err != nil {
		return err
	}
	_, err = c.do(ctx, tok, http.MethodPost, "/repos/"+owner+"/"+repo+"/statuses/"+sha, data, owner+"/"+repo+"@"+sha)
	return err
}

// token fetches the token for one call. "" means no Authorization header.
func (c *GitHubClient) token(ctx context.Context) (string, error) {
	if c.Token == nil {
		return "", nil
	}
	return c.Token(ctx)
}

// do sends one request and returns the body of a 2xx answer. subject names
// the repo and commit in error messages.
func (c *GitHubClient) do(ctx context.Context, tok, method, path string, body []byte, subject string) ([]byte, error) {
	base := c.BaseURL
	if base == "" {
		base = defaultGitHubAPI
	}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(base, "/")+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "agent-net")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		// The *url.Error text holds the URL, never the headers.
		return nil, fmt.Errorf("github: %s %s: %w", method, subject, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("github: reading answer for %s: %w", subject, err)
	}
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return data, nil
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, fmt.Errorf("github: 401 for %s: the token is missing, expired or lacks access to this repository", subject)
	case resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("github: 403 for %s: the token lacks access, or the rate limit is used up", subject)
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("github: repository or commit not found: %s (a private repository also answers 404 when the token lacks access)", subject)
	default:
		return nil, fmt.Errorf("github: HTTP %d for %s", resp.StatusCode, subject)
	}
}
