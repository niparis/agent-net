package daemon

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestOpenCodeInventoryProbeIsolation(t *testing.T) {
	tempRoot := filepath.Clean(os.TempDir())
	withinTemp := func(path string) bool {
		if path == "" {
			return false
		}
		rel, err := filepath.Rel(tempRoot, filepath.Clean(path))
		return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
	for _, name := range []string{"XDG_STATE_HOME", "XDG_DATA_HOME", "HOME"} {
		got := os.Getenv(name)
		if !withinTemp(got) {
			t.Errorf("%s=%q is outside temp dir %q", name, got, tempRoot)
		}
	}
	if path, err := ownerOpenCodeServicePath(""); err != nil || !withinTemp(path) {
		t.Fatalf("default probe path %q (%v) is outside temp dir %q", path, err, tempRoot)
	}

	outsideHome := t.TempDir()
	allowedHome := t.TempDir()
	trap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("inventory probe contacted service outside daemon temp HomeDir: %s", r.URL.Path)
		fmt.Fprint(w, `{"paths":{}}`)
	}))
	defer trap.Close()
	writeOwnerOpenCodeService(t, filepath.Join(outsideHome, ".local", "state"), trap.URL)

	t.Setenv("XDG_STATE_HOME", "")
	if path, err := ownerOpenCodeServicePath(""); err != nil || !withinTemp(path) {
		t.Fatalf("HOME-fallback probe path %q (%v) is outside temp dir %q", path, err, tempRoot)
	}
	t.Setenv("HOME", outsideHome)
	list := []protocol.HarnessInfo{{Name: "opencode", Version: "2.2.6"}}
	(&Daemon{cfg: Config{HomeDir: allowedHome}}).checkOpenCodeCapabilities(context.Background(), list)
	if path, err := ownerOpenCodeServicePath(allowedHome); err != nil || !strings.HasPrefix(path, allowedHome+string(filepath.Separator)) {
		t.Fatalf("configured HomeDir probe path %q (%v) is outside %q", path, err, allowedHome)
	}
}

func TestOpenCodeInventoryMissingOperations(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"paths":{}}`) }))
	defer server.Close()
	writeOwnerOpenCodeService(t, state, server.URL)
	list := []protocol.HarnessInfo{{Name: "opencode", Version: "2.2.6"}}
	(&Daemon{}).checkOpenCodeCapabilities(context.Background(), list)
	if len(list[0].MissingOperations) == 0 {
		t.Fatal("missing operations not reported")
	}
}

func TestOpenCodeInventoryCachesService(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; fmt.Fprint(w, `{"paths":{}}`) }))
	defer server.Close()
	writeOwnerOpenCodeService(t, state, server.URL)
	d := &Daemon{}
	for i := 0; i < 2; i++ {
		list := []protocol.HarnessInfo{{Name: "opencode", Version: "2.2.6"}}
		d.checkOpenCodeCapabilities(context.Background(), list)
	}
	if calls != 1 {
		t.Fatalf("discovery called %d times", calls)
	}
}
