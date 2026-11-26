package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/coder/websocket"
	"github.com/niparis/agent-net/internal/protocol"
)

// a complete brief for the overlap tests: the path is the one a worker or a
// change can share.
func gatewayBrief(path string) string {
	return "Goal: The owner asked: \"fix the parser crash\" (message m_86b8d8cd77f796f0).\n" +
		"Files or areas: " + path + " and docs/reference.md.\n" +
		"Limits: tests use temp dirs and fakes only, never the real setup.\n" +
		"Definition of done: the new test fails first and passes after the fix.\n" +
		"How you are graded: the caller reruns the test and reads the diff.\n" +
		"Evidence:\n- the owner's words\n- the failing test\n- the diff\n"
}

func briefFrame(task string) protocol.WorkerStartFrame {
	return protocol.WorkerStartFrame{Owner: "s_caller", WorkerStartRequest: protocol.WorkerStartRequest{Project: "shop", Task: task, Harness: "opencode", Model: "fake/m1"}}
}

func TestWorkerBriefPassesACompleteBrief(t *testing.T) {
	s, _ := newTestStore(t)
	flags, err := s.checkWorkerBrief(briefFrame(gatewayBrief("internal/gateway/brief.go")), "shop", briefTestRepo())
	if err != nil {
		t.Fatal(err)
	}
	if len(flags) != 0 {
		t.Fatalf("complete brief failed: %v", flags)
	}
}

func TestWorkerBriefOverlapsRunningWorker(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("mac", "linux")
	worker, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "opencode", NativeID: "w1", Worker: &protocol.WorkerInfo{
		ID: "w_1", Owner: "s_owner", Project: "shop", Task: "Fix internal/gateway/brief.go and its tests."}})
	if err != nil {
		t.Fatal(err)
	}
	flags, err := s.checkWorkerBrief(briefFrame(gatewayBrief("internal/gateway/brief.go")), "shop", briefTestRepo())
	if err != nil {
		t.Fatal(err)
	}
	want := "overlaps running worker " + worker + " on internal/gateway/brief.go"
	if !strings.Contains(strings.Join(flagReasons(flags), "\n"), want) {
		t.Fatalf("flags %q do not say %q", flags, want)
	}

	// The same brief passes when it states the overlap and why.
	ok := gatewayBrief("internal/gateway/brief.go")
	ok = strings.Replace(ok, "Evidence:", "Overlap ok: the other worker is only reading the file.\nEvidence:", 1)
	flags, err = s.checkWorkerBrief(briefFrame(ok), "shop", briefTestRepo())
	if err != nil {
		t.Fatal(err)
	}
	if len(flags) != 0 {
		t.Fatalf("stated overlap failed: %v", flags)
	}
}

// insertOpenChange adds an open change on branch that touches path, as the
// delivery code stores it: the change row and its current diff stat.
func insertOpenChange(t *testing.T, s *Store, id, branch, path string) {
	t.Helper()
	insertOpenChangeIn(t, s, "shop", id, branch, path)
}

