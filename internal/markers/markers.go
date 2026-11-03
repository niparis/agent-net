// Package markers identifies and neutralizes agent-net framing in untrusted text.
package markers

import "regexp"

// Pattern matches the start of an agent-net framing marker.
var Pattern = regexp.MustCompile(`(?i)\[(\s*)agent-net`)

// Neutralize breaks each marker while keeping the text readable.
func Neutralize(text string) string {
	return Pattern.ReplaceAllString(text, "[​${1}agent-net")
}
