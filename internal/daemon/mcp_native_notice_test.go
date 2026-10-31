package daemon

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// noticesFor returns the "moved to agent-net" notices a fake Claude adapter got.
func noticesFor(fa *fakeAdapter, native string) []protocol.Message {
	var out []protocol.Message
	for _, m := range fa.delivered() {
		if strings.Contains(m.Body, "moved to agent-net") && strings.Contains(m.Body, native) {
			out = append(out, m)
		}
	}
	return out
}

// After a native entry is removed, running sessions of that harness in its
// scope are told once to restart. A user-level entry reaches every session of
// the harness on that machine; a project-level entry reaches the sessions of
// that project only. Sessions of other harnesses are left alone.
func TestMCPNativeRemoveTellsRunningSessions(t *testing.T) {
	fa := &fakeAdapter{harness: "claude"}
	f := newImportFixtureWith(t, map[string]string{"probe": stdioNative()}, "", fa)
	other := t.TempDir()
	fa.setSessions(
		protocol.SessionInfo{Harness: "claude", NativeID: "c-alpha", Name: "a", Status: "idle", Cwd: f.repo},
		protocol.SessionInfo{Harness: "claude", NativeID: "c-other", Name: "o", Status: "idle", Cwd: other})
	eventually(t, "claude sessions known", func() bool {
		return fa.host.SessionID("claude", "c-alpha") != "" && fa.host.SessionID("claude", "c-other") != ""
	})
	pi, _ := helloPi(t, f.n, "np", f.repo, true)
	_ = pi

	// A user-level entry: both Claude sessions are told, the Pi session is not.
	cand := f.importProbe(t)
	plan := f.nativePlan(t, cand)
	res := f.nativeCall(t, protocol.MCPNativeRemoveRequest{PlanID: plan.ID})
	if res.State != protocol.MCPNativeRemoved || len(res.Notified) != 2 {
		t.Fatalf("apply: %+v", res)
	}
	joined := strings.Join(res.Notes, " ")
	if !strings.Contains(joined, "2 running session") {
		t.Fatalf("notes do not say who was told: %v", res.Notes)
	}
	eventually(t, "both sessions got the notice", func() bool {
		return len(noticesFor(fa, "probe")) == 2
	})
	for _, m := range noticesFor(fa, "probe") {
		if !strings.Contains(m.Body, "restart this session") || m.FromHarness != protocol.OwnerHarness {
			t.Fatalf("notice: %+v", m)
		}
	}
	// Undo and remove again with a new plan: the same session is not told twice
	// for the same plan, and the count in the result stays honest.
	f.nativeCall(t, protocol.MCPNativeRemoveRequest{UndoPlanID: plan.ID})
	if n := len(noticesFor(fa, "probe")); n != 2 {
		t.Fatalf("undo sent notices: %d", n)
	}
}

func TestMCPNativeRemoveProjectEntryTellsProjectSessionsOnly(t *testing.T) {
	fa := &fakeAdapter{harness: "claude"}
	f := newImportFixtureWith(t, map[string]string{"probe": stdioNative()}, "", fa)
	other := t.TempDir()
	fa.setSessions(
		protocol.SessionInfo{Harness: "claude", NativeID: "c-alpha", Name: "a", Status: "idle", Cwd: f.repo},
		protocol.SessionInfo{Harness: "claude", NativeID: "c-other", Name: "o", Status: "idle", Cwd: filepath.Join(other)})
	eventually(t, "claude sessions known", func() bool {
		return fa.host.SessionID("claude", "c-alpha") != "" && fa.host.SessionID("claude", "c-other") != ""
	})
	cand := f.candidate(t, "claude", "projsrv")
	if res := f.apply(t, f.plan(t, cand).ID); res.State != protocol.MCPImportOK {
		t.Fatalf("import: %+v", res)
	}
	plan := f.nativePlan(t, f.candidate(t, "claude", "projsrv"))
	res := f.nativeCall(t, protocol.MCPNativeRemoveRequest{PlanID: plan.ID})
	if res.State != protocol.MCPNativeRemoved || len(res.Notified) != 1 || res.Notified[0] != fa.host.SessionID("claude", "c-alpha") {
		t.Fatalf("apply: %+v", res)
	}
	eventually(t, "the project session got the notice", func() bool { return len(noticesFor(fa, "projsrv")) == 1 })
	if n := len(noticesFor(fa, "projsrv")); n != 1 {
		t.Fatalf("notices: %d", n)
	}
}
