package daemon

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/redact"
	"golang.org/x/sys/unix"
)

// Files between machines (decision 0054, hardening plan W5 step T3).
//
// The sending daemon checks every path, snapshots and hashes the files, sends
// the message with its manifest and then pushes the bytes: over Taildrop when
// both machines are on the same tailnet, else through the gateway in parts.
// The receiving daemon collects the files into its inbox and delivers the
// message only when every file is in.

// FileConfig holds the folders and timings of files between machines. Zero
// fields take the defaults.
type FileConfig struct {
	// Home is the user's home folder, for the credential paths that are never
	// sent. Default: the home folder.
	Home string
	// Downloads is where the Mac app saves incoming Taildrops and where a
	// Linux daemon moves the Taildrops it was not told about. Default:
	// ~/Downloads.
	Downloads string
	// Layout is "darwin" (Taildrops land in Downloads) or "linux" (they wait
	// for `tailscale file get`). Default: this machine's OS.
	Layout string
	// TailscaleIP returns this machine's Tailscale address, or "". It goes to
	// the gateway in hello. Default: the address of a local interface in
	// 100.64.0.0/10.
	TailscaleIP func() string
	// TailscalePATH is the search path for the tailscale binary. Empty uses
	// $PATH. Tests point it at a folder with a fake tailscale.
	TailscalePATH string
	// TailscaleFallback lists the tailscale binaries tried when there is none
	// on the search path. Nil uses the macOS app's binary; an empty list turns
	// the fallback off, which tests use.
	TailscaleFallback []string
	// Poll is the first wait between two looks for missing files; the wait
	// doubles up to PollMax while nothing arrives. Default: 2 s and 30 s.
	Poll, PollMax time.Duration
	// Wait bounds how long the receiver waits for the files of one message.
	// Default 0: until the message expires on the gateway.
	Wait time.Duration
}

// Paths under the daemon's state folder.
const (
	fileInboxFolder     = "inbox"          // inbox/<session>/<message>/<name>
	fileOutgoingFolder  = "files/outgoing" // snapshots of files being sent
	fileTaildropFolder  = "files/taildrop" // Linux staging for `tailscale file get`
	fileLocalFolder     = "files/local"    // same-machine handoff, by unique name
	fileSnapshotMaxAge  = 24 * time.Hour   // leftover snapshots after a crash
	fileTextSniffBytes  = 8000             // the start of a file read to tell text from binary
	tailscaleMacApp     = "/Applications/Tailscale.app/Contents/MacOS/Tailscale"
	defaultFilePoll     = 2 * time.Second  // first wait between looks for files
	defaultFilePollMax  = 30 * time.Second // longest wait between looks for files
	defaultFileWaitCap  = 24 * time.Hour   // when a message carries no expiry
	fileTaildropTimeout = 10 * time.Minute // one `tailscale file cp` of up to 100 MB
	fileCommandTimeout  = 30 * time.Second // `tailscale status` and `file get`
	fileSecretRefusal   = "looks like it holds a secret"
)

func (c FileConfig) withDefaults() FileConfig {
	if c.Home == "" {
		c.Home = home()
	}
	if c.Downloads == "" {
		c.Downloads = filepath.Join(c.Home, "Downloads")
	}
	if c.Layout == "" {
		c.Layout = runtime.GOOS
	}
	if c.TailscaleIP == nil {
		c.TailscaleIP = localTailscaleIP
	}
	if c.TailscaleFallback == nil && runtime.GOOS == "darwin" {
		c.TailscaleFallback = []string{tailscaleMacApp}
	}
	if c.Poll <= 0 {
		c.Poll = defaultFilePoll
	}
	if c.PollMax < c.Poll {
		c.PollMax = max(defaultFilePollMax, c.Poll)
	}
	return c
}

