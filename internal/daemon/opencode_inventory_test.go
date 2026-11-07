package daemon

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestOpenCodeInventoryMissingOperations(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"paths":{}}`) }))
	defer server.Close()
	writeOwnerOpenCodeService(t, state, server.URL)
	list := []protocol.HarnessInfo{{Name: "opencode", Version: "2.2.6"}}
	checkOpenCodeCapabilities(context.Background(), list)
	if len(list[0].MissingOperations) == 0 {
		t.Fatal("missing operations not reported")
	}
}
