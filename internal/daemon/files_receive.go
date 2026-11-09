package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// fileState is the receiving side's bookkeeping of files between machines.
type fileState struct {
	mu         sync.Mutex
	ctx        context.Context   // the daemon's lifetime, set by Start
	collecting map[string]bool   // message ID -> a collector runs
	announced  map[string]string // unique file name -> message ID
	routes     map[string]string // message ID -> the path words for its delivery record
	loggedNoTS bool              // "tailscale is not installed" was logged once
	stagingMu  sync.Mutex        // one `tailscale file get` pass at a time
}

// Labels of the paths in the delivery record.
const (
	fileRouteEarlier = "earlier"
	fileRouteEmpty   = "empty"
)

var fileRouteWords = map[string]string{
	FilePathTaildrop: "Taildrop",
	FilePathGateway:  "the gateway",
	FilePathLocal:    "the local handoff on this machine",
	fileRouteEarlier: "an earlier delivery attempt",
}

// fileConfig returns the file settings with their defaults, also for a
// daemon value that was not made by New.
func (d *Daemon) fileConfig() FileConfig { return d.cfg.Files.withDefaults() }

func (d *Daemon) tailscale() tailscaleCLI {
	fc := d.fileConfig()
	return tailscaleCLI{search: fc.TailscalePATH, fallback: fc.TailscaleFallback}
}

// receiveFiles starts collecting the files of a message that carries a
// manifest. A worker notice has a shorter wait and can be delivered with
// missing files; other messages wait for every file. A second push while a
// collector runs is ignored: the collector delivers once.
func (d *Daemon) receiveFiles(m protocol.Message) {
	fs := &d.files
	fs.mu.Lock()
	if fs.collecting == nil {
		fs.collecting = map[string]bool{}
	}
	if fs.collecting[m.ID] {
		fs.mu.Unlock()
		return
	}
	fs.collecting[m.ID] = true
	ctx := fs.ctx
	fs.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		defer func() {
			fs.mu.Lock()
			delete(fs.collecting, m.ID)
			fs.mu.Unlock()
		}()
		d.collectThenDeliver(ctx, m)
	}()
}

func (d *Daemon) collectThenDeliver(ctx context.Context, m protocol.Message) {
	if err := protocol.ValidateFiles(m.Files); err != nil {
		d.sendUpdate(ctx, m.ID, protocol.StateFailed, "the file list is not valid: "+err.Error())
		return
	}
	if !safePathElement(m.To) || !safePathElement(m.ID) {
		d.sendUpdate(ctx, m.ID, protocol.StateFailed, "the message or session ID cannot name an inbox folder")
		return
	}
	// A message this daemon already wrote, or started to write, goes the
	// usual way: startDelivery reports the duplicate or the doubt.
	if state, _, err := d.st.readDeliveryStateCtx(ctx, m.ID); err == nil &&
		(state == deliveryDelivered || state == deliveryInjecting || state == deliveryUnconfirmed) {
		d.enqueue(m.To, func() { d.onDeliver(ctx, m) })
		return
	}
	dir := d.fileInboxDir(m.To, m.ID)
	got, err := d.collectFiles(ctx, m, dir)
	if ctx.Err() != nil {
		return // the daemon stops; the gateway pushes the message again later
	}
	if err != nil && !isWorkerFileNotice(m) {
		d.Logf("files: message=%s not delivered: %v", m.ID, err)
		d.sendUpdate(ctx, m.ID, protocol.StateFailed, err.Error())
		return
	}
	var arrived []protocol.FileEntry
	var missing []string
	for _, f := range m.Files {
		if _, ok := got[f.ID]; ok {
			arrived = append(arrived, f)
		} else {
			missing = append(missing, f.Name)
		}
	}
	if err != nil {
		d.Logf("files: worker notice message=%s delivering without files %q: %v", m.ID, strings.Join(missing, ", "), err)
		m.Body += "\n\nfiles missing: " + strings.Join(missing, ", ") + ", transfer did not finish"
	} else {
		d.Logf("files: message=%s all %d files in %s (%s)", m.ID, len(m.Files), dir, fileRouteDetail(got))
	}
	if len(arrived) > 0 {
		m.Body += fileNote(dir, arrived)
	}
	if len(arrived) > 0 {
		d.files.mu.Lock()
		if d.files.routes == nil {
			d.files.routes = map[string]string{}
		}
		d.files.routes[m.ID] = fileRouteDetail(got)
		d.files.mu.Unlock()
	}
	d.enqueue(m.To, func() { d.onDeliver(ctx, m) })
}

