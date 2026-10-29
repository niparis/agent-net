package setup

import "testing"

func TestServiceResult(t *testing.T) {
	for _, c := range []struct {
		changed, existed bool
		want             string
	}{
		{false, true, "ok (restarted)"},
		{true, true, "updated (restarted)"},
		{true, false, "ok (installed, started)"},
	} {
		if got := serviceResult(c.changed, c.existed); got != c.want {
			t.Errorf("%v %v: %q", c.changed, c.existed, got)
		}
	}
}
