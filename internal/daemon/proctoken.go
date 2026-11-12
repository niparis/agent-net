package daemon

import "strings"

// startToken makes the token that identifies one run of a process from its
// start time as ps prints it. ps pads a one-digit day with a second space
// ("Mon Nov  9 ..."), so the text is split into words and joined with one
// space. Every token made on macOS goes through here, both the one read with
// the process view and the one from procToken, so the two always compare equal.
func startToken(lstart string) string {
	return strings.Join(strings.Fields(lstart), " ")
}