// withFileRoute adds the path the files took to a delivery detail, once.
// It goes at the end so the gateway's retry rules, which read the start of
// the detail, still see the harness's words.
func (d *Daemon) withFileRoute(id, detail string) string {
	d.files.mu.Lock()
	route, ok := d.files.routes[id]
	delete(d.files.routes, id)
	d.files.mu.Unlock()
	switch {
	case !ok || route == "":
		return detail
	case detail == "":
		return route
	}
	return detail + "; " + route
}

// fileRouteDetail says in words which paths brought the files.
func fileRouteDetail(got map[string]string) string {
	seen := map[string]bool{}
	var words []string
	for _, route := range got {
		w, ok := fileRouteWords[route]
		if ok && !seen[w] {
			seen[w] = true
			words = append(words, w)
		}
	}
	if len(words) == 0 {
		return "files: empty, nothing to carry"
	}
	sort.Strings(words)
	return "files via " + strings.Join(words, " and ")
}

// fileNote is what the receiving agent reads after the message body.
func fileNote(dir string, files []protocol.FileEntry) string {
	var b strings.Builder
	if len(files) == 1 {
		fmt.Fprintf(&b, "\n\n[agent-net] This message came with 1 file. It is in %s:\n", dir)
	} else {
		fmt.Fprintf(&b, "\n\n[agent-net] This message came with %d files. They are in %s:\n", len(files), dir)
	}
	for _, f := range files {
		fmt.Fprintf(&b, "- %s (%s)\n", f.Name, humanBytes(f.Size))
	}
	fmt.Fprintf(&b, "agent-net deletes them after %d days.", int(protocol.FileRetention/(24*time.Hour)))
	return b.String()
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}

// safePathElement tells whether an ID from the gateway can name a folder.
func safePathElement(s string) bool {
	if s == "" || s == "." || s == ".." || len(s) > 128 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.') {
			return false
		}
	}
	return true
}

func (d *Daemon) fileInboxDir(session, message string) string {
	return filepath.Join(d.cfg.StateDir, fileInboxFolder, session, message)
}

// fileStamp is what a pass remembers of a file that failed its hash, so a
// file still being written is not refused for being half there.
type fileStamp struct {
	size  int64
	mtime time.Time
}

// A worker's final report must reach its caller even if its files do not.
// The stored timestamp also bounds notices replayed after a daemon restart.
const workerNoticeFileWait = 2 * time.Minute

func isWorkerFileNotice(m protocol.Message) bool {
	if !strings.HasPrefix(m.Body, "[agent-net worker notice]\nstatus: ") {
		return false
	}
	// The gateway keeps messages longer than it keeps ended worker sessions.
	// Once that session is deleted, its sender fields are all empty on replay.
	return m.FromWorker || (m.FromHarness == "" && m.FromName == "")
}

// fileWaitDeadline takes the clock as an argument so old and replayed notices
// can be checked without sleeping in tests.
func fileWaitDeadline(m protocol.Message, fc FileConfig, now time.Time) time.Time {
	deadline := m.ExpiresAt
	if deadline.IsZero() {
		deadline = now.Add(defaultFileWaitCap)
	}
	if fc.Wait > 0 && now.Add(fc.Wait).Before(deadline) {
		deadline = now.Add(fc.Wait)
	}
	if isWorkerFileNotice(m) {
		stored := m.CreatedAt
		if stored.IsZero() {
			stored = now
		}
		if limit := stored.Add(workerNoticeFileWait); limit.Before(deadline) {
			deadline = limit
		}
	}
	return deadline
}

