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

// ctoOperatorCall builds the settings_change area operator call the CTO tool
// makes for one owner operation, with the operation's arguments inside it.
func ctoOperatorCall(t *testing.T, method, args string) json.RawMessage {
	t.Helper()
	call, err := json.Marshal(map[string]json.RawMessage{
		"area":      json.RawMessage(`"operator"`),
		"method":    json.RawMessage(fmt.Sprintf("%q", method)),
		"arguments": json.RawMessage(args),
	})
	if err != nil {
		t.Fatal(err)
	}
	return call
}

// A clear through hidden_set is an empty object from the TUI, because the
// fields carry omitempty. The CTO is held to the named list instead, so a
// dropped or mistyped field can never clear both lists.
func TestCTOHiddenSetRefusesEmptyAndUnknownArguments(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi"})
	ctx := context.Background()
	if err := n.d.registerCTOSession(ctx); err != nil {
		t.Fatal(err)
	}
	cli := cliClient(t, n)
	_, sid := helloPi(t, n, "hidden-test", "/work/test", true)

	read := func(t *testing.T, cli *localClient) string {
		t.Helper()
		var got protocol.HiddenItems
		if err := json.Unmarshal(cli.mustCall(protocol.OpHiddenGet, nil), &got); err != nil {
			t.Fatal(err)
		}
		return jsonString(t, got)
	}
	cli.mustCall(protocol.OpHiddenSet, protocol.HiddenItems{Sessions: []string{sid}, Nodes: []string{"n1"}})
	before := read(t, cli)

	call := func(args string) error {
		t.Helper()
		_, err := n.d.runCTOToolRaw(ctx, "settings_change", ctoOperatorCall(t, "hidden_set", args))
		return err
	}

	if err := call(`{"bogus":1}`); err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("unknown field: %v; stored %s, was %s", err, read(t, cli), before)
	}
	if got := read(t, cli); got != before {
		t.Fatalf("unknown field changed the value: %s -> %s", before, got)
	}

	if err := call(`{}`); err == nil || !strings.Contains(err.Error(), "hidden_set") {
		t.Fatalf("empty object: %v; stored %s, was %s", err, read(t, cli), before)
	}
	if got := read(t, cli); got != before {
		t.Fatalf("empty object changed the value: %s -> %s", before, got)
	}

	// Naming a list with [] clears it. The value still replaces the whole
	// preference, so a list to keep is named too.
	if err := call(`{"sessions":[],"nodes":["n1"]}`); err != nil {
		t.Fatalf("named clear: %v", err)
	}
	want := jsonString(t, protocol.HiddenItems{Nodes: []string{"n1"}})
	if got := read(t, cli); got != want {
		t.Fatalf("named clear: want %s, got %s", want, got)
	}

	// The TUI's own clear still sends {}.
	cli.mustCall(protocol.OpHiddenSet, protocol.HiddenItems{})
	if got := read(t, cli); got != jsonString(t, protocol.HiddenItems{}) {
		t.Fatalf("TUI clear: %s", got)
	}
}

// Known difficulty levels are the keys of protocol.DifficultyMinimums, the
// default minimum capability scores read by DifficultyFit and by the gateway's
// PUT /v1/difficulty. The CTO must name a known level, and an empty object is
// no longer a way to clear every override.
func TestCTODifficultySetRefusesEmptyAndUnknownLevels(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	ctx := context.Background()
	if err := n.d.registerCTOSession(ctx); err != nil {
		t.Fatal(err)
	}
	cli := cliClient(t, n)

	read := func(t *testing.T, cli *localClient) string {
		t.Helper()
		var got map[string]float64
		if err := json.Unmarshal(cli.mustCall("difficulty_get", nil), &got); err != nil {
			t.Fatal(err)
		}
		return jsonString(t, got)
	}
	cli.mustCall("difficulty_set", map[string]float64{"hard": 45})
	before := read(t, cli)

	call := func(args string) error {
		t.Helper()
		_, err := n.d.runCTOToolRaw(ctx, "settings_change", ctoOperatorCall(t, "difficulty_set", args))
		return err
	}

	if err := call(`{"bogus":1}`); err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("unknown level: %v; stored %s, was %s", err, read(t, cli), before)
	}
	if got := read(t, cli); got != before {
		t.Fatalf("unknown level changed the value: %s -> %s", before, got)
	}

	if err := call(`{}`); err == nil || !strings.Contains(err.Error(), "difficulty_set") {
		t.Fatalf("empty object: %v; stored %s, was %s", err, read(t, cli), before)
	}
	if got := read(t, cli); got != before {
		t.Fatalf("empty object changed the value: %s -> %s", before, got)
	}

	if err := call(`{"hard":60}`); err != nil {
		t.Fatalf("known level: %v", err)
	}
	after := read(t, cli)
	if after == before {
		t.Fatalf("known level did not change the value: %s", after)
	}

	items, err := inboxAndLog(g.gw.Store())
	if err != nil {
		t.Fatal(err)
	}
	var undo *protocol.InboxItem
	for i := range items {
		if len(items[i].Options) == 2 && items[i].Options[0] == "difficulty_set" {
			undo = &items[i]
		}
	}
	if undo == nil {
		t.Fatalf("no difficulty_set undo in the inbox: %+v", items)
	}
	cli.mustCall(undo.Options[0], json.RawMessage(undo.Options[1]))
	if got := read(t, cli); got != before {
		t.Fatalf("undo: %s -> %s", before, got)
	}
}

