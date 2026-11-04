package delivery

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// TestMain keeps every test away from the real git setup: HOME and the XDG
// config folder are temporary, git reads no global or system config, and no
// inherited GIT_* variable points git at another repository.
func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

func runTests(m *testing.M) int {
	home, err := os.MkdirTemp("", "delivery-test-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(home)
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); strings.HasPrefix(k, "GIT_") {
			os.Unsetenv(k)
		}
	}
	for k, v := range map[string]string{
		"HOME":                home,
		"XDG_CONFIG_HOME":     home,
		"GIT_CONFIG_GLOBAL":   "/dev/null",
		"GIT_CONFIG_NOSYSTEM": "1",
	} {
		os.Setenv(k, v)
	}
	return m.Run()
}