// collectFiles waits until every file of the message is in dir, a file
// fails its hash, or the wait ends. It returns the path each file took.
func (d *Daemon) collectFiles(ctx context.Context, m protocol.Message, dir string) (map[string]string, error) {
	fc := d.fileConfig()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	d.files.mu.Lock()
	if d.files.announced == nil {
		d.files.announced = map[string]string{}
	}
	for _, f := range m.Files {
		d.files.announced[protocol.TaildropName(m.ID, f)] = m.ID
	}
	d.files.mu.Unlock()
	defer func() {
		d.files.mu.Lock()
		for _, f := range m.Files {
			delete(d.files.announced, protocol.TaildropName(m.ID, f))
		}
		d.files.mu.Unlock()
	}()

	deadline := fileWaitDeadline(m, fc, time.Now())
	if isWorkerFileNotice(m) {
		// Bound a stalled gateway or Taildrop call too, not just the polling.
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, deadline)
		defer cancel()
	}
	c := &fileCollector{d: d, m: m, dir: dir, got: map[string]string{}, suspects: map[string]fileStamp{}, gateway: true, deadline: deadline}
	interval := fc.Poll
	reported := ""
	for {
		progress, bad := c.pass(ctx)
		if len(c.got) == len(m.Files) {
			return c.got, nil
		}
		if len(bad) > 0 {
			return c.got, fmt.Errorf("%s arrived, but its sha256 does not match the file list; the message was not delivered", strings.Join(bad, ", "))
		}
		detail := fmt.Sprintf("waiting for files: %d of %d in", len(c.got), len(m.Files))
		if c.blocked != "" {
			detail += " (" + c.blocked + ")"
		}
		if detail != reported {
			reported = detail
			if ctx.Err() == nil {
				d.sendUpdate(ctx, m.ID, protocol.StateDispatched, detail)
			}
			if c.blocked != "" {
				d.Logf("files: message=%s %s", m.ID, c.blocked)
			}
		}
		if !time.Now().Before(deadline) {
			return c.got, fmt.Errorf("files did not arrive before the wait ended: %s", strings.Join(c.missingNames(), ", "))
		}
		if progress {
			interval = fc.Poll
		} else {
			interval = min(interval*2, fc.PollMax)
		}
		wait := min(interval, time.Until(deadline))
		select {
		case <-ctx.Done():
			return c.got, fmt.Errorf("file wait ended with %s missing: %w", strings.Join(c.missingNames(), ", "), ctx.Err())
		case <-time.After(max(wait, 0)):
		}
	}
}

// fileCollector looks for the missing files of one message in every place
// they can arrive.
type fileCollector struct {
	d        *Daemon
	m        protocol.Message
	dir      string
	got      map[string]string    // file ID -> path it took
	suspects map[string]fileStamp // file ID -> a copy that failed its hash once
	gateway  bool                 // false after the gateway did not know xfer.find
	blocked  string               // why a place cannot be read
	deadline time.Time            // bounded when the collector must stop waiting
	opens    map[string]*fileOpen // source path -> its open helper, kept while it blocks
}

// fileOpen is one helper goroutine that opens and copies an arrived file. It
// stays in the collector while a blocked syscall does not return, so a new
// pass waits on the same call instead of starting another.
type fileOpen struct{ done chan error }

func (c *fileCollector) missing() []protocol.FileEntry {
	var out []protocol.FileEntry
	for _, f := range c.m.Files {
		if _, ok := c.got[f.ID]; !ok {
			out = append(out, f)
		}
	}
	return out
}

func (c *fileCollector) missingNames() []string {
	var out []string
	for _, f := range c.missing() {
		out = append(out, f.Name)
	}
	return out
}

