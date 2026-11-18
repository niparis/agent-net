package protocol

import "testing"

// Only a Done line counts, and Docker is a CI matter, not a browser.
func TestBriefNeedsBrowser(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    WorkerStartRequest
		want bool
	}{
		{"screenshot in done", WorkerStartRequest{Done: []string{"The page renders; attach a screenshot at 1280x720."}}, true},
		{"playwright in done", WorkerStartRequest{Done: []string{"Playwright tests pass"}}, true},
		{"browser only in the problem", WorkerStartRequest{Problem: "The browser shows a blank page.", Done: []string{"go test ./... passes"}}, false},
		{"docker is not a browser", WorkerStartRequest{Done: []string{"The docker image builds"}}, false},
		{"no done", WorkerStartRequest{Task: "fix the browser tab"}, false},
	} {
		if _, got := BriefNeedsBrowser(tc.p); got != tc.want {
			t.Errorf("%s: BriefNeedsBrowser = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// The fact table: Claude cannot on macOS, and every other pair is taken to be
// able, so an unknown harness or OS is never left out of a pick.
func TestHarnessRunsBrowser(t *testing.T) {
	for _, tc := range []struct {
		harness, goos string
		want          bool
	}{
		{"claude", "darwin", false},
		{"claude", "linux", true},
		{"opencode", "darwin", true},
		{"codex", "darwin", true},
		{"pi", "darwin", true},
		{"agy", "darwin", true},
		{"claude", "", true},
	} {
		if got := HarnessRunsBrowser(tc.harness, tc.goos); got != tc.want {
			t.Errorf("HarnessRunsBrowser(%q, %q) = %v, want %v", tc.harness, tc.goos, got, tc.want)
		}
	}
}
