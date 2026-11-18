package protocol

import (
	"fmt"
	"regexp"
)

// lintBrowserRE finds a Done line that needs a browser or a screenshot.
var lintBrowserRE = regexp.MustCompile(`(?i)\b(?:` + lintBrowserWords + `)\b`)

// BriefNeedsBrowser returns the first Done line of a start that needs a
// browser, and whether there is one. Only Done counts: a browser named in the
// Problem is context, not a check the worker must run.
func BriefNeedsBrowser(p WorkerStartRequest) (string, bool) {
	for _, line := range cleanBriefLines(p.Done) {
		if lintBrowserRE.MatchString(line) {
			return line, true
		}
	}
	return "", false
}

// browserCannot lists the harness and OS pairs known to be unable to run a
// browser test. A pair that is not here is taken to be able, so an unknown
// harness is never left out of a pick.
//
// How each fact is known:
//   - claude on darwin: Claude Code's own worker sandbox (claudeWorkerSandbox
//     in internal/daemon: no network section) denies a local listen (EPERM)
//     and Chromium's startup. About 45 worker friction reports (wbg-chat-proto,
//     office-politics, tenebrae-strats) say so, and the leads ran every
//     browser check by hand.
//   - claude on linux is not listed. Its sandbox is bubblewrap with a private
//     network, and a local listen inside "bwrap --unshare-net" was measured to
//     work (python3 bind and listen on 127.0.0.1, 2026-11-18). Chromium itself
//     was not run there, and no report names Linux.
//   - opencode, codex and pi run under agent-net's own OS wrapper, where local
//     ports and Chromium work: one wbg-chat-proto OpenCode worker ran 138
//     browser tests.
var browserCannot = map[string]map[string]bool{
	"claude": {"darwin": true},
}

// HarnessRunsBrowser tells whether a worker of this harness can run a browser
// test on a machine of this OS (a runtime.GOOS name). See browserCannot.
func HarnessRunsBrowser(harness, goos string) bool {
	return !browserCannot[harness][goos]
}

// BrowserFlag is the advisory flag for a start whose Done needs a browser and
// whose harness cannot run one on its machine. It never refuses or holds.
func BrowserFlag(line, harness, goos string) BriefFlag {
	return briefFlag(BriefCheckBrowser, line, fmt.Sprintf(
		"a %s worker cannot run a browser on %s. Run this check after the hand-back, or start the task on opencode or codex.", harness, goos))
}
