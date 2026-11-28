package daemon

import "strings"

// The macOS wrapper is sandbox-exec with a Seatbelt profile built from the same
// osSandboxPolicy that bubblewrap gets on Linux. This file has no build tag, so
// the profile is tested on every OS; only ossandbox_darwin.go runs it.
//
// Seatbelt has no mount namespace. The host stays visible and the profile says
// only what may be written: the later of two matching rules wins, so the
// read-only paths of the policy are denied after the writable ones are
// allowed. Reads, the network and unix sockets are not limited (decision 0197).

// sbQuote is s as a Seatbelt string literal.
func sbQuote(s string) string {
	s = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(s)
	return `"` + s + `"`
}

func sbSubpaths(paths []string) string {
	var b strings.Builder
	for _, p := range paths {
		b.WriteString(" (subpath " + sbQuote(p) + ")")
	}
	return b.String()
}

// seatbeltProfile is the sandbox-exec profile for p.
func seatbeltProfile(p osSandboxPolicy) string {
	var b strings.Builder
	b.WriteString("(version 1)\n(allow default)\n(deny file-write*)\n")
	if len(p.Writable) > 0 {
		b.WriteString("(allow file-write*" + sbSubpaths(p.Writable) + ")\n")
	}
	// The null device and terminals are written by every shell command. Opening a
	// new pseudo-terminal writes /dev/ptmx (posix_openpt); without it a tool that
	// allocates a PTY fails with EPERM. The slave side is /dev/ttysNNN, which the
	// terminal rule already covers.
	b.WriteString("(allow file-write* (literal \"/dev/null\") (literal \"/dev/zero\") (literal \"/dev/dtracehelper\") (literal \"/dev/ptmx\") (regex #\"^/dev/tty\") (regex #\"^/dev/fd/\"))\n")
	var ro []string
	for _, r := range p.ReadOnly {
		ro = append(ro, r.Dst)
	}
	if len(ro) > 0 {
		b.WriteString("(deny file-write*" + sbSubpaths(ro) + ")\n")
	}
	return b.String()
}

// seatbeltArgs is the sandbox-exec command line that runs bin under p. The
// working folder is set by the daemon (exec.Cmd.Dir), as sandbox-exec has no
// option for it.
func seatbeltArgs(p osSandboxPolicy, bin string, args []string) []string {
	return append([]string{"-p", seatbeltProfile(p), bin}, args...)
}
