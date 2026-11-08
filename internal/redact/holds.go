package redact

import (
	"regexp"
	"strings"
	"unicode"
)

// secretValueMin is the shortest value that can be a secret by its shape.
const secretValueMin = 16

// genericRules are the name-based rules, with the group that holds each value.
// HoldsSecret refuses one only when its value looks like a secret.
var genericRules = []struct {
	re    *regexp.Regexp
	value int
}{
	{keyValue, 2},
	{authHeader, 2},
	{bearer, 2},
	{urlCreds, 2},
	{secretFlag, 3},
}

// HoldsSecret reports whether a text file holds a secret that agent-net must
// not send. It is the file-send rule. It uses the same patterns as Secrets,
// with one difference. A private key block or a known token shape always
// counts. A name rule (token, key, password, auth, ...) counts only when its
// value looks like a secret (see looksLikeSecret), so code such as
// key='cto_session' or token := req.Token is sent. Secrets keeps the broad
// rule for logs and messages, where over-masking costs nothing.
func HoldsSecret(s string) bool {
	// The space lets a flag at the start of the text match, as in Args.
	s = " " + s
	if privateKey.MatchString(s) {
		return true
	}
	for _, re := range tokenPatterns {
		if re.MatchString(s) {
			return true
		}
	}
	for _, g := range genericRules {
		for _, m := range g.re.FindAllStringSubmatch(s, -1) {
			if looksLikeSecret(m[g.value]) {
				return true
			}
		}
	}
	return false
}

// looksLikeSecret reports whether v, the value after a secret-like name, has
// the shape of a random secret. Quotes around v do not count. The value must be:
//   - at least 16 characters;
//   - one token of letters, digits and _ - + / = ! @ # & * ^ ~ (no spaces,
//     dots, brackets, quotes, $ or %, so code, paths, env references and
//     templates are out);
//   - mixed: a letter and a digit, and at least eight different characters;
//   - not an upper case env var name such as MY_APP_KEY_2;
//   - not a path such as /tmp/agent-net or ~/config;
//   - not an identifier such as cto_session_v2_handler.
func looksLikeSecret(v string) bool {
	v = unquote(v)
	if strings.HasPrefix(v, "/") || strings.HasPrefix(v, "~/") {
		return false
	}
	// A YAML anchor, alias or tag (&name, *name, !tag) names a value. The name
	// is judged, so the sigil is dropped.
	if v != "" && strings.IndexByte("&*!", v[0]) >= 0 {
		v = v[1:]
	}
	if len(v) < secretValueMin {
		return false
	}
	var letter, digit bool
	seen := map[byte]bool{}
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case isLetter(c):
			letter = true
		case isDigit(c):
			digit = true
		case strings.IndexByte("_-+/=!@#&*^~", c) < 0:
			return false
		}
		seen[c] = true
	}
	if !letter || !digit || len(seen) < 8 {
		return false
	}
	return !isEnvName(v) && !isIdentifier(v)
}

// isEnvName reports whether v is an upper case name with an underscore, such
// as MY_APP_KEY_2.
func isEnvName(v string) bool {
	return strings.IndexByte(v, '_') >= 0 && !strings.ContainsFunc(v, unicode.IsLower)
}

// isIdentifier reports whether v reads as a name rather than a secret. A name
// is words joined by _ or -, or camel case, and it has only letters, digits,
// _ and -. A run of letters is at least three long, except a version tag such
// as v2. A run of digits is at most three long, so a number like 773 is fine
// and a hex string is not. At most two in five letters are capitals. A random
// token fails one of these rules.
func isIdentifier(v string) bool {
	if strings.IndexAny(v, "_-") < 0 && !camelCase(v) {
		return false
	}
	letters, capitals := 0, 0
	for i := 0; i < len(v); {
		c := v[i]
		switch {
		case c == '_' || c == '-':
			if i+1 >= len(v) {
				return false
			}
			i++
		case isDigit(c):
			j := i
			for j < len(v) && isDigit(v[j]) {
				j++
			}
			if j-i > 3 {
				return false
			}
			i = j
		case isLetter(c):
			j := i
			for j < len(v) && isLetter(v[j]) {
				j++
			}
			run := j - i
			version := run == 1 && (c == 'v' || c == 'V') && j < len(v) && isDigit(v[j])
			if run < 3 && !version {
				return false
			}
			for k := i; k < j; k++ {
				if isUpper(v[k]) {
					capitals++
				}
			}
			letters += run
			i = j
		default:
			return false
		}
	}
	return capitals*5 <= letters*2
}

// camelCase reports whether v has a capital letter that follows a lower case
// letter or a digit, as in userV2Token or session2Token.
func camelCase(v string) bool {
	for i := 1; i < len(v); i++ {
		if isUpper(v[i]) && (isDigit(v[i-1]) || isLower(v[i-1])) {
			return true
		}
	}
	return false
}

// unquote removes one pair of matching quotes around v.
func unquote(v string) string {
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		return v[1 : len(v)-1]
	}
	return v
}

func isLetter(c byte) bool { return isLower(c) || isUpper(c) }
func isLower(c byte) bool  { return c >= 'a' && c <= 'z' }
func isUpper(c byte) bool  { return c >= 'A' && c <= 'Z' }
func isDigit(c byte) bool  { return c >= '0' && c <= '9' }
