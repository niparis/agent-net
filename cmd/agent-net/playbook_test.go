package main

import "testing"

func TestPlaybookSetArguments(t *testing.T) {
	req, err := playbookSetRequest([]string{"deepseek-v4.1-flash", "--harness", "opencode", "Ask", "for", "a", "diff"})
	if err != nil {
		t.Fatal(err)
	}
	if req.Model != "deepseek-v4.1-flash" || req.Harness != "opencode" || req.Note != "Ask for a diff" {
		t.Errorf("request: %+v", req)
	}
	req, err = playbookSetRequest([]string{"--harness", "pi", "terra"})
	if err != nil || req.Model != "terra" || req.Harness != "pi" || req.Note != "" {
		t.Errorf("flags first: %+v %v", req, err)
	}
	if _, err := playbookSetRequest([]string{"--harness", "pi"}); err == nil {
		t.Error("a set with no model was accepted")
	}
}