// FileRules are the checks a file passes before it is sent.
type FileRules struct {
	// Roots are the allowed source folders: the session's project and
	// worktree. The file system root is never a root.
	Roots []string
	// Temp are the temporary folders that are also allowed. Nil means /tmp;
	// an empty list allows none.
	Temp []string
	// Home is the user's home folder. Its credential folders and files are
	// never sent, whatever the roots.
	Home string
	// Private are agent-net's own folders (state and config). A file in one
	// is refused unless a root inside that folder holds it, such as a
	// worker's worktree under the state folder.
	Private []string
	// AllowSecrets sends a text file the secret scanner flagged. Only the
	// owner sets it.
	AllowSecrets bool
	// ConfineSymlinks keeps each path within the root it was named under.
	ConfineSymlinks bool
}

// credentialPaths are the folders and files under the home folder that hold
// keys, tokens or passwords. They are never sent. agent-net's own folders are
// in FileRules.Private, so a worker's worktree there stays usable.
var credentialPaths = []string{
	".ssh", ".gnupg", ".aws", ".azure", ".kube", ".docker", ".pki",
	".password-store", ".netrc", ".git-credentials", ".npmrc", ".pypirc",
	".config/gcloud", ".config/gh", ".config/hub", ".local/share/keyrings",
	"Library/Keychains", ".claude/.credentials.json", ".codex/auth.json", ".local/share/opencode/auth.json",
	".pi/agent/auth.json", ".gemini/oauth_creds.json",
}

// OutgoingFile is one checked file: its manifest entry, the path it came
// from and the snapshot its bytes are sent from.
type OutgoingFile struct {
	Entry  protocol.FileEntry
	Source string
	Copy   string
}

// OutgoingFiles is a checked set of files, ready to send. Remove deletes the
// snapshot folder when the send is over.
type OutgoingFiles struct {
	Dir   string
	Files []OutgoingFile
}

// Manifest returns the manifest entries, in order.
func (o *OutgoingFiles) Manifest() []protocol.FileEntry {
	out := make([]protocol.FileEntry, len(o.Files))
	for i, f := range o.Files {
		out[i] = f.Entry
	}
	return out
}

// Remove deletes the snapshots.
func (o *OutgoingFiles) Remove() error {
	if o == nil || o.Dir == "" {
		return nil
	}
	return os.RemoveAll(o.Dir)
}

// PrepareFiles checks each path against the rules and the limits, resolves
// symlinks first, copies each file into a new folder under snapshots while
// hashing it, and runs the secret scanner on text files. The caller sends
// from the copies and calls Remove when done. On a refusal nothing is left
// behind.
func PrepareFiles(paths []string, rules FileRules, snapshots string) (*OutgoingFiles, error) {
	return prepareFiles(paths, rules, snapshots, nil)
}

// afterCheck lets tests replace a checked path before the snapshot opens it.
func prepareFiles(paths []string, rules FileRules, snapshots string, afterCheck func()) (*OutgoingFiles, error) {
	if len(paths) == 0 {
		return nil, errors.New("no files to send")
	}
	if len(paths) > protocol.MaxFilesPerMessage {
		return nil, fmt.Errorf("%d files: at most %d files per message", len(paths), protocol.MaxFilesPerMessage)
	}
	type checked struct {
		source, real, name string
		info               os.FileInfo
	}
	var list []checked
	names := map[string]string{}
	for _, p := range paths {
		real, name, info, err := checkSendPath(p, rules)
		if err != nil {
			return nil, err
		}
		if first, ok := names[name]; ok {
			return nil, fmt.Errorf("%s and %s are both named %q; one message cannot hold two files with one name", first, p, name)
		}
		names[name] = p
		list = append(list, checked{source: p, real: real, name: name, info: info})
	}
	if afterCheck != nil {
		afterCheck()
	}
	if err := os.MkdirAll(snapshots, 0o700); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(snapshots, "o_")
	if err != nil {
		return nil, err
	}
	out := &OutgoingFiles{Dir: dir}
	for i, c := range list {
		entry := protocol.FileEntry{ID: fmt.Sprintf("f%d", i+1), Name: c.name}
		copyPath := filepath.Join(dir, entry.ID)
		size, sum, text, err := snapshotFile(c.real, copyPath, c.info, rules.ConfineSymlinks)
		if err == nil && text && !rules.AllowSecrets {
			err = scanForSecrets(copyPath, c.name)
		}
		if err != nil {
			out.Remove()
			return nil, err
		}
		entry.Size, entry.SHA256 = size, sum
		out.Files = append(out.Files, OutgoingFile{Entry: entry, Source: c.source, Copy: copyPath})
	}
	if err := protocol.ValidateFiles(out.Manifest()); err != nil {
		out.Remove()
		return nil, err
	}
	return out, nil
}

