package main

import "testing"

func TestWorkerProfileArgs(t *testing.T) {
	for _, tt := range []struct {
		args   []string
		method string
		valid  bool
	}{
		{nil, "worker_profiles", true}, {[]string{"add", "codex", "terra", "high"}, "worker_profile_add", true}, {[]string{"remove", "name"}, "worker_profile_remove", true}, {[]string{"add", "codex"}, "", false},
	} {
		method, _, err := workerProfileArgs(tt.args)
		if (err == nil) != tt.valid || method != tt.method {
			t.Errorf("%v: %s %v", tt.args, method, err)
		}
	}
}
