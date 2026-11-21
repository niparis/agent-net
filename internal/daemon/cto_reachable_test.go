package daemon

import (
	"context"
	"sort"
	"strings"
	"testing"
)

// Every read and talk name in the CTO class lists must reach a handler; a name
// with none falls through to "unknown CTO tool". The call sends empty
// arguments, which the handlers refuse for missing fields. Names left out: "message" is the stored as-owner call, run only from an
// approved proposal; the merged change tools are routed by action, and their
// concrete calls are covered by the class and proposal tests.
func TestCTOReadAndTalkNamesReachAHandler(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, WorkerSettings{}, nil)
	w.cli.mustCall("cto_send", map[string]string{"text": "start"})
	var names []string
	for name, class := range ctoToolClasses {
		if (class == ctoRead || class == ctoTalk) && name != "message" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		_, err := w.d.executeCTOTool(context.Background(), name, []byte(`{}`))
		if err != nil && strings.Contains(err.Error(), "unknown CTO tool") {
			t.Errorf("%s: %v", name, err)
		}
	}
}