// checkSendPath applies the path rules to one file. It returns the resolved
// path, the name the receiver sees, and the identity of the checked file.
func checkSendPath(p string, rules FileRules) (real, name string, info os.FileInfo, err error) {
	if !filepath.IsAbs(p) {
		return "", "", nil, fmt.Errorf("%s: give the full path of the file", p)
	}
	clean := filepath.Clean(p)
	name = filepath.Base(clean)
	if err := protocol.CheckFileName(name); err != nil {
		return "", "", nil, fmt.Errorf("%s: %v", p, err)
	}
	real, err = filepath.EvalSymlinks(clean)
	if err != nil {
		return "", "", nil, fmt.Errorf("cannot read %s: %w", p, err)
	}
	info, err = os.Lstat(real)
	if err != nil {
		return "", "", nil, fmt.Errorf("%s: cannot read it: %v", p, err)
	}
	if !info.Mode().IsRegular() {
		return "", "", nil, fmt.Errorf("%s is not a regular file", p)
	}
	if info.Size() > protocol.MaxFileBytes {
		return "", "", nil, fmt.Errorf("%s is %d MB; the limit is %d MB per file", p, info.Size()>>20, protocol.MaxFileBytes>>20)
	}
	if rules.ConfineSymlinks {
		if err := checkFileRoot(clean, real, rules); err != nil {
			return "", "", nil, err
		}
	}
	roots := resolvedRoots(rules.Roots)
	// A worktree under a private folder can itself be named through a
	// symlink. Check both the named and canonical root against private paths.
	privateRoots := append([]string{}, roots...)
	for _, r := range rules.Roots {
		if !filepath.IsAbs(r) || filepath.Clean(r) == string(filepath.Separator) {
			continue
		}
		if canonical, err := filepath.EvalSymlinks(r); err == nil && filepath.Clean(canonical) != string(filepath.Separator) {
			privateRoots = append(privateRoots, filepath.Clean(r))
		}
	}
	for _, candidate := range []string{clean, real} {
		if why := forbiddenPath(candidate, rules, privateRoots); why != "" {
			return "", "", nil, fmt.Errorf("%s: agent-net never sends files from %s", p, why)
		}
	}
	allowed := false
	temp := rules.Temp
	if temp == nil {
		temp = []string{"/tmp"}
	}
	for _, r := range append(roots, resolvedRoots(temp)...) {
		if within(real, r) {
			allowed = true
			break
		}
	}
	if !allowed {
		return "", "", nil, fmt.Errorf("%s is outside the session's project and /tmp; agent-net sends only files from there", p)
	}
	return real, name, info, nil
}

