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

func TestSuggestIgnoresCommonFolderNames(t *testing.T) {
	peers := []Candidate{
		{SessionID: "s_cleanup", Name: "cleanup", Hostname: "laptop-a", Online: true, Project: "maintenance", Folder: "cleanup"},
		{SessionID: "s_cog", Name: "cogforge", Hostname: "laptop-a", Online: true, Project: "cogforge", Folder: "cogforge"},
	}
	self := Self{SessionID: "s_me", Project: "client-app", Hostname: "laptop-b"}

	got := Suggest("git diff -- cleanup/config.go cogforge/config.go", self, peers)
	if len(got) != 1 || got[0].Candidate.SessionID != "s_cog" {
		t.Fatalf("want only cogforge; got %+v", got)
	}

	got = Suggest("git diff -- cleanup/config.go", self, peers[:1])
	if len(got) != 0 {
		t.Fatalf("a common folder name must not match: %+v", got)
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

func TestSuggestIgnoresLoginAndHomeNameInPaths(t *testing.T) {
	t.Setenv("USER", "nicolas")
	t.Setenv("HOME", "/home/nicolas")
	c := []Candidate{{SessionID: "s_peer", Name: "peer", Hostname: "Nicolas-MBP", Online: true,
		Project: "other", Folder: "other"}}
	self := Self{SessionID: "s_me", Project: "client-app", Hostname: "laptop-b"}
	got := Suggest("cannot read /home/nicolas/x: permission denied", self, c)
	if len(got) != 0 {
		t.Errorf("a path under home matched the host name part: %+v", got)
	}
	got = Suggest("ssh: connect to host nicolas-mbp port 22: Connection refused", self, c)
	if len(got) != 1 {
		t.Errorf("the full host name should still match: %+v", got)
	}
	c = []Candidate{{SessionID: "s_peer2", Name: "peer2", Hostname: "nicolas", Online: true,
		Project: "other2", Folder: "other2"}}
	got = Suggest("cannot reach nicolas: connection refused", self, c)
	if len(got) != 0 {
		t.Errorf("a single label equal to the login matched: %+v", got)
	}
}

func TestSuggestNeverMatchesLoginOrHomeName(t *testing.T) {
	// The daemon can run with no HOME, so masking the home path in the text is
	// best effort. The login name and the home folder name must never be a
	// name that suggests a session, whatever the text looks like.
	t.Setenv("USER", "nicolas")
	t.Setenv("HOME", "")
	self := Self{SessionID: "s_me", Project: "client-app", Hostname: "laptop-b"}
	peers := []Candidate{
		// A peer that runs in the home folder itself.
		{SessionID: "s_home", Name: "nicolas", Online: true, Project: "research", Folder: "nicolas"},
		// A project named after the login.
		{SessionID: "s_proj", Name: "other", Online: true, Project: "nicolas", Folder: "code"},
		// A repository named after the login.
		{SessionID: "s_repo", Name: "other2", Online: true, Project: "x", Folder: "code",
			Remote: "git@example.com:acme/nicolas.git"},
	}
	got := Suggest("cannot read /home/nicolas/code/x: permission denied", self, peers)
	if len(got) != 0 {
		t.Fatalf("a path under the home folder matched a login or home name: %+v", got)
	}
	for _, peer := range peers {
		if got := Suggest("cannot read /home/nicolas/code/x: permission denied", self, []Candidate{peer}); len(got) != 0 {
			t.Errorf("%s: matched %+v", peer.SessionID, got)
		}
	}

	// A folder under the home folder still matches by its own name.
	cog := []Candidate{{SessionID: "s_cog", Name: "cogforge", Online: true, Project: "cogforge",
		Folder: "cogforge", Remote: "git@example.com:acme/cogforge.git"}}
	got = Suggest("cannot read /home/nicolas/code/cogforge/config: permission denied", self, cog)
	if len(got) != 1 || got[0].Candidate.SessionID != "s_cog" {
		t.Fatalf("got %+v", got)
	}
}

func TestSuggestNeverMatchesHomeFolderName(t *testing.T) {
	// The home folder name is skipped even when it is not the login name.
	t.Setenv("USER", "alice")
	t.Setenv("HOME", "/home/nicolas")
	peer := Candidate{SessionID: "s_home", Name: "nicolas", Online: true, Project: "research", Folder: "nicolas"}
	got := Suggest("cannot reach nicolas: connection refused", Self{SessionID: "s_me", Project: "client-app"}, []Candidate{peer})
	if len(got) != 0 {
		t.Fatalf("the home folder name matched a folder: %+v", got)
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
