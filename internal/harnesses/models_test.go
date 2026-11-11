package harnesses

import (
	"context"
	"strings"
	"testing"
)

// A codex app server can say why it will not answer on stderr and then exit.
// codexModels must report that text. The stderr copy goroutine of exec writes
// while codexModels reads the buffer before Wait, so this also guards the
// buffer against a data race (run it with -race).
func TestCodexModelsReportsStderrOfAServerThatExits(t *testing.T) {
	root := t.TempDir()
	codex := script(t, root, "codex", `i=0
while [ "$i" -lt 500 ]; do
  echo "codex: stderr line $i" >&2
  i=$((i+1))
done
exit 1`)
	_, _, err := codexModels(context.Background(), codex, "")
	if err == nil {
		t.Fatal("want an error from a server that never answered")
	}
	if !strings.Contains(err.Error(), "stderr line") {
		t.Fatalf("err = %v, want the server's stderr in it", err)
	}
}
