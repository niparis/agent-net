package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// ctoSetter is one CTO write action that stores a whole value. route turns
// the value's JSON into the CTO call a user or the CTO would make.
type ctoSetter struct {
	name     string
	undo     string
	route    func(t *testing.T, raw string) (string, json.RawMessage)
	baseline func(t *testing.T, cli *localClient)
	read     func(t *testing.T, cli *localClient) string
	right    string
	cleared  string
	wrong    string
}

func ctoOpRoute(method string) func(*testing.T, string) (string, json.RawMessage) {
	return func(t *testing.T, raw string) (string, json.RawMessage) {
		t.Helper()
		return "cto_op", json.RawMessage(fmt.Sprintf(`{"method":%q,"args":%s}`, method, raw))
	}
}

// settingsAreaRoute builds the settings_change call with its routing area and
// the value's fields side by side, as the CTO tool does.
func settingsAreaRoute(area string) func(*testing.T, string) (string, json.RawMessage) {
	return func(t *testing.T, raw string) (string, json.RawMessage) {
		t.Helper()
		var fields map[string]json.RawMessage
		if err := json.Unmarshal([]byte(raw), &fields); err != nil {
			t.Fatal(err)
		}
		value, _ := json.Marshal(area)
		fields["area"] = value
		b, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		return "settings_change", b
	}
}

