package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// handoverTestCTO starts a gateway, a node with the fake OpenCode, and a
// registered CTO session. The CTO starts on OpenCode, as a new install does.
func handoverTestCTO(t *testing.T) (*testGateway, *node, *localClient) {
	t.Helper()
	t.Setenv("FAKE_OC_AUTH_RECORD", "1")
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{detect: fakeInventory, interval: time.Hour, cfg: func(c *Config) { c.HarnessPaths = map[string]string{"opencode": os.Args[0]} }})
	if err := n.d.registerCTOSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	return g, n, cliClient(t, n)
}

// handoverNotes reads the CTO's notes named handover-*, by name.
func handoverNotes(t *testing.T, d *Daemon) map[string]string {
	t.Helper()
	raw, err := d.ctoNotes(context.Background(), "agent_notes_find", nil)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, note := range raw.(*protocol.NotesFindResult).Notes {
		if strings.HasPrefix(note.Name, "handover-") {
			out[note.Name] = note.Body
		}
	}
	return out
}

func switchCTOHarness(t *testing.T, cli *localClient, harness string) {
	t.Helper()
	cli.mustCall("cto_settings", protocol.CTOSettingsRequest{Harness: &harness})
}

// When the old harness is down, the handover is the mechanical layer alone. It
// carries the owner's words and says that no model answered.
func TestCTOHandoverMechanicalAloneWhenOldHarnessIsDown(t *testing.T) {
	withFakeOpenCodeRouteSets(t, testCTOHandoverMechanicalAloneWhenOldHarnessIsDown)
}

