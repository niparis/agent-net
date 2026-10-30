package daemon

import "strings"

// processEnviron is the environment of a process. Tests replace it so that the
// herdr or tmux the tests run in is not seen.
var processEnviron = osProcessEnviron

// processEnvVar reads one variable of a process's environment. It returns ""
// when the process is not ours or the variable is not set.
func processEnvVar(pid int, key string) string {
	prefix := key + "="
	for _, kv := range processEnviron(pid) {
		if strings.HasPrefix(kv, prefix) {
			return kv[len(prefix):]
		}
	}
	return ""
}

// parseProcArgs2 returns the environment strings ("KEY=value") in the buffer
// that macOS gives for sysctl kern.procargs2.<pid>. The layout is:
//
//	int32  argc (native byte order, little endian on every Mac)
//	string the executable path, NUL ended
//	NULs   padding
//	argc   argument strings, each NUL ended
//	rest   environment strings, each NUL ended; an empty string ends the list
//
// Values may hold spaces and any other byte except NUL, so the strings are cut
// at NUL and never split on anything else.
func parseProcArgs2(buf []byte) []string {
	if len(buf) < 4 {
		return nil
	}
	argc := int(uint32(buf[0]) | uint32(buf[1])<<8 | uint32(buf[2])<<16 | uint32(buf[3])<<24)
	rest := buf[4:]
	next := func() (string, bool) { // one NUL ended string
		for i, b := range rest {
			if b == 0 {
				s := string(rest[:i])
				rest = rest[i+1:]
				return s, true
			}
		}
		if len(rest) > 0 { // the kernel may cut the last string
			s := string(rest)
			rest = nil
			return s, true
		}
		return "", false
	}
	if _, ok := next(); !ok { // executable path
		return nil
	}
	for len(rest) > 0 && rest[0] == 0 { // padding
		rest = rest[1:]
	}
	for i := 0; i < argc; i++ {
		if _, ok := next(); !ok {
			return nil
		}
	}
	var env []string
	for {
		s, ok := next()
		if !ok || s == "" {
			break
		}
		if strings.Contains(s, "=") {
			env = append(env, s)
		}
	}
	return env
}
