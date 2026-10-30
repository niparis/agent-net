package main

import "testing"

func TestScoreArguments(t *testing.T) {
	for _, tt := range []struct {
		args  []string
		valid bool
	}{
		{nil, true}, {[]string{"set", "model", "32"}, true}, {[]string{"clear", "model"}, true}, {[]string{"set", "model", "101"}, false}, {[]string{"set", "model", "nan"}, false}, {[]string{"clear"}, false},
	} {
		_, _, err := scoreArguments(tt.args)
		if (err == nil) != tt.valid {
			t.Errorf("%v: %v", tt.args, err)
		}
	}
}

func TestWorkerProviderArguments(t *testing.T) {
	for _, tt := range []struct {
		args   []string
		method string
		want   int
	}{{nil, "worker_providers_get", -1}, {[]string{"openrouter", "codex"}, "worker_providers_set", 2}, {[]string{"--default"}, "worker_providers_set", 0}} {
		method, req := workerProviderArguments(tt.args)
		if method != tt.method || tt.want >= 0 && (req == nil || len(req.Providers) != tt.want) {
			t.Fatalf("%v: %s %+v", tt.args, method, req)
		}
	}
}