// checkFileRoot uses the most specific allowed root that contains the named
// path, so a link in a project cannot escape to a different folder in /tmp.
func checkFileRoot(clean, real string, rules FileRules) error {
	temp := rules.Temp
	if temp == nil {
		temp = []string{"/tmp"}
	}
	var root, resolved string
	matchLen := 0
	for _, r := range append(append([]string{}, rules.Roots...), temp...) {
		if !filepath.IsAbs(r) || filepath.Clean(r) == string(filepath.Separator) {
			continue
		}
		r = filepath.Clean(r)
		canonical, err := filepath.EvalSymlinks(r)
		if err != nil {
			continue
		}
		canonical = filepath.Clean(canonical)
		if canonical == string(filepath.Separator) {
			continue
		}
		for _, spelling := range []string{r, canonical} {
			if within(clean, spelling) && len(spelling) > matchLen {
				root, resolved, matchLen = spelling, canonical, len(spelling)
			}
		}
	}
	if root == "" {
		return fmt.Errorf("%s is outside the session's project and /tmp", clean)
	}
	if !within(real, resolved) {
		return fmt.Errorf("%s: a symlink points outside the source folder %s", clean, root)
	}
	return nil
}

// resolvedRoots resolves symlinks in the roots and drops empty ones and the
// file system root.
func resolvedRoots(roots []string) []string {
	var out []string
	for _, r := range roots {
		if r == "" || !filepath.IsAbs(r) {
			continue
		}
		if real, err := filepath.EvalSymlinks(r); err == nil {
			r = real
		}
		r = filepath.Clean(r)
		if r == string(filepath.Separator) {
			continue
		}
		out = append(out, r)
	}
	return out
}

// forbiddenPath says why a path may never be sent, or "" when it may.
func forbiddenPath(p string, rules FileRules, roots []string) string {
	if rules.Home != "" {
		homes := []string{filepath.Clean(rules.Home)}
		if real, err := filepath.EvalSymlinks(rules.Home); err == nil && real != homes[0] {
			homes = append(homes, real)
		}
		for _, h := range homes {
			for _, c := range credentialPaths {
				if within(p, filepath.Join(h, filepath.FromSlash(c))) {
					return "~/" + c
				}
			}
		}
	}
	for _, private := range rules.Private {
		if private == "" {
			continue
		}
		dirs := []string{filepath.Clean(private)}
		if real, err := filepath.EvalSymlinks(private); err == nil && real != dirs[0] {
			dirs = append(dirs, real)
		}
		for _, dir := range dirs {
			if !within(p, dir) {
				continue
			}
			inner := false
			for _, r := range roots {
				if r != dir && within(r, dir) && within(p, r) {
					inner = true
					break
				}
			}
			if !inner {
				return "agent-net's own folder " + private
			}
		}
	}
	return ""
}