// insertOpenChangeIn is insertOpenChange for a named project.
func insertOpenChangeIn(t *testing.T, s *Store, project, id, branch, path string) {
	t.Helper()
	if _, err := s.db.Exec(`INSERT INTO delivery_changes
		(id,project,machine,branch,target_branch,base_commit,head_commit,uncommitted,revision,origin,caller,title,difficulty,state,merged_commit,created_at,updated_at)
		VALUES(?,?,'',?,'','',?,0,1,'{}','s_caller','parser','','open','',1,1)`,
		id, project, branch, strings.Repeat("a", 40)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO delivery_revisions(change_id,number,branch,commit_hash,uncommitted,at,diff_stat) VALUES(?,1,?,?,0,1,?)`,
		id, branch, strings.Repeat("a", 40), " "+path+" | 2 +-\n 1 file changed, 1 insertion(+), 1 deletion(-)"); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerBriefOverlapsOpenChange(t *testing.T) {
	s, _ := newTestStore(t)
	insertOpenChange(t, s, "ch_open", "agent-net/x", "internal/gateway/brief.go")
	flags, err := s.checkWorkerBrief(briefFrame(gatewayBrief("internal/gateway/brief.go")), "shop", briefTestRepo())
	if err != nil {
		t.Fatal(err)
	}
	want := "overlaps open change ch_open on internal/gateway/brief.go"
	if !strings.Contains(strings.Join(flagReasons(flags), "\n"), want) {
		t.Fatalf("flags %q do not say %q", flags, want)
	}
}

// An overlap is one advisory flag: the start goes ahead, and the caller sees
// the file in brief_flags.
func TestOverlappingBriefStartsWithAFlag(t *testing.T) {
	a := newAgyGate(t)
	client, received := connectTestNode(t, a.g, a.node)
	serveBriefRepo(t, client, received, briefTestRepo())
	insertOpenChangeIn(t, a.g.store, "alpha", "ch_open", "agent-net/x", "internal/gateway/brief.go")
	ans, err := a.g.workerStart(context.Background(), a.nc, protocol.WorkerStartFrame{Owner: a.owner, Level: "normal",
		WorkerStartRequest: protocol.WorkerStartRequest{Project: "alpha", Task: gatewayBrief("internal/gateway/brief.go"), Harness: "agy", Model: "gemini-3.8-flash-low"}})
	if err != nil {
		t.Fatalf("an overlapping brief was refused: %v", err)
	}
	if len(ans.BriefFlags) != 1 || ans.BriefFlags[0].Check != protocol.BriefCheckOverlap {
		t.Fatalf("brief_flags = %+v", ans.BriefFlags)
	}
	if !strings.Contains(ans.BriefFlags[0].Reason, "overlaps open change ch_open on internal/gateway/brief.go") {
		t.Fatalf("flag reason = %q", ans.BriefFlags[0].Reason)
	}
}

// A fix round starts from its own change's branch and names the same files, so
// the change it fixes is open. The change this start targets, by its branch,
// its id in the brief, or the worker it continues, is not an overlap.
func TestWorkerBriefFixRoundDoesNotOverlapItsOwnChange(t *testing.T) {
	const change, branch = "ch_8c5cf62b1a765790", "agent-net/w_299d8f5860a0fa4a"
	s, _ := newTestStore(t)
	insertOpenChange(t, s, change, branch, "internal/gateway/brief.go")

	// The start's base is the branch of the change it fixes.
	frame := briefFrame(gatewayBrief("internal/gateway/brief.go"))
	frame.Base = branch
	flags, err := s.checkWorkerBrief(frame, "shop", briefTestRepo())
	if err != nil {
		t.Fatal(err)
	}
	if len(flags) != 0 {
		t.Fatalf("fix round on its own change failed: %v", flags)
	}

	// A brief that names the change's id targets it too.
	frame = briefFrame(gatewayBrief("internal/gateway/brief.go") + "Change: " + change + "\n")
	flags, err = s.checkWorkerBrief(frame, "shop", briefTestRepo())
	if err != nil {
		t.Fatal(err)
	}
	if len(flags) != 0 {
		t.Fatalf("brief naming its own change failed: %v", flags)
	}

	// Continuing the worker that made the change targets it as well.
	node, _, _ := s.CreateNode("mac", "linux")
	worker, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "opencode", NativeID: "w1", Worker: &protocol.WorkerInfo{
		ID: "w_1", Owner: "s_owner", Project: "shop", Task: "Fix internal/gateway/brief.go and its tests."}})
	if err != nil {
		t.Fatal(err)
	}
	frame = briefFrame(gatewayBrief("internal/gateway/brief.go"))
	frame.Base = branch
	frame.Continue = worker
	flags, err = s.checkWorkerBrief(frame, "shop", briefTestRepo())
	if err != nil {
		t.Fatal(err)
	}
	if len(flags) != 0 {
		t.Fatalf("continued fix round failed: %v", flags)
	}
}

// The exemption is only for the change this start targets. A different open
// change on the same project is still an overlap.
func TestWorkerBriefOverlapsADifferentOpenChange(t *testing.T) {
	s, _ := newTestStore(t)
	insertOpenChange(t, s, "ch_8c5cf62b1a765790", "agent-net/w_299d8f5860a0fa4a", "internal/gateway/brief.go")
	insertOpenChange(t, s, "ch_0f1e2d3c4b5a6978", "agent-net/w_1111222233334444", "internal/tui/other.go")
	frame := briefFrame(gatewayBrief("internal/tui/other.go"))
	frame.Base = "agent-net/w_299d8f5860a0fa4a"
	flags, err := s.checkWorkerBrief(frame, "shop", briefTestRepo())
	if err != nil {
		t.Fatal(err)
	}
	want := "overlaps open change ch_0f1e2d3c4b5a6978 on internal/tui/other.go"
	if !strings.Contains(strings.Join(flagReasons(flags), "\n"), want) {
		t.Fatalf("flags %q do not say %q", flags, want)
	}
}

// runningWorkerWithTask starts a running worker in project shop whose brief is
// task, and returns its session id.
func runningWorkerWithTask(t *testing.T, s *Store, task string) string {
	t.Helper()
	node, _, _ := s.CreateNode("mac", "linux")
	worker, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "opencode", NativeID: "w1", Worker: &protocol.WorkerInfo{
		ID: "w_1", Owner: "s_owner", Project: "shop", Task: task}})
	if err != nil {
		t.Fatal(err)
	}
	return worker
}

// briefWithScope is a brief whose scope line is scope. It names no file of its
// own, so the only paths it can overlap on are the ones scope names.
func briefWithScope(scope string) string {
	return "Goal: The owner asked: \"fix the parser crash\" (message m_86b8d8cd77f796f0).\n" +
		"Scope: " + scope + "\n" +
		"Limits: tests use temp dirs and fakes only, never the real setup.\n" +
		"Definition of done: the new test fails first and passes after the fix.\n" +
		"Evidence:\n- the owner's words\n"
}

// A brief may name a file that a running worker holds only to tell its own
// worker to leave that file alone. That is not an overlap.
func TestWorkerBriefAskingToAvoidARunningWorkersFileStarts(t *testing.T) {
	for _, scope := range []string{
		"Do not touch internal/gateway/brief.go.",
		"Don't edit internal/gateway/brief.go.",
		"Never change internal/gateway/brief.go.",
		"Avoid editing internal/gateway/brief.go.",
		"internal/gateway/brief.go is out of scope.",
		"Do not touch internal/gateway/session_start.go, internal/gateway/brief.go or the hand-back paths, which other workers change now.",
	} {
		t.Run(scope, func(t *testing.T) {
			s, _ := newTestStore(t)
			runningWorkerWithTask(t, s, "Fix internal/gateway/brief.go and its tests.")
			flags, err := s.checkWorkerBrief(briefFrame(briefWithScope(scope)), "shop", briefTestRepo())
			if err != nil {
				t.Fatal(err)
			}
			if len(flags) != 0 {
				t.Fatalf("a brief that avoids the running worker's file failed: %v", flags)
			}
		})
	}
}

// A brief that asks for a change to a file a running worker holds is still
// refused, even when another sentence of the same brief asks to avoid a file.
func TestWorkerBriefAskingToChangeARunningWorkersFileIsAFlag(t *testing.T) {
	const want = "overlaps running worker"
	for _, scope := range []string{
		"Change internal/gateway/brief.go. Do not touch internal/tui/other.go.",
		"Do not touch internal/tui/other.go; change internal/gateway/brief.go.",
		"Do not touch internal/tui/other.go, but change internal/gateway/brief.go.",
		"Do not touch internal/tui/other.go and change internal/gateway/brief.go.",
		"Do not touch internal/tui/other.go, or change internal/gateway/brief.go.",
		"internal/tui/other.go is out of scope, change internal/gateway/brief.go.",
	} {
		t.Run(scope, func(t *testing.T) {
			s, _ := newTestStore(t)
			worker := runningWorkerWithTask(t, s, "Fix internal/gateway/brief.go and its tests.")
			flags, err := s.checkWorkerBrief(briefFrame(briefWithScope(scope)), "shop", briefTestRepo())
			if err != nil {
				t.Fatal(err)
			}
			if exp := want + " " + worker + " on internal/gateway/brief.go"; !strings.Contains(strings.Join(flagReasons(flags), "\n"), exp) {
				t.Fatalf("failures %q do not say %q", flags, exp)
			}
		})
	}
}

// The same rule holds the other way: a running worker told to leave a file
// alone does not hold it, so a new brief that changes that file does not
// overlap it. A file the running worker does change still overlaps.
func TestWorkerBriefChangingAFileARunningWorkerAvoidsStarts(t *testing.T) {
	s, _ := newTestStore(t)
	worker := runningWorkerWithTask(t, s, "Do not touch internal/gateway/brief.go. Fix internal/tui/other.go.")

	flags, err := s.checkWorkerBrief(briefFrame(briefWithScope("Change internal/gateway/brief.go.")), "shop", briefTestRepo())
	if err != nil || len(flags) != 0 {
		t.Fatalf("a file the running worker avoids overlapped: %v, %v", flags, err)
	}
	flags, err = s.checkWorkerBrief(briefFrame(briefWithScope("Change internal/tui/other.go.")), "shop", briefTestRepo())
	if want := "overlaps running worker " + worker + " on internal/tui/other.go"; err != nil || !strings.Contains(strings.Join(flagReasons(flags), "\n"), want) {
		t.Fatalf("failures %q, %v do not say %q", flags, err, want)
	}
}

// A running worker's brief can name a directory as the area it reads. Files
// that another change added to that directory since the worker started are
// not its work, so a new brief that names one of them starts. A file the
// brief really names still overlaps.
func TestWorkerBriefIgnoresFilesAddedAfterARunningWorkersBase(t *testing.T) {
	s, _ := newTestStore(t)
	repo := briefTestRepo()
	repo.Files = append(repo.Files, "internal/gateway/prices.go")
	worker := runningWorkerWithTask(t, s, "Fix internal/gateway/brief.go and tests in internal/gateway.")

	flags, err := s.checkWorkerBrief(briefFrame(gatewayBrief("internal/gateway/prices.go")), "shop", repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(flags) != 0 {
		t.Fatalf("a file added after the running worker's base was refused: %v", flags)
	}

	flags, err = s.checkWorkerBrief(briefFrame(gatewayBrief("internal/gateway/brief.go")), "shop", repo)
	if err != nil {
		t.Fatal(err)
	}
	want := "overlaps running worker " + worker + " on internal/gateway/brief.go"
	if !strings.Contains(strings.Join(flagReasons(flags), "\n"), want) {
		t.Fatalf("flags %q do not say %q", flags, want)
	}
}

// A brief with none of the six old format sections starts: decision 0145
// retired them, and no warning took their place.
func TestWorkerBriefWithoutTheSixSectionsStarts(t *testing.T) {
	a := newAgyGate(t)
	task := "Fix the crash when the parser sees an empty file.\n"
	_, err := a.g.workerStart(context.Background(), a.nc, protocol.WorkerStartFrame{Owner: a.owner, Level: "normal",
		WorkerStartRequest: protocol.WorkerStartRequest{Project: "alpha", Task: task, Harness: "agy", Model: "gemini-3.8-flash-low"}})
	if err != nil && strings.Contains(err.Error(), "brief refused") {
		t.Fatalf("a plain brief was refused: %v", err)
	}
	var rejections int
	if err := a.g.store.db.QueryRow(`SELECT COUNT(*) FROM worker_starts WHERE state='refused'`).Scan(&rejections); err != nil {
		t.Fatal(err)
	}
	if rejections != 0 {
		t.Fatalf("the start counted %d refusals", rejections)
	}
}

func TestWorkerBriefIgnoresPathsOutsideTheBase(t *testing.T) {
	s, _ := newTestStore(t)
	insertOpenChange(t, s, "ch_open", "feature", "tmp/agent-net-gocache")
	flags, err := s.checkWorkerBrief(briefFrame(gatewayBrief("internal/gateway/brief.go")+"Run GOCACHE=/tmp/agent-net-gocache go test.\n"), "shop", briefTestRepo())
	if err != nil || len(flags) != 0 {
		t.Fatalf("external cache path refused: %v, %v", flags, err)
	}
}

func TestWorkerBriefLeadingSlashDirectoryOverlaps(t *testing.T) {
	s, _ := newTestStore(t)
	insertOpenChange(t, s, "ch_open", "feature", "internal/toolcatalog/catalog.go")
	flags, err := s.checkWorkerBrief(briefFrame(gatewayBrief("/internal/toolcatalog")), "shop", briefTestRepo())
	if err != nil || !strings.Contains(strings.Join(flagReasons(flags), "\n"), "internal/toolcatalog/catalog.go") {
		t.Fatalf("repo directory overlap missed: %v, %v", flags, err)
	}
}

func TestWorkerBriefSharedDocumentationIsAFlag(t *testing.T) {
	for _, path := range []string{"docs/reference.md", "AGENTS.md", "README.md", "internal/README.md", "migrations.lock"} {
		t.Run(path, func(t *testing.T) {
			s, _ := newTestStore(t)
			insertOpenChange(t, s, "ch_open", "feature", path)
			flags, err := s.checkWorkerBrief(briefFrame(gatewayBrief("/"+path)), "shop", briefTestRepo())
			if err != nil || len(flags) != 1 || flags[0].Check != protocol.BriefCheckOverlap {
				t.Fatalf("shared documentation flag for %s: %v, %v", path, flags, err)
			}
			if !strings.Contains(strings.ToLower(flags[0].Reason), strings.ToLower(path)) {
				t.Fatalf("flag %+v does not name %s", flags[0], path)
			}
		})
	}
}

// scripts/test-changed.sh never counts as an overlap unless the brief says to change it.
func TestWorkerBriefNamingTestChangedScriptOnlyToRunItStarts(t *testing.T) {
	s, _ := newTestStore(t)
	repo := briefTestRepo()
	repo.Files = append(repo.Files, "scripts/test-changed.sh")
	insertOpenChange(t, s, "ch_script", "agent-net/w_script", "scripts/test-changed.sh")

	task := "Problem: Fix parser crash in internal/toolcatalog/catalog.go.\n" +
		"Done:\n- parser tests pass\n- run scripts/test-changed.sh\n"
	flags, err := s.checkWorkerBrief(briefFrame(task), "shop", repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(flags) != 0 {
		t.Fatalf("brief naming scripts/test-changed.sh to run it failed: %v", flags)
	}
}

// A brief citing a file only as evidence starts without overlap refusals.
func TestWorkerBriefCitingFileOnlyAsEvidenceStarts(t *testing.T) {
	s, _ := newTestStore(t)
	runningWorkerWithTask(t, s, "Fix internal/gateway/brief.go and its tests.")
	insertOpenChange(t, s, "ch_other", "agent-net/w_other", "internal/tui/other.go")

	task := "Problem: Fix parser crash in internal/toolcatalog/catalog.go.\n" +
		"Evidence:\n- crash was seen in internal/gateway/brief.go and internal/tui/other.go\n" +
		"Done:\n- tests pass in internal/toolcatalog/catalog.go\n"
	flags, err := s.checkWorkerBrief(briefFrame(task), "shop", briefTestRepo())
	if err != nil {
		t.Fatal(err)
	}
	if len(flags) != 0 {
		t.Fatalf("brief citing files only as evidence failed: %v", flags)
	}
}

// A brief that asks to change scripts/test-changed.sh overlaps a change or worker holding it.
func TestWorkerBriefChangingTestChangedScriptIsAFlag(t *testing.T) {
	s, _ := newTestStore(t)
	repo := briefTestRepo()
	repo.Files = append(repo.Files, "scripts/test-changed.sh")
	insertOpenChange(t, s, "ch_script", "agent-net/w_script", "scripts/test-changed.sh")

	task := "Problem: Fix issue in test runner.\n" +
		"Done:\n- change scripts/test-changed.sh to add timeout flag\n"
	flags, err := s.checkWorkerBrief(briefFrame(task), "shop", repo)
	if err != nil {
		t.Fatal(err)
	}
	want := "overlaps open change ch_script on scripts/test-changed.sh"
	if !strings.Contains(strings.Join(flagReasons(flags), "\n"), want) {
		t.Fatalf("flags %q do not say %q", flags, want)
	}
}

// A brief whose done list changes a held file is still refused.
func TestWorkerBriefWhoseDoneListChangesHeldFileIsAFlag(t *testing.T) {
	s, _ := newTestStore(t)
	worker := runningWorkerWithTask(t, s, "Fix internal/gateway/brief.go and its tests.")

	task := "Problem: Fix parser crash in internal/toolcatalog/catalog.go.\n" +
		"Evidence:\n- see stack trace in internal/tui/other.go\n" +
		"Done:\n- change internal/gateway/brief.go to fix crash\n"
	flags, err := s.checkWorkerBrief(briefFrame(task), "shop", briefTestRepo())
	if err != nil {
		t.Fatal(err)
	}
	want := "overlaps running worker " + worker + " on internal/gateway/brief.go"
	if !strings.Contains(strings.Join(flagReasons(flags), "\n"), want) {
		t.Fatalf("flags %q do not say %q", flags, want)
	}
}

// A brief where 'do not' or 'never' does not use an avoidance verb still counts
// named files as overlaps, while 'do not run', 'do not touch', and 'only runs' blank.
func TestWorkerBriefAvoidVerbRequired(t *testing.T) {
	s, _ := newTestStore(t)
	worker := runningWorkerWithTask(t, s, "Fix internal/gateway/brief.go and its tests.")

	for _, task := range []string{
		"Problem: Fix names do not swap in internal/gateway/brief.go.\nDone:\n- tests pass in internal/toolcatalog/catalog.go\n",
		"Problem: The session never wakes in internal/gateway/brief.go.\nDone:\n- tests pass in internal/toolcatalog/catalog.go\n",
	} {
		t.Run(task, func(t *testing.T) {
			flags, err := s.checkWorkerBrief(briefFrame(task), "shop", briefTestRepo())
			if err != nil {
				t.Fatal(err)
			}
			want := "overlaps running worker " + worker + " on internal/gateway/brief.go"
			if !strings.Contains(strings.Join(flagReasons(flags), "\n"), want) {
				t.Fatalf("flags %q do not say %q", flags, want)
			}
		})
	}

	for _, task := range []string{
		"Problem: Do not run the full package.\nScope: do not touch internal/gateway/brief.go.\nDone:\n- tests pass in internal/toolcatalog/catalog.go\n",
		"Problem: Only runs scripts/test-changed.sh.\nScope: do not touch internal/gateway/brief.go.\nDone:\n- tests pass in internal/toolcatalog/catalog.go\n",
	} {
		t.Run(task, func(t *testing.T) {
			flags, err := s.checkWorkerBrief(briefFrame(task), "shop", briefTestRepo())
			if err != nil {
				t.Fatal(err)
			}
			if len(flags) != 0 {
				t.Fatalf("unexpected flags: %v", flags)
			}
		})
	}
}

// flagReasons joins the reasons of the overlap flags for a contains check.
func flagReasons(flags []protocol.BriefFlag) []string {
	out := make([]string, len(flags))
	for i, f := range flags {
		out[i] = f.Reason
	}
	return out
}

func briefTestRepo() protocol.WorkerBriefRepo {
	return protocol.WorkerBriefRepo{Root: "/repo/shop", Files: []string{
		"internal/gateway/brief.go", "internal/tui/other.go", "internal/toolcatalog/catalog.go",
		"docs/reference.md", "AGENTS.md", "README.md", "internal/README.md", "migrations.lock",
	}}
}

// serveBriefRepo answers the node's base-tree read with repo, which the overlap
// check needs. Any other call fails at once, so a start that reaches the launch
// does not wait for a worker the test never runs.
func serveBriefRepo(t *testing.T, client *websocket.Conn, received <-chan []byte, repo protocol.WorkerBriefRepo) {
	t.Helper()
	ctx := t.Context()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case raw := <-received:
				var h protocol.Header
				if json.Unmarshal(raw, &h) != nil || h.ID == "" {
					continue
				}
				var res []byte
				var err error
				switch h.Type {
				case "worker.brief_repo":
					res, err = protocol.EncodeResult(h.ID, repo, nil)
				case "worker.launch":
					res, err = protocol.EncodeResult(h.ID, protocol.WorkerLaunched{SessionID: "s_brief_test"}, nil)
				default:
					res, err = protocol.EncodeResult(h.ID, nil, errors.New("test node runs no worker"))
				}
				if err == nil {
					_ = client.Write(ctx, websocket.MessageText, res)
				}
			}
		}
	}()
}
