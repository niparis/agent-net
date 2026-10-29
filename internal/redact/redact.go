// Package redact hides secrets in text that is about to leave a machine or be
// shown: the command lines of processes and the text of a session.
//
// It is a best-effort filter for the common shapes of a secret. It cannot
// know every secret, so the callers keep the text short and send it only when
// the owner asks.
package redact

import (
	"regexp"
	"strings"
	"unicode"
)

// Mask replaces a secret.
const Mask = "[REDACTED]"

var (
	// Whole tokens with a known prefix.
	tokenPatterns = []*regexp.Regexp{
		regexp.MustCompile(`sk-[A-Za-z0-9_\-]{16,}`),                                          // OpenAI, Anthropic and look-alikes
		regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}`),                                    // GitHub tokens
		regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}`),                                  // GitHub fine-grained tokens
		regexp.MustCompile(`\b(?:AKIA|ASIA|AGPA|AIDA|AROA)[0-9A-Z]{16}\b`),                    // AWS access key IDs
		regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9\-]{10,}`),                                 // Slack
		regexp.MustCompile(`\bAIza[0-9A-Za-z_\-]{35}\b`),                                      // Google API keys
		regexp.MustCompile(`\bglpat-[A-Za-z0-9_\-]{20,}`),                                     // GitLab
		regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}`), // JWT
	}
	privateKey = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(?:-----END [A-Z ]*PRIVATE KEY-----|$)`)
	// Authorization headers and bearer tokens.
	authHeader = regexp.MustCompile(`(?i)(authorization["']?\s*[:=]\s*["']?)(?:(?:bearer|basic|token)\s+)?[^\s"',;]+`)
	bearer     = regexp.MustCompile(`(?i)(\bbearer\s+)[A-Za-z0-9._~+/=\-]{8,}`)
	// user:password@ inside a URL.
	urlCreds = regexp.MustCompile(`(?i)(\b[a-z][a-z0-9+.\-]*://[^\s/:@]*:)[^\s/@]+(@)`)
	// name=value, name: value and "name": "value" where the name ends in a
	// word that says "secret". The name may carry a prefix such as MY_APP_.
	keyValue = regexp.MustCompile(`(?i)(\b[a-z0-9_.\-]*(?:api[_\-]?key|apikey|access[_\-]?key|private[_\-]?key|secret|token|passw(?:or)?d|passwd|pwd|credentials?|auth|key)["']?\s*[=:]\s*)("[^"\n]*"|'[^'\n]*'|[^\s"',;&)}\]]+)`)
	// A flag whose name says "secret", with its value after = or a space.
	secretFlag = regexp.MustCompile(`(?i)(\s--?[a-z0-9\-]*(?:password|passwd|token|secret|api-?key|apikey|auth|credential)[a-z0-9\-]*)(=|\s+)(\S+)`)
)

// Secrets returns s with the secrets it can recognise replaced by Mask.
func Secrets(s string) string {
	if s == "" {
		return s
	}
	s = privateKey.ReplaceAllString(s, Mask)
	s = authHeader.ReplaceAllString(s, "${1}"+Mask)
	s = bearer.ReplaceAllString(s, "${1}"+Mask)
	s = urlCreds.ReplaceAllString(s, "${1}"+Mask+"${2}")
	s = keyValue.ReplaceAllString(s, "${1}"+Mask)
	for _, re := range tokenPatterns {
		s = re.ReplaceAllString(s, Mask)
	}
	return s
}

// ArgsLen is how many characters of a command line are kept.
const ArgsLen = 80

// Args makes a command line safe to show: secrets are masked (also the value
// of a flag like --token abc), control characters become spaces and the
// result is cut to ArgsLen characters. Masking comes before cutting, so a cut
// can never leave half a secret.
func Args(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	s = Secrets(s)
	s = secretFlag.ReplaceAllString(" "+s, "${1}${2}"+Mask)
	s = strings.Join(strings.Fields(s), " ")
	return Cut(s, ArgsLen)
}

// Cut returns the first n characters of s (not bytes).
func Cut(s string, n int) string {
	if n <= 0 {
		return ""
	}
	i := 0
	for pos := range s {
		if i == n {
			return s[:pos]
		}
		i++
	}
	return s
}
