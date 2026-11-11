package gateway

import (
	"encoding/json"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
	"strings"
	"testing"
)

func TestStepContractSettingsLive(t *testing.T) {
	s, _ := newTestStore(t)
	for _, step := range []string{"accept", "review", "test", "approve", "merge", "release", "deploy", "verify"} {
		got := s.stepContract("alpha", step)
		if !strings.HasPrefix(got, "contract:\n  ") || len(got) < 40 {
			t.Fatalf("%s: %q", step, got)
		}
	}
	key := "delivery.contract.test"
	for _, change := range []SettingsChange{
		{Key: key, Level: "network", Scope: "network", Value: settingValue(`"Network tests"`)},
		{Key: key, Level: "repo", Scope: "alpha", Value: settingValue(`"Project tests"`)},
	} {
		if _, err := s.WriteSettings([]SettingsChange{change}, "cto", "contract update"); err != nil {
			t.Fatal(err)
		}
	}
	if got := s.stepContract("alpha", "test"); got != "contract:\n  Project tests" {
		t.Fatal(got)
	}
	if got := s.stepContract("beta", "test"); got != "contract:\n  Network tests" {
		t.Fatal(got)
	}
}

// Repo visibility is not stored, so a public project opts into a train with
// its repo setting. Without that setting the per-change text stays.
func TestStepContractCIMode(t *testing.T) {
	s, _ := newTestStore(t)
	private := s.stepContract("alpha", "test")
	if !strings.Contains(private, "Run the full changed packages and CI.") {
		t.Fatalf("private repo contract: %q", private)
	}
	if _, err := s.WriteSettings([]SettingsChange{{Key: "delivery.ci_mode", Level: "repo", Scope: "beta", Value: settingValue(`"train"`)}}, "cto", "public repo"); err != nil {
		t.Fatal(err)
	}
	public := s.stepContract("beta", "test")
	for _, want := range []string{
		"Test each change locally with the targeted packages",
		"Run CI once on the train tip.",
		"Cite that one run for every change in the train.",
		"bisect locally to find the culprit before you re-push",
	} {
		if !strings.Contains(public, want) {
			t.Errorf("public repo contract lacks %q: %q", want, public)
		}
	}
	if got := s.stepContract("alpha", "test"); got != private {
		t.Fatalf("per-change repo changed: %q", got)
	}
}

// Exercise the message writer, including its compact batch and reminder paths.
func TestStepTaskContracts(t *testing.T) {
	for _, batch := range []bool{false, true} {
		r := newDeliveryRig(t)
		if _, err := r.s.db.Exec(`UPDATE sessions SET online=0 WHERE id=?`, r.caller); err != nil {
			t.Fatal(err)
		}
		c := r.submit(r.a, r.workerSubmit("feature/contracts", commitA)).Change.Change
		if _, err := r.s.db.Exec(`DELETE FROM delivery_step_tasks WHERE change_id=?`, c.ID); err != nil {
			t.Fatal(err)
		}
		for _, step := range []string{"accept", "review", "test", "approve", "merge", "release", "deploy", "verify"} {
			if _, err := r.s.db.Exec(`INSERT INTO delivery_step_tasks(change_id,revision,step,for_step,after_merge,owner_node,created_at) VALUES(?,1,?,'',0,'/caller',0)`, c.ID, step); err != nil {
				t.Fatal(err)
			}
			target := nodeTaskTarget{id: c.ID, revision: 1, ref: protocol.StepRef{Step: step}, owner: "/caller", session: r.caller}
			check := func(want string) {
				t.Helper()
				m, err := r.s.storeStepTaskMessagesInternal([]nodeTaskTarget{target}, batch)
				if err != nil {
					t.Fatal(err)
				}
				if m.ID == "" || !strings.Contains(m.Body, want) {
					t.Fatalf("batch=%t step=%s: %q; want %q", batch, step, m.Body, want)
				}
			}
			snap, err := r.s.SettingsSnapshot()
			if err != nil {
				t.Fatal(err)
			}
			check("contract:\n  " + snap.String("delivery.contract."+step, settings.Where{Repo: "demo"}))
			// A CTO update must reach the next notification, including reminders.
			for _, level := range []string{"network", "repo"} {
				scope := "network"
				if level == "repo" {
					scope = "demo"
				}
				text := level + " " + step + " contract"
				value, _ := json.Marshal(text)
				if _, err := r.s.WriteSettings([]SettingsChange{{Key: "delivery.contract." + step, Level: level, Scope: scope, Value: value}}, "cto", "update contract"); err != nil {
					t.Fatal(err)
				}
				if _, err := r.s.db.Exec(`UPDATE messages SET state='expired' WHERE id IN (SELECT message_id FROM delivery_step_messages WHERE change_id=?)`, c.ID); err != nil {
					t.Fatal(err)
				}
				check("contract:\n  " + text)
			}
		}
	}
}