func testCTOHandoverMechanicalAloneWhenOldHarnessIsDown(t *testing.T) {
	_, n, cli := handoverTestCTO(t)
	// Nothing has started OpenCode, so there is no conversation to ask.
	if err := n.d.recordCTOOwnerMessage("check the migration plan first"); err != nil {
		t.Fatal(err)
	}
	switchCTOHarness(t, cli, "claude")

	notes := handoverNotes(t, n.d)
	if len(notes) != 1 {
		t.Fatalf("want one handover note, got %d", len(notes))
	}
	name := n.d.latestHandoverNote()
	body, ok := notes[name]
	if !ok {
		t.Fatalf("the latest handover %q is not a note: %v", name, notes)
	}
	for _, want := range []string{
		"Handover from opencode to claude",
		"Workers the CTO started that are not done (0):",
		"Changes the CTO started that are not done (0):",
		"Harness-local work (opencode): no OpenCode server running",
		"check the migration plan first",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("handover lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "Model output") {
		t.Fatalf("a down harness gave a model layer:\n%s", body)
	}
}

// When the old conversation answers, the same note also holds its words, marked
// as model output. The new conversation is pointed at the note.
func TestCTOHandoverBothLayersWhenOldHarnessAnswers(t *testing.T) {
	withFakeOpenCodeRouteSets(t, testCTOHandoverBothLayersWhenOldHarnessAnswers)
}

func testCTOHandoverBothLayersWhenOldHarnessAnswers(t *testing.T) {
	_, n, cli := handoverTestCTO(t)
	model := "fake/m1"
	cli.mustCall("cto_settings", protocol.CTOSettingsRequest{Model: &model})
	cli.mustCall("cto_send", map[string]string{"text": "keep the release notes short"})
	switchCTOHarness(t, cli, "claude")
	// The old conversation answers after the switch has returned.
	waitHandoverSettled(t, n.d)
	if n.d.ctoHarnessRunning("opencode") {
		t.Fatal("the old OpenCode is still running after its note was written")
	}

	name := n.d.latestHandoverNote()
	body := handoverNotes(t, n.d)[name]
	if !strings.Contains(body, "keep the release notes short") {
		t.Fatalf("the owner's message is missing:\n%s", body)
	}
	if !strings.Contains(body, "## Model output (not the owner's words)\n\n"+fakeOCHandoverAnswer) {
		t.Fatalf("the model layer is missing or not marked:\n%s", body)
	}
	if strings.Contains(body, "Harness-local work (opencode): busy turn") {
		t.Fatalf("an idle OpenCode turn was reported busy:\n%s", body)
	}
	if strings.Contains(n.logs.String(), "was running when switched") {
		t.Fatalf("an owner switch on an idle turn logged a busy notice:\n%s", n.logs.String())
	}
	start := n.d.ctoStartBlock(context.Background())
	if !strings.Contains(start, "Latest handover note: "+name) {
		t.Fatalf("the new conversation is not pointed at the handover:\n%s", start)
	}
}

// Each switch writes its own note, so a second switch does not replace the
// first one.
func TestCTOHandoverTwoSwitchesKeepTwoNotes(t *testing.T) {
	withFakeOpenCodeRouteSets(t, testCTOHandoverTwoSwitchesKeepTwoNotes)
}

func testCTOHandoverTwoSwitchesKeepTwoNotes(t *testing.T) {
	_, n, cli := handoverTestCTO(t)
	cli.mustCall("cto_send", map[string]string{"text": "first thing"})
	switchCTOHarness(t, cli, "claude")
	first := n.d.latestHandoverNote()
	switchCTOHarness(t, cli, "opencode")
	second := n.d.latestHandoverNote()
	if first == "" || second == "" || first == second {
		t.Fatalf("the two switches gave the names %q and %q", first, second)
	}
	notes := handoverNotes(t, n.d)
	if len(notes) != 2 {
		t.Fatalf("want two handover notes, got %d: %v", len(notes), notes)
	}
	if !strings.Contains(notes[first], "Handover from opencode to claude") || !strings.Contains(notes[second], "Handover from claude to opencode") {
		t.Fatalf("the notes do not name their switches:\n%s\n---\n%s", notes[first], notes[second])
	}
}

// An owner switch during a busy OpenCode turn sends the owner one notice before
// the turn stops. The busy turn skips the model layer, because it cannot answer.
func TestCTOHandoverOwnerNoticeBeforeStoppingBusyTurn(t *testing.T) {
	withFakeOpenCodeRouteSets(t, testCTOHandoverOwnerNoticeBeforeStoppingBusyTurn)
}

func testCTOHandoverOwnerNoticeBeforeStoppingBusyTurn(t *testing.T) {
	_, n, cli := handoverTestCTO(t)
	cli.mustCall("cto_send", map[string]string{"text": "a long task"})
	n.d.cto.mu.Lock()
	session := n.d.cto.session
	n.d.cto.mu.Unlock()
	// The fake OpenCode reports the session as running while this file names it.
	if err := os.WriteFile(filepath.Join(n.cfg.StateDir, "cto", "data", "fake-active"), []byte(session), 0o600); err != nil {
		t.Fatal(err)
	}
	switchCTOHarness(t, cli, "claude")

	if !strings.Contains(n.logs.String(), "was running when switched to claude") {
		t.Fatalf("want log about the running turn:\n%s", n.logs.String())
	}
	body := handoverNotes(t, n.d)[n.d.latestHandoverNote()]
	if !strings.Contains(body, "Harness-local work (opencode): busy turn in OpenCode") {
		t.Fatalf("the busy turn is not in the handover:\n%s", body)
	}
	if strings.Contains(body, "Model output") {
		t.Fatalf("a busy turn gave a model layer:\n%s", body)
	}
}

// A running harness that does not answer the handover in time does not hold
// the switch: the handover is the mechanical layer alone, and the switch goes on.
func TestCTOHandoverFallsBackWhenOldHarnessDoesNotAnswer(t *testing.T) {
	sessions, _ := useFakeClaude(t)
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{detect: fakeInventory, interval: time.Hour, cfg: func(c *Config) {
		c.ClaudeSessionsDir = sessions
		c.Adapters = append(c.Adapters, &fsClaudeAdapter{dir: sessions})
		c.HarnessPaths = map[string]string{"claude": os.Args[0], "opencode": os.Args[0]}
		c.testHandoverWait = time.Second
	}})
	if err := n.d.registerCTOSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	cli := cliClient(t, n)
	switchCTOHarness(t, cli, "claude")
	cli.mustCall("cto_send", map[string]string{"text": "claude one"})
	switchCTOHarness(t, cli, "opencode")

	body := handoverNotes(t, n.d)[n.d.latestHandoverNote()]
	if !strings.Contains(body, "Handover from claude to opencode") {
		t.Fatalf("the switch wrote no handover:\n%s", body)
	}
	if strings.Contains(body, "Model output") {
		t.Fatalf("a Claude job that never answered gave a model layer:\n%s", body)
	}
}