// pass looks once in every place. It reports whether a file came in, and the
// names of files whose bytes failed the hash twice in a row.
func (c *fileCollector) pass(ctx context.Context) (progress bool, bad []string) {
	d, fc := c.d, c.d.fileConfig()
	c.blocked = ""
	took := func(f protocol.FileEntry, route string) {
		c.got[f.ID] = route
		delete(c.suspects, f.ID)
		progress = true
	}
	// Already in the inbox: a restart, or the gateway pushed the message again.
	for _, f := range c.missing() {
		if f.Size == 0 {
			if err := writeEmptyFile(filepath.Join(c.dir, f.Name), f); err == nil {
				took(f, fileRouteEmpty)
			}
			continue
		}
		if verifyFile(filepath.Join(c.dir, f.Name), f) == nil {
			took(f, fileRouteEarlier)
		}
	}
	// The local handoff, for a sender on this machine.
	for _, f := range c.missing() {
		src := filepath.Join(d.cfg.StateDir, fileLocalFolder, protocol.TaildropName(c.m.ID, f))
		if c.tryTake(ctx, src, f) == nil {
			took(f, FilePathLocal)
		}
	}
	// Taildrop. On Linux the files wait for `tailscale file get`; the owner
	// may also have collected them into Downloads. A Mac daemon never looks
	// in Downloads: a privacy check or an iCloud-backed file there can block
	// an open in the kernel forever (decision 0125). A sender on a machine
	// with a known OS other than darwin uses the gateway for a Mac receiver.
	var places []string
	if len(c.missing()) > 0 && fc.Layout != "darwin" {
		d.taildropCollect(ctx)
		places = append(places, filepath.Join(d.cfg.StateDir, fileTaildropFolder), fc.Downloads)
	}
	var mismatched []protocol.FileEntry
	for _, place := range places {
		for _, f := range c.missing() {
			src := filepath.Join(place, protocol.TaildropName(c.m.ID, f))
			err := c.tryTake(ctx, src, f)
			switch {
			case err == nil:
				took(f, FilePathTaildrop)
			case errors.Is(err, errFileMismatch):
				mismatched = append(mismatched, f)
				if place != fc.Downloads {
					// A bad copy in the staging folder goes to Downloads,
					// like any file this daemon does not take.
					d.moveToDownloads(src)
				}
			}
		}
	}
	// The gateway in parts.
	if missing := c.missing(); len(missing) > 0 && c.gateway {
		if c.fromGateway(ctx, missing) {
			progress = true
		}
	}
	for _, f := range mismatched {
		if _, ok := c.got[f.ID]; ok {
			continue
		}
		bad = append(bad, f.Name)
	}
	return progress, bad
}

var errFileMismatch = errors.New("the file does not match its size or sha256")

// errFileOpenTimeout says an open did not return in time. The helper that ran
// it may still be blocked in the kernel.
var errFileOpenTimeout = errors.New("the file open did not return in time")

// fileOpenTimeout bounds one open of an arrived file. A blocked open repeats
// until the collector's deadline, which is usually shorter.
const fileOpenTimeout = 5 * time.Second

// The stat and open of a file that arrived are variables so a test can make
// them block, as a macOS privacy check or an iCloud-backed file can.
var (
	takeFileLstat = os.Lstat
	takeFileOpen  = os.Open
)

// tryTake takes one announced copy into the inbox. A copy that fails its
// hash is left where it is; it counts as bad only when the same copy (same
// size and time) fails twice, so a file still being written is not refused.
// It returns os.ErrNotExist when there is nothing to take.
func (c *fileCollector) tryTake(ctx context.Context, src string, f protocol.FileEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := takeFileLstat(src)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			c.blocked = fmt.Sprintf("cannot read %s: %v", filepath.Dir(src), errors.Unwrap(err))
		}
		return os.ErrNotExist
	}
	if !info.Mode().IsRegular() {
		return os.ErrNotExist // never follow a link someone left under an announced name
	}
	if info.Size() < f.Size {
		return os.ErrNotExist // still arriving
	}
	err = c.takeWithin(ctx, src, f)
	if !errors.Is(err, errFileMismatch) {
		return err
	}
	stamp := fileStamp{size: info.Size(), mtime: info.ModTime()}
	if prev, ok := c.suspects[f.ID]; ok && prev == stamp {
		return errFileMismatch
	}
	c.suspects[f.ID] = stamp
	return os.ErrNotExist
}