func TestCTOWriteActionsRefuseUnknownArguments(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	ctx := context.Background()
	if err := n.d.registerCTOSession(ctx); err != nil {
		t.Fatal(err)
	}
	cli := cliClient(t, n)

	readFloors := func(t *testing.T, cli *localClient) string {
		t.Helper()
		var got protocol.WorkerFloors
		if err := json.Unmarshal(cli.mustCall(protocol.OpWorkerFloorsGet, nil), &got); err != nil {
			t.Fatal(err)
		}
		return jsonString(t, got.Floors)
	}
	readProtected := func(t *testing.T, cli *localClient) string {
		t.Helper()
		var got protocol.ProtectedProviders
		if err := json.Unmarshal(cli.mustCall(protocol.OpProtectedProvidersGet, nil), &got); err != nil {
			t.Fatal(err)
		}
		return jsonString(t, got.Providers)
	}
	readProviders := func(t *testing.T, cli *localClient) string {
		t.Helper()
		var got protocol.WorkerProviders
		if err := json.Unmarshal(cli.mustCall(protocol.OpWorkerProvidersGet, nil), &got); err != nil {
			t.Fatal(err)
		}
		return jsonString(t, got.Providers)
	}
	readScore := func(t *testing.T, cli *localClient) string {
		t.Helper()
		var scores []protocol.ModelScore
		if err := json.Unmarshal(cli.mustCall(protocol.OpScoresGet, nil), &scores); err != nil {
			t.Fatal(err)
		}
		for _, score := range scores {
			if score.Match == "fixture" {
				return fmt.Sprint(score.Score)
			}
		}
		return "default"
	}
	readTimezone := func(t *testing.T, cli *localClient) string {
		t.Helper()
		var got protocol.TimeZoneSettings
		if err := json.Unmarshal(cli.mustCall(protocol.OpTimeZoneGet, nil), &got); err != nil {
			t.Fatal(err)
		}
		return got.Name
	}
	readPlaybook := func(t *testing.T, cli *localClient) string {
		t.Helper()
		var got protocol.PlaybookGetResult
		if err := json.Unmarshal(cli.mustCall("playbook_get", protocol.PlaybookGetRequest{Model: "fixture", Harness: "pi"}), &got); err != nil {
			t.Fatal(err)
		}
		if got.Note == nil {
			return "none"
		}
		return got.Note.Note
	}

	setters := []ctoSetter{
		{
			name: "worker_floors_set", undo: protocol.OpWorkerFloorsSet,
			route: ctoOpRoute(protocol.OpWorkerFloorsSet),
			baseline: func(t *testing.T, cli *localClient) {
				cli.mustCall(protocol.OpWorkerFloorsSet, protocol.WorkerFloors{Floors: map[string]int{"anthropic": 20}})
			},
			read:    readFloors,
			right:   `{"floors":{"openai":35}}`,
			cleared: `{"floors":{}}`,
			wrong:   `{"floors":"not a map"}`,
		},
		{
			name: "protected_providers_set", undo: protocol.OpProtectedProvidersSet,
			route: settingsAreaRoute("protected_providers"),
			baseline: func(t *testing.T, cli *localClient) {
				cli.mustCall(protocol.OpProtectedProvidersSet, protocol.ProtectedProviders{Providers: []string{"anthropic"}})
			},
			read:    readProtected,
			right:   `{"protected_providers":["openai"]}`,
			cleared: `{"protected_providers":[]}`,
			wrong:   `{"protected_providers":"not a list"}`,
		},
		{
			name: "worker_providers_set", undo: protocol.OpWorkerProvidersSet,
			route: settingsAreaRoute("worker_providers"),
			baseline: func(t *testing.T, cli *localClient) {
				cli.mustCall(protocol.OpWorkerProvidersSet, protocol.WorkerProviders{Providers: []string{"anthropic"}})
			},
			read:    readProviders,
			right:   `{"providers":["openai"]}`,
			cleared: `{"providers":[]}`,
			wrong:   `{"providers":"not a list"}`,
		},
		{
			name: "score_set", undo: protocol.OpScoresSet,
			route: settingsAreaRoute("score"),
			baseline: func(t *testing.T, cli *localClient) {
				score := 40.0
				cli.mustCall(protocol.OpScoresSet, protocol.ScoreSetRequest{Model: "fixture", Score: &score})
			},
			read:    readScore,
			right:   `{"model":"fixture","score":80}`,
			cleared: `{"model":"fixture"}`,
			wrong:   `{"model":"fixture","score":"high"}`,
		},
		{
			name: "timezone_set", undo: protocol.OpTimeZoneSet,
			route: ctoOpRoute(protocol.OpTimeZoneSet),
			baseline: func(t *testing.T, cli *localClient) {
				cli.mustCall(protocol.OpTimeZoneSet, protocol.TimeZoneSettings{Name: "Europe/Paris"})
			},
			read:    readTimezone,
			right:   `{"name":"America/New_York"}`,
			cleared: `{"name":""}`,
			wrong:   `{"name":12}`,
		},
		{
			name: "playbook_set", undo: protocol.OpPlaybookSet,
			route: settingsAreaRoute("playbook"),
			baseline: func(t *testing.T, cli *localClient) {
				cli.mustCall(protocol.OpPlaybookSet, protocol.PlaybookSetRequest{Model: "fixture", Harness: "pi", Note: "keep me"})
			},
			read:    readPlaybook,
			right:   `{"model":"fixture","harness":"pi","note":"new note"}`,
			cleared: `{"model":"fixture","harness":"pi","note":""}`,
			wrong:   `{"model":"fixture","harness":"pi","note":12}`,
		},
	}

	for _, setter := range setters {
		t.Run(setter.name, func(t *testing.T) {
			t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
			setter.baseline(t, cli)
			before := setter.read(t, cli)

			call := func(t *testing.T, raw string) error {
				t.Helper()
				tool, args := setter.route(t, raw)
				_, err := n.d.runCTOToolRaw(ctx, tool, args)
				return err
			}

			// A field the setter does not have is refused by name. It must
			// never be dropped and leave the stored value at its zero value.
			if err := call(t, `{"bogus":1}`); err == nil || !strings.Contains(err.Error(), "bogus") {
				t.Fatalf("unknown field: %v; stored %s, was %s", err, setter.read(t, cli), before)
			}
			if got := setter.read(t, cli); got != before {
				t.Fatalf("unknown field changed the value: %s -> %s", before, got)
			}

			// A setter that replaces a whole value needs that value named.
			if err := call(t, `{}`); err == nil || !strings.Contains(err.Error(), setter.name) {
				t.Fatalf("missing value: %v; stored %s, was %s", err, setter.read(t, cli), before)
			}
			if got := setter.read(t, cli); got != before {
				t.Fatalf("missing value changed the value: %s -> %s", before, got)
			}

			if err := call(t, setter.wrong); err == nil {
				t.Fatal("wrong type was accepted")
			}
			if got := setter.read(t, cli); got != before {
				t.Fatalf("wrong type changed the value: %s -> %s", before, got)
			}

			if err := call(t, setter.right); err != nil {
				t.Fatalf("right call: %v", err)
			}
			after := setter.read(t, cli)
			if after == before {
				t.Fatalf("right call did not change the value: %s", after)
			}

			// The undo the CTO notice offers is the call the TUI builds, and
			// it still works.
			items, err := inboxAndLog(g.gw.Store())
			if err != nil {
				t.Fatal(err)
			}
			var undo *protocol.InboxItem
			for i := range items {
				if len(items[i].Options) == 2 && items[i].Options[0] == setter.undo {
					undo = &items[i]
				}
			}
			if undo == nil {
				t.Fatalf("no %s undo in the inbox: %+v", setter.undo, items)
			}
			cli.mustCall(undo.Options[0], json.RawMessage(undo.Options[1]))
			if got := setter.read(t, cli); got != before {
				t.Fatalf("undo: %s -> %s", before, got)
			}

			// An explicit empty value still clears.
			if err := call(t, setter.cleared); err != nil {
				t.Fatalf("explicit clear: %v", err)
			}
			if got := setter.read(t, cli); got == before || got == after {
				t.Fatalf("explicit clear did not clear: %s", got)
			}
		})
	}
}

func jsonString(t *testing.T, value any) string {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