// The model-layer request reaches a Claude CTO as agent-net's own labelled
// prompt, never as a peer message that claims to be the owner speaking.
func TestCTOHandoverRequestIsAgentNetNotTheOwner(t *testing.T) {
	t.Setenv(envFakeClaudeTranscribe, "1")
	n, cli := handoverClaudeNode(t, time.Second)
	switchCTOHarness(t, cli, "opencode")
	waitHandoverSettled(t, n.d)
	id, err := n.d.ctoConversation("claude")
	if err != nil || id == "" {
		t.Fatalf("Claude conversation: %q %v", id, err)
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(n.cfg.ClaudeSessionsDir), "projects", "*", id+".jsonl"))
	if err != nil || len(matches) == 0 {
		t.Fatalf("Claude transcript: %v %v", matches, err)
	}
	raw, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.Contains(line, ctoHandoverPrompt) {
			continue
		}
		found = true
		if !strings.Contains(line, "[agent-net: "+ctoHandoverLabel+"]") || !strings.Contains(line, `from-name=\"agent-net\"`) {
			t.Fatalf("the request is not labelled as agent-net's prompt: %s", line)
		}
		if strings.Contains(line, "This is the owner speaking") {
			t.Fatalf("the request claims to be the owner: %s", line)
		}
	}
	if !found {
		t.Fatal("the handover request is not in the Claude transcript")
	}
}

// The owner's Claude timeline does not show the handover request either. The
// transcript keeps agent-net's labelled prompt inside its frame, and the timeline
// reads it back to the labelled prompt, so the exchange is dropped there too.
func TestCTOHandoverRequestIsHiddenFromTheClaudeTimeline(t *testing.T) {
	t.Setenv(envFakeClaudeTranscribe, "1")
	n, cli := handoverClaudeNode(t, time.Second)
	switchCTOHarness(t, cli, "opencode")
	waitHandoverSettled(t, n.d)
	switchCTOHarness(t, cli, "claude")
	waitHandoverSettled(t, n.d)
	var events protocol.CTOEvents
	if err := json.Unmarshal(cli.mustCall(protocol.OpCTOEvents, map[string]int{"index": 0}), &events); err != nil {
		t.Fatal(err)
	}
	sawOwner := false
	for _, raw := range events.Messages {
		text := string(raw)
		if strings.Contains(text, "claude one") {
			sawOwner = true
		}
		if strings.Contains(text, ctoHandoverPrompt) {
			t.Fatalf("the owner's Claude timeline shows the handover request: %s", text)
		}
	}
	if !sawOwner {
		t.Fatalf("the owner's Claude timeline lacks the owner's message: %v", events.Messages)
	}
}

// The owner's timeline does not show the handover request or its reply, in
// either message form. Other messages stay, in order.
func TestHandoverExchangeIsHiddenFromTheOwnerTimeline(t *testing.T) {
	msg := func(s string) json.RawMessage { return json.RawMessage(s) }
	in := []json.RawMessage{
		msg(`{"type":"user","text":"keep the release notes short"}`),
		msg(`{"type":"user","text":` + strconv.Quote(ctoHandoverPrompt) + `}`),
		msg(`{"type":"assistant","content":[{"type":"text","text":"Promise: finish the migration plan."}]}`),
		msg(`{"type":"user","text":` + strconv.Quote(ctoSystemPromptText(ctoHandoverLabel, ctoHandoverPrompt)) + `}`),
		msg(`{"type":"assistant","content":[{"type":"text","text":"The labelled promise."}]}`),
		msg(`{"type":"idle","outcome":"succeeded"}`),
		msg(`{"type":"user","text":"what next?"}`),
		msg(`{"type":"assistant","text":"Ship it."}`),
	}
	out := dropHandoverExchange(in)
	if len(out) != 4 {
		t.Fatalf("kept %d messages, want 4: %s", len(out), out)
	}
	for i, want := range []string{"keep the release notes short", "idle", "what next?", "Ship it."} {
		if !strings.Contains(string(out[i]), want) {
			t.Fatalf("message %d is %s, want one with %q", i, out[i], want)
		}
	}
}

// Only the newest ten owner messages are ever read, so the record keeps no more
// than that. The handover reads the newest ten, oldest first.
func TestCTOOwnerMessagesKeepTheNewestTen(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	st, err := openState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	d := &Daemon{st: st}
	for i := 1; i <= 12; i++ {
		if err := d.recordCTOOwnerMessage(fmt.Sprintf("message %d", i)); err != nil {
			t.Fatal(err)
		}
	}
	var rows int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM cto_owner_messages`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != ctoHandoverOwnerMessages {
		t.Fatalf("the record holds %d messages, want %d", rows, ctoHandoverOwnerMessages)
	}
	got, err := d.ctoOwnerMessages(ctoHandoverOwnerMessages)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 10 || got[0].text != "message 3" || got[9].text != "message 12" {
		t.Fatalf("newest ten, oldest first: %+v", got)
	}
}
