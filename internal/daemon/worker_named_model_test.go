package daemon

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

const namedModelKey = "workers.named_model_reminder"

// A caller that names a model gets the reminder; the picker's own choice,
// a harness alone and a continue do not.
func TestNamedModelReminder(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	w.register(t, newRepo(t, "shop"))

	named := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1",
		Task: "FAKE:edit FAKE:handback add work.txt", Isolation: protocol.IsolationWorktree})
	if !strings.HasPrefix(named.Reminder, "You named the model.") {
		t.Fatalf("named model: reminder = %q", named.Reminder)
	}

	w.workerStopped(t, g, protocol.StopHandback)
	cont, err := w.startDefault(protocol.WorkerStartRequest{Continue: named.SessionID, Task: "FAKE:ignore"})
	if err != nil {
		t.Fatal(err)
	}
	if cont.Reminder != "" {
		t.Fatalf("continue: reminder = %q", cont.Reminder)
	}
}

// The picker's own choice (an empty model, a harness alone, or "auto") gets
// no reminder.
func TestNamedModelReminderNotForPickerChoice(t *testing.T) {
	setupShadowJudge(t, difficultyScoresJSON, false)
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, withDifficulty(nil))
	w.register(t, newRepo(t, "shop"))
	score := 51.0
	w.cli.mustCall(protocol.OpScoresSet, protocol.ScoreSetRequest{Model: "fake/m1", Score: &score})
	w.cli.mustCall(protocol.OpWorkerProvidersSet, protocol.WorkerProviders{Providers: []string{"fake"}})
	w.cli.mustCall(protocol.OpWorkerProfileAdd, protocol.WorkerProfileRequest{Harness: "opencode", Model: "fake/m1"})
	for _, model := range []string{"", "auto"} {
		res, err := w.start(protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: model, Task: "x"})
		if err != nil || res.Model != "fake/m1" {
			t.Fatalf("model %q: %+v, %v", model, res, err)
		}
		if res.Reminder != "" {
			t.Fatalf("model %q: reminder = %q", model, res.Reminder)
		}
	}
}

// The text is the setting's, and a changed setting changes it.
func TestNamedModelReminderComesFromTheSetting(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	w.register(t, newRepo(t, "shop"))
	snap, err := w.d.cleanSettingsSnapshot([]byte(`{"revision":9,"recipe":"current","delivery_cutover":true,"values":[{"key":"` + namedModelKey + `","level":"network","scope":"network","value":"Name a model rarely."}],"maturity":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	w.d.settingsSnapshot.Store(&snap)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1", Task: "FAKE:ignore"})
	if res.Reminder != "Name a model rarely." {
		t.Fatalf("reminder = %q", res.Reminder)
	}
}
