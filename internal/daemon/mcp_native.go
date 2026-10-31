package daemon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
)

// This file edits the native MCP config files of this machine on the owner's
// word: it removes one imported entry, and restores it on undo. The file is
// hashed at plan time and edited only if it still has that hash. The original
// is saved first in a private backup. Only the entry's bytes are cut. Undo puts
// the original back only if the file is still exactly what the edit wrote.

const mcpBackupDir = "mcp-backups"

// nativeLockWait is how long an edit waits for Claude Code's config lock.
var nativeLockWait = 2 * time.Second

var backupIDRE = regexp.MustCompile(`^[0-9a-f]{64}/[0-9]{8}T[0-9]{6}\.[0-9]{9}Z-[0-9]+$`)

// nativeMeta is saved beside a backup. It lets undo find the file without
// trusting the request.
type nativeMeta struct {
	Harness  string `json:"harness"`
	Scope    string `json:"scope"`
	Root     string `json:"root,omitempty"`
	Location string `json:"location"`
	Name     string `json:"name"`
	PreHash  string `json:"pre_hash"`
	PostHash string `json:"post_hash"`
}

func sumHex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

type nativeFile struct {
	base, path string
	data       []byte
	mode       os.FileMode
	root       string
}

func (d *Daemon) nativeOptions() inventory.Options {
	return inventory.Options{Home: d.cfg.InventoryHome, CodexHome: d.cfg.CodexHome}
}

// observedRoot returns the project root of an entry the last scan saw.
func (d *Daemon) observedRoot(p protocol.MCPNativeEditRequest) (string, bool) {
	d.toolsMu.Lock()
	defer d.toolsMu.Unlock()
	for _, v := range d.toolsReport.Items {
		if v.Kind == protocol.InventoryMCP && v.Harness == p.Harness && v.Scope == p.Scope && v.Project == p.Project && v.Location == p.Location && v.Name == p.Name {
			return v.ProjectRoot, true
		}
	}
	return "", false
}

func (d *Daemon) openNative(harness, scope, root, location string) (nativeFile, string) {
	base, path, ok := inventory.NativePath(d.nativeOptions(), harness, scope, root, location)
	if !ok {
		return nativeFile{}, "not a known native MCP source"
	}
	data, mode, err := inventory.ReadNativeFile(base, path)
	if err != nil {
		return nativeFile{}, "the file cannot be read: " + err.Error()
	}
	return nativeFile{base: base, path: path, data: data, mode: mode, root: root}, ""
}

// committed reports whether Git tracks the file in a repository.
func committed(path string) bool {
	dir := filepath.Dir(path)
	top, err := projectGit(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(strings.TrimSpace(string(top)), path)
	if err != nil {
		return false
	}
	tracked, err := projectGit(strings.TrimSpace(string(top)), "ls-files", "-z", "--", rel)
	return err == nil && len(tracked) > 0
}

const committedReason = "committed in the repo: remove it with a commit"

// nativeEdit runs one operation. It never returns file content.
func (d *Daemon) nativeEdit(p protocol.MCPNativeEditRequest) protocol.MCPNativeEditResult {
	if d.cfg.InventoryHome == "" || d.cfg.StateDir == "" {
		return protocol.MCPNativeEditResult{Conflict: "inventory scanner is disabled"}
	}
	switch p.Op {
	case protocol.MCPNativePlan, protocol.MCPNativeApply:
		root, ok := d.observedRoot(p)
		if !ok {
			return protocol.MCPNativeEditResult{Conflict: "the native entry is no longer observed"}
		}
		f, why := d.openNative(p.Harness, p.Scope, root, p.Location)
		if why != "" {
			return protocol.MCPNativeEditResult{Conflict: why}
		}
		res := protocol.MCPNativeEditResult{FileHash: sumHex(f.data)}
		if committed(f.path) {
			res.Committed, res.Conflict = true, committedReason
			return res
		}
		if _, err := inventory.RemoveNativeEntry(d.nativeOptions(), p.Harness, p.Scope, root, p.Location, p.Name, f.data); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				res.Conflict = "the native entry is no longer in the file"
			} else {
				res.Conflict = err.Error()
			}
			return res
		}
		if p.Op == protocol.MCPNativePlan {
			return res
		}
		return d.applyNative(p, f)
	case protocol.MCPNativeUndo:
		return d.undoNative(p)
	}
	return protocol.MCPNativeEditResult{Conflict: "unknown operation"}
}

