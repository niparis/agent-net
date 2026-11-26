package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// refusalGateway keeps the notices a daemon without a link sends.
type refusalGateway struct {
	mu    sync.Mutex
	sends []protocol.MsgSend
	fail  bool
}

func (g *refusalGateway) call(_ context.Context, typ string, p any) (json.RawMessage, bool, error) {
	if m, ok := p.(protocol.MsgSend); ok && typ == "msg.send" {
		if g.fail {
			return nil, true, errors.New("the gateway is down")
		}
		g.mu.Lock()
		g.sends = append(g.sends, m)
		g.mu.Unlock()
	}
	return json.RawMessage(`{}`), true, nil
}

func (g *refusalGateway) all() []protocol.MsgSend {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]protocol.MsgSend(nil), g.sends...)
}

// refusalDaemon is a daemon with a state folder and a fake gateway, without a
// socket or a link, and one running worker of harness.
func refusalDaemon(t *testing.T, harness string) (*Daemon, *worker, *refusalGateway) {
	t.Helper()
	st, err := openState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.close() })
	g := &refusalGateway{}
	d := &Daemon{cfg: Config{StateDir: t.TempDir(), testGatewayCall: g.call}, st: st, log: log.New(io.Discard, "", 0)}
	w := &worker{id: "w_test", harness: harness, sessionID: "s_worker", owner: "s_owner", taskMsg: "m_task", startedAt: time.Now()}
	w.dir = filepath.Join(d.cfg.StateDir, "workers", w.id)
	if err := os.MkdirAll(w.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	d.workers = map[string]*worker{w.id: w}
	return d, w, g
}

// A refused agy or Codex hand-back cannot be returned to the model, so the
// caller gets the worker's report and the reason in one notice.
func TestRefusedHandbackTellsTheCallerWithTheReport(t *testing.T) {
	t.Parallel()
	d, w, g := refusalDaemon(t, "codex")
	d.codexRefused(w, protocol.WorkerHandbackRequest{Report: "the codex report", Status: protocol.StatusDone}, "the codex report", errors.New("unknown recipient s_gone"))

	sends := g.all()
	if len(sends) != 1 {
		t.Fatalf("%d notices, want one: %+v", len(sends), sends)
	}
	body := sends[0].Body
	for _, want := range []string{"status: hand-back refused", "unknown recipient s_gone", "the codex report"} {
		if !strings.Contains(body, want) {
			t.Errorf("notice lacks %q:\n%s", want, body)
		}
	}
	if !w.idleNoticeSent {
		t.Error("the refused turn is not marked handled, so an idle notice would follow")
	}
}

// When the notice cannot go out, the idle period keeps the report so the
// caller still gets it with the idle notice.
func TestRefusedNoticeFailureLeavesTheReportForTheIdleNotice(t *testing.T) {
	t.Parallel()
	d, w, g := refusalDaemon(t, "codex")
	g.fail = true
	w.sessionID = ""
	d.codexRefused(w, protocol.WorkerHandbackRequest{Report: "the codex report", Status: protocol.StatusDone}, "the codex report", errors.New("unknown recipient s_gone"))

	if len(g.all()) != 0 {
		t.Fatal("a notice reached a gateway that is down")
	}
	if w.idleNoticeSent {
		t.Error("the turn is marked handled although the caller was not told")
	}
	if w.idleNoticeReport != "the codex report" {
		t.Errorf("idle report %q, want the worker's report", w.idleNoticeReport)
	}
}

// tellCallerRefused is the notice the agy path sends after it drops the
// pending hand-back.
func TestTellCallerRefusedNamesTheWorkerState(t *testing.T) {
	t.Parallel()
	d, w, g := refusalDaemon(t, "agy")
	if !d.tellCallerRefused(context.Background(), w, protocol.WorkerHandbackRequest{Report: "the agy report", Status: protocol.StatusBlocked}, "the file is gone") {
		t.Fatal("the notice did not go out")
	}
	body := g.all()[0].Body
	for _, want := range []string{"status: hand-back refused", "status blocked", "the file is gone", "the worker is still running", "the agy report"} {
		if !strings.Contains(body, want) {
			t.Errorf("notice lacks %q:\n%s", want, body)
		}
	}
}
