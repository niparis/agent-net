package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestPhraseWorkerProtectedAndReadOnly(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startCodexNodeWithModels(t, g, shortSet, []string{"gpt-fake"})
	repo := newRepo(t, "shop")
	w.register(t, repo)
	score := 90.0
	w.cli.mustCall(protocol.OpScoresSet, protocol.ScoreSetRequest{Model: "gpt-fake", Score: &score})
	w.cli.mustCall(protocol.OpWorkerProfileAdd, protocol.WorkerProfileRequest{Harness: "codex", Model: "gpt-fake"})
	w.cli.mustCall(protocol.OpProtectedProvidersSet, protocol.ProtectedProviders{Providers: []string{"openai"}, Reason: "reserved"})
	frame := protocol.WorkerStartFrame{Owner: w.ownerID(), RequireReadOnly: true, WorkerStartRequest: protocol.WorkerStartRequest{Project: "shop", Difficulty: "trivial", Isolation: protocol.IsolationNone, Task: "phrase these words"}}
	if _, err := w.d.call(context.Background(), "worker.start", frame); err == nil {
		t.Fatal("protected provider was used")
	}
	if w.d.workerCount() != 0 {
		t.Fatal("protected worker started")
	}
	w.cli.mustCall(protocol.OpProtectedProvidersSet, protocol.ProtectedProviders{Providers: []string{}, Reason: "test"})
	raw, err := w.d.call(context.Background(), "worker.start", frame)
	if err != nil {
		t.Fatal(err)
	}
	var out protocol.WorkerStartAnswer
	if err = json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.Model != "gpt-fake" || out.Permissions != protocol.WorkersReadOnly || out.SessionID == "" {
		t.Fatalf("selection: %+v", out)
	}
}

func TestPhraseWorkerThroughGateway(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startCodexNodeWithModels(t, g, shortSet, []string{"gpt-fake"})
	repo := newRepo(t, "shop")
	w.register(t, repo)
	score := 90.0
	w.cli.mustCall(protocol.OpScoresSet, protocol.ScoreSetRequest{Model: "gpt-fake", Score: &score})
	w.cli.mustCall(protocol.OpWorkerProfileAdd, protocol.WorkerProfileRequest{Harness: "codex", Model: "gpt-fake"})
	_, sender := helloPi(t, w.node, "phrase-sender", repo, true)
	s := g.gw.Store()
	node, err := s.SessionNode(sender)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.AddArchitect("ops", "owner"); err != nil {
		t.Fatal(err)
	}
	dest, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "phrase-target", Repo: protocol.RepoInfo{Project: "ops"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RememberOwnerPrompt(sender, "ops: backups are his job"); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.StoreMessage(protocol.MsgSend{Sender: sender, Recipient: dest, Body: "please"}); err != nil {
		t.Fatal(err)
	}
	// The test uses the gateway's test hook instead of waiting two minutes.
	db, err := sql.Open("sqlite", filepath.Join(g.dir, "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE learned_suggestions SET ready_at=0`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err = s.ProcessLearnedContext(ctx); err != nil {
		t.Fatal(err)
	}
	h, err := s.CharterHistory("ops", 10)
	if err != nil || len(h) != 1 || h[0].NewValue != "daily backup operations" {
		t.Fatalf("history=%+v err=%v", h, err)
	}
	rec := readCodex(t, repo, func(r fakeCodexRecord) bool { return len(r.TurnParams) > 0 })
	if rec.Thread["sandbox"] != "read-only" {
		t.Fatalf("sandbox: %+v", rec.Thread)
	}
}

func TestPhraseWorkerRespectsNoNetworkPolicy(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startCodexNodeWithModels(t, g, shortSet, []string{"gpt-fake"})
	repo := newRepo(t, "shop")
	w.register(t, repo)
	setWorkersLevel(t, w, repo, protocol.WorkersNoNetwork)
	score := 90.0
	w.cli.mustCall(protocol.OpScoresSet, protocol.ScoreSetRequest{Model: "gpt-fake", Score: &score})
	w.cli.mustCall(protocol.OpWorkerProfileAdd, protocol.WorkerProfileRequest{Harness: "codex", Model: "gpt-fake"})
	frame := protocol.WorkerStartFrame{Owner: w.ownerID(), RequireReadOnly: true, WorkerStartRequest: protocol.WorkerStartRequest{Project: "shop", Difficulty: "trivial", Isolation: protocol.IsolationNone, Task: "phrase these words"}}
	if _, err := w.d.call(context.Background(), "worker.start", frame); err == nil {
		t.Fatal("background worker widened a no-network policy")
	}
	if w.d.workerCount() != 0 {
		t.Fatal("worker started")
	}
}