// within tells whether p is dir or inside it.
func within(p, dir string) bool {
	rel, err := filepath.Rel(dir, p)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// openSnapshotSource opens the already resolved path without following any
// symlink. A checked directory may itself have been moved and replaced by a
// symlink, even when the final file still has the same device and inode.
func openSnapshotSource(path string) (*os.File, error) {
	dir, err := unix.Open(string(filepath.Separator), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	parts := strings.Split(strings.TrimPrefix(filepath.Clean(path), string(filepath.Separator)), string(filepath.Separator))
	for i, part := range parts {
		flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC
		if i == len(parts)-1 {
			// Opening a substituted FIFO must not block before fstat.
			flags |= unix.O_NONBLOCK
		} else {
			flags |= unix.O_DIRECTORY
		}
		next, openErr := unix.Openat(dir, part, flags, 0)
		_ = unix.Close(dir)
		if openErr != nil {
			return nil, &os.PathError{Op: "open", Path: path, Err: openErr}
		}
		dir = next
	}
	return os.NewFile(uintptr(dir), path), nil
}

// snapshotFile copies src to dst while hashing it, and tells whether the
// bytes look like text. It refuses a file that grew past the limit.
func snapshotFile(src, dst string, checked os.FileInfo, confined bool) (size int64, sum string, text bool, err error) {
	in, err := openSnapshotSource(src)
	if err != nil {
		return 0, "", false, err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return 0, "", false, err
	}
	if !info.Mode().IsRegular() || !os.SameFile(checked, info) {
		return 0, "", false, fmt.Errorf("%s changed after its path was checked", src)
	}
	if confined {
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return 0, "", false, fmt.Errorf("%s: cannot check hard links", src)
		}
		if stat.Nlink > 1 {
			return 0, "", false, fmt.Errorf("%s has multiple hard links; confined files must have only one", src)
		}
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, "", false, err
	}
	h := sha256.New()
	sniff := &prefixBuffer{max: fileTextSniffBytes}
	size, err = io.Copy(io.MultiWriter(out, h, sniff), io.LimitReader(in, protocol.MaxFileBytes+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return 0, "", false, err
	}
	if size > protocol.MaxFileBytes {
		return 0, "", false, fmt.Errorf("%s grew past the %d MB limit while it was read", src, protocol.MaxFileBytes>>20)
	}
	return size, hex.EncodeToString(h.Sum(nil)), looksLikeText(sniff.b, size), nil
}

// prefixBuffer keeps the first max bytes written to it.
type prefixBuffer struct {
	b   []byte
	max int
}

func (p *prefixBuffer) Write(b []byte) (int, error) {
	if room := p.max - len(p.b); room > 0 {
		p.b = append(p.b, b[:min(room, len(b))]...)
	}
	return len(b), nil
}

// looksLikeText tells text from binary the way Git does: no NUL byte in the
// start of the file, and valid UTF-8 there (a character cut at the end of the
// sample is allowed).
func looksLikeText(start []byte, size int64) bool {
	if bytes.IndexByte(start, 0) >= 0 {
		return false
	}
	if int64(len(start)) < size {
		for i := 0; i < utf8.UTFMax && len(start) > 0 && !utf8.Valid(start); i++ {
			start = start[:len(start)-1]
		}
	}
	return utf8.Valid(start)
}

// scanForSecrets runs the existing secret scanner (the one that keeps
// secrets out of skills) on a text file. The refusal never repeats the text.
func scanForSecrets(path, name string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if s := string(b); redact.Secrets(s) != s {
		return fmt.Errorf("%s %s; agent-net does not send it. Remove the secret, or the owner sends the file anyway", name, fileSecretRefusal)
	}
	return nil
}

// cleanFileFolders is the housekeeping of files between machines: received
// files go after FileRetention, send snapshots left by a crash after a day,
// and same-machine or staged Taildrop files nobody collected after
// FileRetention. logf may be nil.
func cleanFileFolders(state string, now time.Time, logf func(string, ...any)) {
	removeOld := func(path string, age time.Duration) bool {
		info, err := os.Lstat(path)
		if err != nil || now.Sub(info.ModTime()) < age {
			return false
		}
		if err := os.RemoveAll(path); err != nil {
			if logf != nil {
				logf("housekeeping files: remove %s: %v", path, err)
			}
			return false
		}
		return true
	}
	inbox := filepath.Join(state, fileInboxFolder)
	sessions, _ := os.ReadDir(inbox)
	removed := 0
	for _, s := range sessions {
		sdir := filepath.Join(inbox, s.Name())
		messages, err := os.ReadDir(sdir)
		if err != nil {
			continue
		}
		for _, m := range messages {
			if removeOld(filepath.Join(sdir, m.Name()), protocol.FileRetention) {
				removed++
			}
		}
		if left, err := os.ReadDir(sdir); err == nil && len(left) == 0 {
			os.Remove(sdir)
		}
	}
	for folder, age := range map[string]time.Duration{
		fileOutgoingFolder: fileSnapshotMaxAge,
		fileLocalFolder:    protocol.FileRetention,
		fileTaildropFolder: protocol.FileRetention,
	} {
		entries, _ := os.ReadDir(filepath.Join(state, folder))
		for _, e := range entries {
			removeOld(filepath.Join(state, folder, e.Name()), age)
		}
	}
	if removed > 0 && logf != nil {
		logf("housekeeping files: removed %d received message folders older than %s", removed, protocol.FileRetention)
	}
}
