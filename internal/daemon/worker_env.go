package daemon

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// The environment a worker's tools need to work (friction reports of
// 2026-11-18). The daemon service starts with a minimal PATH, and a worker's
// folders are writable only inside its own worktree, so without this a worker
// loses time on "node: not found", a read-only uv or npm cache, a TLS error
// from uv or pip, and two workers that bind the same fixed port.

// workerPortCount is how many ports each worker owns from WORKER_PORT_BASE.
const workerPortCount = 100

// The pool of port ranges is below every OS's ephemeral range (Linux starts at
// 32768, macOS at 49152), so a range cannot collide with a port the OS picks.
const (
	workerPortFirst = 20000
	workerPortLast  = 29999
)

// workerEnvState is what the daemon finds out once and reuses for every start.
type workerEnvState struct {
	loginOnce sync.Once
	loginPath string
	caOnce    sync.Once
	caBundle  string
}

// loginShellPathTimeout bounds the one probe of the login shell.
const loginShellPathTimeout = 5 * time.Second

// loginPATH is the PATH of the owner's login shell, found once per daemon
// start. A service manager starts the daemon with a short PATH that lacks
// /opt/homebrew/bin, ~/.local/go/bin and the folders a shell profile adds.
// It is "" when the shell cannot tell.
func (d *Daemon) loginPATH() string {
	d.workerEnvState.loginOnce.Do(func() {
		begin := time.Now()
		path := probeLoginPATH(d.loginShell())
		d.workerEnvState.loginPath = path
		d.Logf("worker env: login PATH of %s probed in %s (%d entries)", d.loginShell(), time.Since(begin).Round(time.Millisecond), len(filepath.SplitList(path)))
	})
	return d.workerEnvState.loginPath
}

func (d *Daemon) loginShell() string {
	if d.cfg.testLoginShell != "" {
		return d.cfg.testLoginShell
	}
	if sh := os.Getenv("SHELL"); sh != "" {
		return sh
	}
	if runtime.GOOS == "darwin" {
		return "/bin/zsh"
	}
	return "/bin/sh"
}

const (
	pathBegin = "__AGENT_NET_PATH_BEGIN__"
	pathEnd   = "__AGENT_NET_PATH_END__"
)

// probeLoginPATH runs shell as a login shell and reads its $PATH. Profile
// files may print text, so the value sits between two markers.
func probeLoginPATH(shell string) string {
	ctx, cancel := context.WithTimeout(context.Background(), loginShellPathTimeout)
	defer cancel()
	var cmd *exec.Cmd
	if filepath.Base(shell) == "fish" {
		cmd = exec.CommandContext(ctx, shell, "-l", "-c", `printf '%s%s%s' `+pathBegin+` (string join : $PATH) `+pathEnd)
	} else {
		// -i too: zsh and bash read their PATH exports from .zshrc and .bashrc
		// as often as from the login files.
		cmd = exec.CommandContext(ctx, shell, "-l", "-i", "-c", `printf '%s%s%s' `+pathBegin+` "$PATH" `+pathEnd)
	}
	cmd.Stdin = nil
	cmd.WaitDelay = 250 * time.Millisecond
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil && out.Len() == 0 {
		return ""
	}
	s := out.String()
	i := strings.LastIndex(s, pathBegin)
	if i < 0 {
		return ""
	}
	s = s[i+len(pathBegin):]
	j := strings.Index(s, pathEnd)
	if j < 0 {
		return ""
	}
	return strings.TrimSpace(s[:j])
}

// mergePATH is current with the entries of extra that it lacks, in order. The
// daemon's own entries stay first, so a tool that resolved before still does.
func mergePATH(current, extra string) string {
	seen := map[string]bool{}
	var out []string
	for _, list := range []string{current, extra} {
		for _, p := range filepath.SplitList(list) {
			if p == "" || !filepath.IsAbs(p) || seen[p] {
				continue
			}
			seen[p] = true
			out = append(out, p)
		}
	}
	return strings.Join(out, string(os.PathListSeparator))
}

// systemCABundles are the places the OS keeps its trust store as one PEM file.
// It is a variable so a test can point it at a temporary file.
var systemCABundles = []string{
	"/etc/ssl/certs/ca-certificates.crt",   // Debian, Ubuntu, Arch
	"/etc/pki/tls/certs/ca-bundle.crt",     // Fedora, RHEL
	"/etc/ssl/ca-bundle.pem",               // openSUSE
	"/etc/ssl/cert.pem",                    // Alpine, macOS
	"/usr/local/etc/openssl@3/cert.pem",    // Homebrew OpenSSL on an Intel Mac
	"/opt/homebrew/etc/openssl@3/cert.pem", // Homebrew OpenSSL on Apple silicon
}

