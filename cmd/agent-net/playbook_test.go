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
	req, err = playbookSetRequest([]string{"--pin", "terra", "keep", "this"})
	if err != nil || req.Pin == nil || !*req.Pin || req.Note != "keep this" {
		t.Errorf("pin: %+v %v", req, err)
	}
	req, err = playbookSetRequest([]string{"terra", "--unpin", "keep"})
	if err != nil || req.Pin == nil || *req.Pin {
		t.Errorf("unpin: %+v %v", req, err)
	}
}