// withConfigLock takes the lock Claude Code uses for ~/.claude.json, so an edit
// does not race a running Claude. Other files need no lock.
func withConfigLock(path string, fn func() string) string {
	if filepath.Base(path) != ".claude.json" {
		return fn()
	}
	lock := path + ".lock"
	deadline := time.Now().Add(nativeLockWait)
	for {
		err := os.Mkdir(lock, 0o700)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrExist) {
			return "cannot take the Claude config lock"
		}
		if time.Now().After(deadline) {
			return "the Claude config lock is held; try again"
		}
		time.Sleep(20 * time.Millisecond)
	}
	defer os.Remove(lock)
	return fn()
}

func (d *Daemon) applyNative(p protocol.MCPNativeEditRequest, f nativeFile) protocol.MCPNativeEditResult {
	res := protocol.MCPNativeEditResult{FileHash: sumHex(f.data)}
	fail := func(why string) protocol.MCPNativeEditResult { res.Conflict = why; return res }
	if res.FileHash != p.FileHash {
		return fail("changed since plan")
	}
	itemSum := sha256.Sum256([]byte(f.path + "\x00" + p.Name))
	item := hex.EncodeToString(itemSum[:])
	why := withConfigLock(f.path, func() string {
		// Read again under the lock: the edit must start from the planned bytes.
		cur, mode, err := inventory.ReadNativeFile(f.base, f.path)
		if err != nil || sumHex(cur) != p.FileHash {
			return "changed since plan"
		}
		out, err := inventory.RemoveNativeEntry(d.nativeOptions(), p.Harness, p.Scope, f.root, p.Location, p.Name, cur)
		if err != nil {
			return err.Error()
		}
		j, err := newSkillJournal(d.cfg.StateDir, skillJournalRecord{Operation: "mcp-edit", Target: f.path, BeforeHash: p.FileHash, AfterHash: sumHex(out), Step: "prepared"})
		if err != nil {
			return "cannot write the operation journal"
		}
		retain := false
		defer func() {
			if !retain {
				_ = j.finish()
			}
		}()
		id, stamp, err := d.writeNativeBackup(item, cur, nativeMeta{Harness: p.Harness, Scope: p.Scope, Root: f.root, Location: p.Location, Name: p.Name, PreHash: p.FileHash, PostHash: sumHex(out)})
		if err != nil {
			return "cannot save a backup; the file was not changed"
		}
		j.record.Backup = filepath.Join(stamp, "preimage")
		if err = j.advance("backed_up"); err != nil {
			retain = true
			return "operation interrupted; the file was not changed"
		}
		if why := replaceNative(f.path, mode, out, p.FileHash); why != "" {
			return why
		}
		retain = true
		if err = j.advance("edited"); err != nil {
			return "operation interrupted"
		}
		got, _, err := inventory.ReadNativeFile(f.base, f.path)
		if err != nil || !bytes.Equal(got, out) {
			return "changed after the edit; the file was kept as it is"
		}
		if _, _, _, err = inventory.LoadNativeMCP(d.nativeOptions(), p.Harness, p.Scope, f.root, p.Location, p.Name); !errors.Is(err, os.ErrNotExist) {
			return "the entry is still in the file after the edit"
		}
		retain = false
		res.PostHash, res.BackupID, res.FileHash = sumHex(out), id, sumHex(out)
		d.pruneBackups(mcpBackupDir)
		return ""
	})
	if why != "" {
		return fail(why)
	}
	d.wakeTools()
	return res
}

