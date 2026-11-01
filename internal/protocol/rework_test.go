package protocol

import "testing"

func TestReworkCounts(t *testing.T) {
	for _, tc := range []struct {
		handback, partial, idle bool
		cont, fix               int
	}{
		{false, false, false, 0, 0}, {false, true, false, 0, 0},
		{false, false, true, 1, 0}, {true, false, false, 0, 1},
		{true, true, false, 1, 0}, {true, false, true, 0, 1}, {true, true, true, 1, 0},
	} {
		cont, fix := ReworkCounts(tc.handback, tc.partial, tc.idle)
		if cont != tc.cont || fix != tc.fix {
			t.Errorf("%+v: got %d %d", tc, cont, fix)
		}
	}
}