// takeWithin runs the open and copy in a helper goroutine. A blocked syscall
// ignores the context, so the collector stops waiting at its deadline or when
// the daemon stops. The helper is not part of d.wg, so Stop does not wait for
// a syscall that may never return. A later pass waits on the same helper
// instead of starting another.
func (c *fileCollector) takeWithin(ctx context.Context, src string, f protocol.FileEntry) error {
	timeout := fileOpenTimeout
	if !c.deadline.IsZero() {
		if left := time.Until(c.deadline); left < timeout {
			timeout = left
		}
	}
	if timeout <= 0 {
		return errFileOpenTimeout
	}
	if c.opens == nil {
		c.opens = map[string]*fileOpen{}
	}
	call := c.opens[src]
	if call == nil {
		call = &fileOpen{done: make(chan error, 1)}
		c.opens[src] = call
		go func() { call.done <- takeFile(src, filepath.Join(c.dir, f.Name), f) }()
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-call.done:
		delete(c.opens, src)
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return errFileOpenTimeout
	}
}

// takeFile copies src into dst while hashing it, keeps the copy only when
// size and hash match the manifest, then removes src.
func takeFile(src, dst string, f protocol.FileEntry) error {
	in, err := takeFileOpen(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".part-"+f.ID+"-")
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(in, f.Size+1))
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil && (n != f.Size || hex.EncodeToString(h.Sum(nil)) != f.SHA256) {
		err = errFileMismatch
	}
	if err == nil {
		err = os.Rename(tmp.Name(), dst)
	}
	if err != nil {
		os.Remove(tmp.Name())
		return err
	}
	in.Close()
	return os.Remove(src)
}

// verifyFile checks that path holds exactly the manifest's bytes.
func verifyFile(path string, f protocol.FileEntry) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != f.Size {
		return errFileMismatch
	}
	in, err := os.Open(path)
	if err != nil {
		return err
	}
	defer in.Close()
	h := sha256.New()
	if _, err := io.Copy(h, in); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != f.SHA256 {
		return errFileMismatch
	}
	return nil
}

func writeEmptyFile(path string, f protocol.FileEntry) error {
	if sum := sha256.Sum256(nil); hex.EncodeToString(sum[:]) != f.SHA256 {
		return errFileMismatch
	}
	out, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	return out.Close()
}

// fromGateway looks for the missing files among the finished uploads
// addressed to this machine, downloads each one whose hash matches the
// manifest, and confirms it so the gateway frees the space.
func (c *fileCollector) fromGateway(ctx context.Context, missing []protocol.FileEntry) bool {
	d := c.d
	byName := map[string]protocol.FileEntry{}
	var names []string
	for _, f := range missing {
		n := protocol.TaildropName(c.m.ID, f)
		byName[n] = f
		names = append(names, n)
	}
	raw, err := d.call(ctx, "xfer.find", protocol.TransferFind{Names: names})
	if err != nil {
		var re RemoteError
		if errors.As(err, &re) && strings.Contains(string(re), "unknown frame type") {
			c.gateway = false
			d.Logf("files: the gateway does not know xfer.find; files of message %s can only come over Taildrop", c.m.ID)
		}
		return false
	}
	var found protocol.TransferFound
	if json.Unmarshal(raw, &found) != nil {
		return false
	}
	progress := false
	for _, up := range found.Uploads {
		f, ok := byName[up.Name]
		if !ok || !up.Finished || up.Size != f.Size || !strings.EqualFold(up.SHA256, f.SHA256) {
			continue
		}
		if _, done := c.got[f.ID]; done {
			continue
		}
		if err := c.download(ctx, up.UploadID, f); err != nil {
			d.Logf("files: message=%s file=%s download from the gateway: %v", c.m.ID, f.ID, err)
			continue
		}
		c.got[f.ID] = FilePathGateway
		progress = true
		if err := TransferConfirm(ctx, d, up.UploadID); err != nil {
			d.Logf("files: confirm upload %s: %v", up.UploadID, err)
		}
	}
	return progress
}

