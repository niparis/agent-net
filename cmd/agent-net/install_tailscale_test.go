package main

import "testing"

func TestCheckTailscaleFlags(t *testing.T) {
	for _, tc := range []struct {
		gateway, machine bool
		name, key        string
		ok               bool
	}{
		{false, false, "", "", true},
		{true, false, "agent-net", "", true},
		{true, false, "agent-net", "/run/secrets/ts-key", true},
		{true, true, "", "", true},
		{false, false, "agent-net", "", false},
		{false, true, "", "", false},
		{true, true, "agent-net", "", false},
		{true, false, "", "/run/secrets/ts-key", false},
	} {
		if err := checkTailscaleFlags(tc.gateway, tc.name, tc.key, tc.machine); (err == nil) != tc.ok {
			t.Errorf("%+v: %v", tc, err)
		}
	}
}