// keychainBundle exports the macOS keychains as one PEM file. /etc/ssl/cert.pem
// holds only the roots Apple ships; a local CA that the owner trusted sits in
// a keychain, which is where uv's native TLS reads it. It is a variable so a
// test can replace the call.
var keychainBundle = func(ctx context.Context) ([]byte, error) {
	return exec.CommandContext(ctx, "security", "find-certificate", "-a", "-p",
		"/Library/Keychains/System.keychain", "/System/Library/Keychains/SystemRootCertificates.keychain").Output()
}

// systemCA is the path of the machine's trust store as one PEM file, or "".
func (d *Daemon) systemCA() string {
	d.workerEnvState.caOnce.Do(func() {
		if runtime.GOOS == "darwin" && d.cfg.StateDir != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if pem, err := keychainBundle(ctx); err == nil && bytes.Contains(pem, []byte("BEGIN CERTIFICATE")) {
				path := filepath.Join(d.cfg.StateDir, "system-ca-bundle.pem")
				if err := os.WriteFile(path, pem, 0o644); err == nil {
					d.workerEnvState.caBundle = path
					return
				}
			}
		}
		for _, p := range systemCABundles {
			if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() && fi.Size() > 0 {
				d.workerEnvState.caBundle = p
				return
			}
		}
	})
	return d.workerEnvState.caBundle
}

// workerCacheRoot is the folder of a worker's package caches when it does not
// run in the OS wrapper. It sits in the worker's own folder, which the daemon
// removes with the worker.
func (d *Daemon) workerCacheRoot(w *worker) string {
	return filepath.Join(d.cfg.StateDir, "workers", w.id, "cache")
}

// workerCacheVars points the package managers at per-worker caches below root,
// so none of them writes to the owner's home folder, which a worker cannot
// write, and two workers never share a cache or its locks.
func workerCacheVars(root string) map[string]string {
	return map[string]string{
		"UV_CACHE_DIR":     filepath.Join(root, "uv"),
		"npm_config_cache": filepath.Join(root, "npm"),
		"PIP_CACHE_DIR":    filepath.Join(root, "pip"),
	}
}

// workerToolEnv is what every harness's worker process gets on top of the
// daemon's own environment. A variable the owner set in the daemon's
// environment for the TLS bundle is kept.
func (d *Daemon) workerToolEnv(w *worker) map[string]string {
	set := workerCacheVars(d.workerCacheRoot(w))
	if err := os.MkdirAll(d.workerCacheRoot(w), 0o700); err != nil {
		d.Logf("worker %s: cache folder: %v", w.id, err)
	}
	set["PATH"] = mergePATH(os.Getenv("PATH"), d.loginPATH())
	if ca := d.systemCA(); ca != "" {
		// uv and pip carry their own CA list, which lacks a local CA that the
		// system trusts. uv reads the system store with UV_NATIVE_TLS; pip
		// takes a file.
		set["UV_NATIVE_TLS"] = "1"
		if os.Getenv("PIP_CERT") == "" {
			set["PIP_CERT"] = ca
		}
		if os.Getenv("SSL_CERT_FILE") == "" {
			set["SSL_CERT_FILE"] = ca
		}
	}
	if base := d.workerPortBase(w); base > 0 {
		set["WORKER_PORT_BASE"] = fmt.Sprint(base)
	}
	return set
}

// workerPortBase gives w a range of workerPortCount ports that no other live
// worker on this machine owns and that nothing listens on now. It keeps the
// range for the life of the worker. 0 means every range is taken.
func (d *Daemon) workerPortBase(w *worker) int {
	for {
		d.wmu.Lock()
		if w.portBase > 0 {
			defer d.wmu.Unlock()
			return w.portBase
		}
		used := map[int]bool{}
		for _, other := range d.workers {
			if other != w && other.portBase > 0 {
				used[other.portBase] = true
			}
		}
		d.wmu.Unlock()
		// The probe runs outside the lock; the claim below checks again.
		pick := 0
		for base := workerPortFirst; base+workerPortCount-1 <= workerPortLast; base += workerPortCount {
			if !used[base] && portRangeFree(base, workerPortCount) {
				pick = base
				break
			}
		}
		if pick == 0 {
			return 0
		}
		d.wmu.Lock()
		taken := false
		for _, other := range d.workers {
			if other != w && other.portBase == pick {
				taken = true
			}
		}
		if !taken && w.portBase == 0 {
			w.portBase = pick
		}
		d.wmu.Unlock()
	}
}

// portRangeFree tells whether nothing listens on the n ports from base.
func portRangeFree(base, n int) bool {
	for p := base; p < base+n; p++ {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err != nil {
			return false
		}
		ln.Close()
	}
	return true
}
