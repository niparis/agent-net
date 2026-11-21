package testsock

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// run reports whether the body after Skip ran.
func run(t *testing.T) (reached bool) {
	t.Helper()
	t.Run("probe", func(t *testing.T) {
		Skip(t)
		reached = true
	})
	return reached
}

func TestSkipOnlyWithEnv(t *testing.T) {
	t.Setenv(LogEnv, "") // an outer script run must not count this test's own skip
	t.Setenv(Env, "")
	if !run(t) {
		t.Fatal("Skip skipped without the env var")
	}
	t.Setenv(Env, "1")
	if run(t) {
		t.Fatal("Skip did not skip with the env var")
	}
}

func TestSkipLogsTheTestName(t *testing.T) {
	log := filepath.Join(t.TempDir(), "skipped")
	t.Setenv(Env, "1")
	t.Setenv(LogEnv, log)
	run(t)
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(b)); got != "TestSkipLogsTheTestName/probe" {
		t.Fatalf("log = %q, want the skipped test name", got)
	}
}