// replaceNative writes data to path through a temporary file in the same
// folder, keeping the file mode. It renames only if the file still has the
// hash it had when the edit started.
func replaceNative(path string, mode os.FileMode, data []byte, want string) string {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".agent-net-*")
	if err != nil {
		return "cannot write next to the file"
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err = tmp.Chmod(mode); err == nil {
		_, err = tmp.Write(data)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if e := tmp.Close(); err == nil {
		err = e
	}
	if err != nil {
		return "cannot write next to the file"
	}
	cur, err := os.ReadFile(path)
	if err != nil || sumHex(cur) != want {
		return "changed since plan"
	}
	if os.Rename(name, path) != nil {
		return "cannot replace the file"
	}
	return ""
}

// writeNativeBackup saves the original file under the private backup folder.
// It returns the backup ID and the stable folder path.
func (d *Daemon) writeNativeBackup(item string, data []byte, meta nativeMeta) (id, stamp string, err error) {
	root := filepath.Join(d.cfg.StateDir, mcpBackupDir)
	dir := filepath.Join(root, item)
	for _, p := range []string{root, dir} {
		if err = os.MkdirAll(p, 0o700); err != nil {
			return "", "", err
		}
		if err = os.Chmod(p, 0o700); err != nil {
			return "", "", err
		}
	}
	stamp, err = os.MkdirTemp(dir, time.Now().UTC().Format("20060102T150405.000000000Z")+"-")
	if err != nil {
		return "", "", err
	}
	fail := func(e error) (string, string, error) { _ = os.RemoveAll(stamp); return "", "", e }
	if err = os.Chmod(stamp, 0o700); err != nil {
		return fail(err)
	}
	if err = os.WriteFile(filepath.Join(stamp, "preimage"), data, 0o600); err != nil {
		return fail(err)
	}
	if err = writeNativeMeta(stamp, meta); err != nil {
		return fail(err)
	}
	return item + "/" + filepath.Base(stamp), stamp, nil
}

func writeNativeMeta(stamp string, meta nativeMeta) error {
	b, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(stamp, "meta.json"), b, 0o600)
}

func (d *Daemon) undoNative(p protocol.MCPNativeEditRequest) protocol.MCPNativeEditResult {
	fail := func(why string) protocol.MCPNativeEditResult { return protocol.MCPNativeEditResult{Conflict: why} }
	if !backupIDRE.MatchString(p.BackupID) {
		return fail("invalid backup")
	}
	stamp := filepath.Join(d.cfg.StateDir, mcpBackupDir, filepath.FromSlash(p.BackupID))
	raw, err := os.ReadFile(filepath.Join(stamp, "meta.json"))
	var meta nativeMeta
	if err != nil || json.Unmarshal(raw, &meta) != nil {
		return fail("the local backup is gone")
	}
	pre, err := os.ReadFile(filepath.Join(stamp, "preimage"))
	if err != nil || sumHex(pre) != meta.PreHash {
		return fail("the local backup does not match")
	}
	if meta.PostHash != p.PostHash || meta.Name != p.Name {
		return fail("the backup is not the one this edit made")
	}
	f, why := d.openNative(meta.Harness, meta.Scope, meta.Root, meta.Location)
	if why != "" {
		return fail(why)
	}
	if sumHex(f.data) != meta.PostHash {
		return protocol.MCPNativeEditResult{Conflict: "changed since apply", FileHash: sumHex(f.data)}
	}
	var res protocol.MCPNativeEditResult
	why = withConfigLock(f.path, func() string {
		cur, mode, err := inventory.ReadNativeFile(f.base, f.path)
		if err != nil || sumHex(cur) != meta.PostHash {
			return "changed since apply"
		}
		j, err := newSkillJournal(d.cfg.StateDir, skillJournalRecord{Operation: "mcp-edit", Target: f.path, BeforeHash: meta.PostHash, AfterHash: meta.PreHash, Backup: filepath.Join(stamp, "preimage"), Step: "prepared"})
		if err != nil {
			return "cannot write the operation journal"
		}
		defer func() { _ = j.finish() }()
		if why := replaceNative(f.path, mode, pre, meta.PostHash); why != "" {
			if why == "changed since plan" {
				why = "changed since apply"
			}
			return why
		}
		got, _, err := inventory.ReadNativeFile(f.base, f.path)
		if err != nil || !bytes.Equal(got, pre) {
			return "changed after the restore; the file was kept as it is"
		}
		res.FileHash = meta.PreHash
		return ""
	})
	if why != "" {
		return protocol.MCPNativeEditResult{Conflict: why}
	}
	d.wakeTools()
	return res
}

func (d *Daemon) onMCPNativeEdit(ctx context.Context, l *link, h protocol.Header, raw []byte) {
	var p protocol.MCPNativeEditRequest
	var result protocol.MCPNativeEditResult
	err := json.Unmarshal(raw, &p)
	if err == nil {
		result = d.nativeEdit(p)
	}
	if b, e := protocol.EncodeResult(h.ID, result, err); e == nil {
		_ = l.write(ctx, b)
	}
}
