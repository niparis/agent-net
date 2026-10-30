package hint

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func cands() []Candidate {
	return []Candidate{
		{SessionID: "s_cog", Name: "cogforge", Hostname: "laptop-a", Online: true, Project: "cogforge",
			Remote: "git@example.com:acme/cogforge.git", Folder: "cogforge",
			Task: "fixing the retry queue", Expertise: "cogforge API and its deployment"},
		{SessionID: "s_home", Name: "homelab", Hostname: "homelab-box", Online: true, Project: "infra",
			Folder: "infra", Expertise: "live logs of homelab services; docker compose stacks"},
		{SessionID: "s_off", Name: "cogforge-old", Hostname: "laptop-a", Online: false, Project: "cogforge"},
		{SessionID: "s_me", Name: "client", Hostname: "laptop-b", Online: true, Project: "client-app"},
		{SessionID: "s_you", Name: "you (on laptop-b)", Hostname: "laptop-b", Online: true, Owner: true},
		{SessionID: "s_other", Name: "docs", Hostname: "laptop-a", Online: true, Project: "docs-site", Folder: "docs-site"},
	}
}

const e503 = `curl: (22) The requested URL returned error: 503
HTTP/1.1 503 Service Unavailable
POST https://cogforge.example.com/api/v1/jobs failed`

func TestSuggestFindsTheOwnerOfAService(t *testing.T) {
	got := Suggest(e503, Self{SessionID: "s_me", Project: "client-app", Hostname: "laptop-b"}, cands())
	if len(got) == 0 || got[0].Candidate.SessionID != "s_cog" {
		t.Fatalf("want cogforge first, got %+v", got)
	}
	if !strings.Contains(got[0].Why, "cogforge") {
		t.Errorf("why = %q", got[0].Why)
	}
	for _, g := range got {
		if g.Candidate.SessionID == "s_off" || g.Candidate.SessionID == "s_me" || g.Candidate.SessionID == "s_you" {
			t.Errorf("must not suggest %s", g.Candidate.SessionID)
		}
	}
}

func TestSuggestUsesExpertiseWords(t *testing.T) {
	// No name in the error, but two words of the homelab expertise.
	text := "docker compose: service unavailable, see the homelab services stack"
	got := Suggest(text, Self{SessionID: "s_me", Project: "client-app", Hostname: "laptop-b"}, cands())
	if len(got) != 1 || got[0].Candidate.SessionID != "s_home" {
		t.Fatalf("got %+v", got)
	}
}

func TestSuggestNeedsMoreThanOneGenericWord(t *testing.T) {
	got := Suggest("error: connection refused (api service logs)", Self{SessionID: "s_me", Project: "client-app"}, cands())
	if len(got) != 0 {
		t.Fatalf("generic words must not match: %+v", got)
	}
}

func TestSuggestSkipsSelfAndSameProject(t *testing.T) {
	got := Suggest(e503, Self{SessionID: "s_cog", Project: "cogforge"}, cands())
	for _, g := range got {
		if g.Candidate.Project == "cogforge" {
			t.Errorf("suggested a session of the same project: %+v", g)
		}
	}
	// A second session of the same project: still not suggested.
	got = Suggest(e503, Self{SessionID: "s_x", Project: "Cogforge"}, cands())
	if len(got) != 0 {
		t.Errorf("got %+v", got)
	}
}

func TestSuggestIgnoresHostOfSelf(t *testing.T) {
	c := []Candidate{{SessionID: "s_a", Name: "a", Hostname: "laptop-a", Online: true, Project: "x1"}}
	got := Suggest("cannot reach /home/u/laptop-a/data", Self{SessionID: "s_me", Hostname: "laptop-a"}, c)
	if len(got) != 0 {
		t.Errorf("host of self matched: %+v", got)
	}
	got = Suggest("ssh: connect to host laptop-a port 22: Connection refused", Self{SessionID: "s_me", Hostname: "laptop-b"}, c)
	if len(got) != 1 {
		t.Errorf("host of another machine should match: %+v", got)
	}
}

func TestSuggestAtMostTwo(t *testing.T) {
	var c []Candidate
	for _, n := range []string{"alpha", "bravo", "charlie"} {
		c = append(c, Candidate{SessionID: "s_" + n, Name: n, Online: true, Project: "svc-" + n, Folder: "shared-thing"})
	}
	got := Suggest("shared-thing returned 502 Bad Gateway", Self{SessionID: "s_me"}, c)
	if len(got) != protocol.MaxHints {
		t.Fatalf("got %d", len(got))
	}
}

func TestLooksLikeFailure(t *testing.T) {
	for _, s := range []string{e503, "dial tcp 10.0.0.1:443: connect: connection refused", "Error: ETIMEDOUT", "HTTP 502",
		"request failed with status 500", "curl: (7) Failed to connect", "upstream timed out"} {
		if !LooksLikeFailure(s) {
			t.Errorf("should look like a failure: %q", s)
		}
	}
	for _, s := range []string{"ok", "built 503 files", "all tests passed in 4.5s", "port 8080 listening"} {
		if LooksLikeFailure(s) {
			t.Errorf("should not look like a failure: %q", s)
		}
	}
}