// Every merged CTO tool carries reason next to its arguments, and the tools
// that allow owner_words carry those too. Neither field may reach the concrete
// operation as an unknown argument. The gated actions show the proposal path;
// the rest show the immediate one.
func TestCTOMergedToolsTakeReasonAndOwnerWords(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi"})
	ctx := context.Background()
	if err := n.d.registerCTOSession(ctx); err != nil {
		t.Fatal(err)
	}
	cli := cliClient(t, n)
	words := "go ahead with the change"
	ctoOwnerSays(t, n.d, "please "+words)

	_, sid := helloPi(t, n, "rename-test", "/work/test", true)
	cli.mustCall("node_add", map[string]string{"name": "first"})
	cli.mustCall("node_add", map[string]string{"name": "second"})
	cli.mustCall("node_add", map[string]string{"name": "remove-me"})
	if _, err := g.gw.Store().AddProjects(n.cfg.NodeID, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: "display-project", Path: "/work/display-project"}}}); err != nil {
		t.Fatal(err)
	}

	// owner_words is allowed only on the tools in ctoWordsTools.
	withWords := func(args map[string]any) map[string]any {
		args["owner_words"] = words
		return args
	}

	immediate := []struct {
		tool  string
		args  map[string]any
		check func(t *testing.T, result any, err error)
	}{
		{"settings_change", withWords(map[string]any{"area": "notifications", "disabled": true}), func(t *testing.T, _ any, err error) {
			if err != nil {
				t.Fatal(err)
			}
		}},
		{"session_change", withWords(map[string]any{"action": "rename", "session_id": sid, "name": "renamed"}), func(t *testing.T, _ any, err error) {
			if err != nil {
				t.Fatal(err)
			}
		}},
		{"project_change", withWords(map[string]any{"action": "display", "project": "display-project", "name": "First"}), func(t *testing.T, _ any, err error) {
			if err != nil {
				t.Fatal(err)
			}
		}},
		{"graph_change", withWords(map[string]any{"action": "edge_set", "from": "first", "to": "second", "kind": "depends", "rule": "auto"}), func(t *testing.T, _ any, err error) {
			if err != nil {
				t.Fatal(err)
			}
		}},
		// worker_change does not take owner_words. Stopping a session that is
		// not a worker still reaches the handler, so the error proves reason
		// was stripped rather than refused as an unknown field.
		{"worker_change", map[string]any{"action": "stop", "session": "s_not_a_worker"}, func(t *testing.T, _ any, err error) {
			if err == nil || !strings.Contains(err.Error(), "not a worker") {
				t.Fatalf("worker_change: %v", err)
			}
		}},
		// An owner question is no stored item since decision 0196. An unknown
		// item still reaches the gateway's answer, so its error proves the same.
		{"answer", map[string]any{"kind": "inbox", "id": "i_not_stored", "answer": "yes"}, func(t *testing.T, _ any, err error) {
			if err == nil || !strings.Contains(err.Error(), "inbox item not found") {
				t.Fatalf("answer: %v", err)
			}
		}},
	}
	for _, tc := range immediate {
		t.Run(tc.tool, func(t *testing.T) {
			tc.args["reason"] = "the owner asked for it"
			raw, err := json.Marshal(tc.args)
			if err != nil {
				t.Fatal(err)
			}
			result, err := n.d.runCTOToolRaw(ctx, tc.tool, raw)
			tc.check(t, result, err)
		})
	}

	// The gated actions wait for the owner, and reason rides along without
	// becoming an unknown argument of the stored call.
	other := startNode(t, g, nodeOpts{})
	proposals := []struct {
		tool string
		args map[string]any
	}{
		{"graph_change", map[string]any{"action": "node_remove", "name": "remove-me", "reason": "the owner asked for it"}},
		{"settings_change", map[string]any{"area": "machine_remove", "node_id": other.cfg.NodeID, "reason": "the owner asked for it"}},
	}
	for _, tc := range proposals {
		t.Run(tc.tool+" proposal", func(t *testing.T) {
			raw, err := json.Marshal(tc.args)
			if err != nil {
				t.Fatal(err)
			}
			result, err := n.d.runCTOToolRaw(ctx, tc.tool, raw)
			if err != nil {
				t.Fatal(err)
			}
			if text, _ := result.(string); !strings.HasPrefix(text, "proposal ") {
				t.Fatalf("want a proposal, got %v", result)
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
