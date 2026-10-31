package daemon

import (
	"strings"
	"testing"
)

func TestInsideSessionServiceRoots(t *testing.T) {
	fakeProcessTree(t, map[int]int{300: 200, 200: 100, 400: 1}, map[int][]string{
		100: {"/Applications/Codex.app/Contents/Resources/codex", "app-server"},
	})
	d := &Daemon{}
	d.inSession = d.insideSession
	lc := &localConn{d: d, client: "cli", peerPID: 300}
	for _, tc := range []struct {
		name    string
		args    []string
		tracked bool
	}{
		{"codex command", []string{"/Applications/Codex.app/Contents/Resources/codex", "app-server"}, false},
		{"codex control peer", []string{"/some/other-server"}, true},
		{"opencode service", []string{"/usr/bin/opencode", "serve", "--service"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			processArgs = func(pid int) []string {
				if pid == 100 {
					return tc.args
				}
				return nil
			}
			d.adapters = nil
			if tc.tracked {
				d.adapters = map[string]Adapter{"codex": &codexAdapter{rpc: &codexRPC{pid: 100}}}
			}
			for _, method := range []string{"owner_send", "worker_start"} {
				err := lc.operatorGate(method)
				if err == nil || !strings.HasPrefix(err.Error(), `method "`+method+`" is not available from inside an agent session`) {
					t.Errorf("%s: got %v", method, err)
				}
			}
		})
	}
	lc.peerPID = 400
	for _, method := range []string{"owner_send", "worker_start"} {
		if err := lc.operatorGate(method); err != nil {
			t.Errorf("unrelated shell %s: %v", method, err)
		}
	}
}

func TestInsideSessionHostCommandExact(t *testing.T) {
	for _, args := range [][]string{{"/bin/not-codex", "app-server"}, {"codex", "run"}, {"opencode-helper", "serve"}, {"opencode", "run"}, {"codex"}} {
		if sessionHostCommand(args) {
			t.Errorf("unexpected host: %q", args)
		}
	}
}
