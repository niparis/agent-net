package daemon

import (
	"fmt"
	"strings"
)

// The macOS wrapper is sandbox-exec with a Seatbelt profile built from the same
// osSandboxPolicy that bubblewrap gets on Linux. This file has no build tag, so
// the profile is tested on every OS; only ossandbox_darwin.go runs it.
//
// Seatbelt has no mount namespace. The host stays visible and the profile says
// what may be written: the later of two matching rules wins, so the read-only
// paths of the policy are denied after the writable ones are allowed. Reads are
// not limited, as on Linux.

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
	writable := append([]string{}, p.Writable...)
	if p.Tmp != "" {
		writable = append(writable, p.Tmp)
	}
	if len(writable) > 0 {
		b.WriteString("(allow file-write*" + sbSubpaths(writable) + ")\n")
	}
	// The null device and terminals are written by every shell command.
	b.WriteString("(allow file-write* (literal \"/dev/null\") (literal \"/dev/zero\") (literal \"/dev/dtracehelper\") (regex #\"^/dev/tty\") (regex #\"^/dev/fd/\"))\n")
	var ro []string
	for _, r := range p.ReadOnly {
		ro = append(ro, r.Dst)
	}
	if len(ro) > 0 {
		b.WriteString("(deny file-write*" + sbSubpaths(ro) + ")\n")
	}
	// Folders and files the policy hides are neither read nor written.
	hidden := append(append([]string{}, p.Hide...), p.Mask...)
	if len(hidden) > 0 {
		b.WriteString("(deny file-read* file-write*" + sbSubpaths(hidden) + ")\n")
	}
	if !p.Network {
		b.WriteString("(deny network*)\n")
	}
	// A worker may not reach the sockets of other services of the owner. The
	// resolver stays, so host names still resolve.
	b.WriteString("(deny network-outbound (remote unix-socket))\n")
	b.WriteString("(allow network-outbound (remote unix-socket (path-literal \"/private/var/run/mDNSResponder\")))\n")
	if p.Socket != "" {
		b.WriteString(fmt.Sprintf("(allow network-outbound (remote unix-socket (path-literal %s)))\n", sbQuote(p.Socket)))
	}
	return b.String()
}

// seatbeltArgs is the sandbox-exec command line that runs bin under p. The
// working folder is set by the daemon (exec.Cmd.Dir), as sandbox-exec has no
// option for it.
func seatbeltArgs(p osSandboxPolicy, bin string, args []string) []string {
	return append([]string{"-p", seatbeltProfile(p), bin}, args...)
}