func (c *fileCollector) download(ctx context.Context, uploadID string, f protocol.FileEntry) error {
	tmp, err := os.CreateTemp(c.dir, ".part-"+f.ID+"-")
	if err != nil {
		return err
	}
	err = TransferDownload(ctx, c.d, uploadID, tmp)
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = verifyFile(tmp.Name(), f)
	}
	if err == nil {
		err = os.Rename(tmp.Name(), filepath.Join(c.dir, f.Name))
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}

// taildropCollect runs `tailscale file get` into the staging folder (Linux),
// then moves every staged file nobody announced to Downloads, so the owner's
// own Taildrops still arrive where the owner expects them.
func (d *Daemon) taildropCollect(ctx context.Context) {
	d.files.stagingMu.Lock()
	defer d.files.stagingMu.Unlock()
	staging := filepath.Join(d.cfg.StateDir, fileTaildropFolder)
	if err := os.MkdirAll(staging, 0o700); err != nil {
		d.Logf("files: staging folder: %v", err)
		return
	}
	if err := d.tailscale().get(ctx, staging); err != nil {
		d.files.mu.Lock()
		quiet := errors.Is(err, errNoTailscale) && d.files.loggedNoTS
		if errors.Is(err, errNoTailscale) {
			d.files.loggedNoTS = true
		}
		d.files.mu.Unlock()
		if !quiet {
			d.Logf("files: tailscale file get: %v", err)
		}
	}
	entries, err := os.ReadDir(staging)
	if err != nil {
		return
	}
	for _, e := range entries {
		d.files.mu.Lock()
		_, ok := d.files.announced[e.Name()]
		d.files.mu.Unlock()
		if ok || !e.Type().IsRegular() {
			continue
		}
		d.moveToDownloads(filepath.Join(staging, e.Name()))
	}
}

// moveToDownloads moves a file to Downloads under a free name ("name (1).ext"
// when the name is taken). It never overwrites a file.
func (d *Daemon) moveToDownloads(src string) {
	downloads := d.fileConfig().Downloads
	if err := os.MkdirAll(downloads, 0o700); err != nil {
		d.Logf("files: Downloads folder: %v", err)
		return
	}
	base := filepath.Base(src)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	for i := 0; i < 1000; i++ {
		name := base
		if i > 0 {
			name = fmt.Sprintf("%s (%d)%s", stem, i, ext)
		}
		dst := filepath.Join(downloads, name)
		err := os.Link(src, dst)
		if err == nil {
			os.Remove(src)
			return
		}
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if errors.Is(err, syscall.EXDEV) || errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.ENOTSUP) {
			if err = copyNew(src, dst); err == nil {
				os.Remove(src)
				return
			}
			if errors.Is(err, os.ErrExist) {
				continue
			}
		}
		d.Logf("files: move %s to Downloads: %v", base, err)
		return
	}
	d.Logf("files: no free name for %s in Downloads", base)
}

// copyNew copies src to a new file dst; it fails when dst exists.
func copyNew(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(dst)
	}
	return err
}

// filesHousekeepingLoop removes received files after seven days, and the
// leftovers of interrupted sends.
var (
	filesCleanFirstDelay = time.Minute
	filesCleanEvery      = time.Hour
)

func (d *Daemon) filesHousekeepingLoop(ctx context.Context) {
	runEvery(ctx, filesCleanFirstDelay, filesCleanEvery, func() {
		cleanFileFolders(d.cfg.StateDir, time.Now(), d.Logf)
	})
}
