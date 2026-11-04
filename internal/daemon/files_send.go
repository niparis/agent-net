package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The paths a file travels by, as SendFiles and the delivery record name them.
const (
	FilePathTaildrop = "taildrop"
	FilePathGateway  = "gateway"
	FilePathLocal    = "local"
)

// FileSend is one message with files, as the session tools (T4) and the
// owner flow (T5) hand it to the daemon. Paths are full paths on this
// machine. AllowSecrets sends a text file the secret scanner flagged; only
// the owner may set it.
type FileSend struct {
	From, To        string
	Body            string
	Type            string
	ParentID        string
	IdempotencyKey  string
	Paths           []string
	AllowSecrets    bool
	ConfineSymlinks bool
}

// FileSent says how one file left this machine. Note explains a change of
// path, such as a failed Taildrop that fell back to the gateway.
type FileSent struct {
	Entry protocol.FileEntry
	Path  string
	Note  string
}

// FileSendResult is the stored message and what happened to each file.
type FileSendResult struct {
	Message protocol.Message
	Files   []FileSent
}

// SendFiles checks the files, sends the message with their manifest and then
// pushes the bytes: on this machine through the local handoff folder, over
// Taildrop when the receiver is an online peer on this machine's tailnet,
// else through the gateway in parts. A Taildrop that fails falls back to the
// gateway. The receiving daemon finds each file by its unique name and hash.
//
// The message is not queued while the gateway is unreachable: its ID names
// the files, so it must be stored first. An error after the message was
// stored comes with the result, so the caller can report the message ID.
func (d *Daemon) SendFiles(ctx context.Context, req FileSend) (FileSendResult, error) {
	var res FileSendResult
	rules, err := d.fileRules(req.From, req.AllowSecrets)
	if err != nil {
		return res, err
	}
	rules.ConfineSymlinks = req.ConfineSymlinks
	out, err := PrepareFiles(req.Paths, rules, filepath.Join(d.cfg.StateDir, fileOutgoingFolder))
	if err != nil {
		return res, err
	}
	defer out.Remove()
	return d.sendPreparedFiles(ctx, req, out)
}

// sendPreparedFiles sends the checked snapshots, including their manifest in
// the original message. Handbacks prepare before changing worker state.
func (d *Daemon) sendPreparedFiles(ctx context.Context, req FileSend, out *OutgoingFiles) (FileSendResult, error) {
	var res FileSendResult
	if req.IdempotencyKey == "" {
		req.IdempotencyKey = protocol.NewID("k")
	}
	d.touchWorker(req.From, req.To)
	if ls := d.activitySession(req.From); ls != nil {
		d.touchSession(ls, time.Now())
	}
	raw, err := d.call(withCallWait(ctx, 90*time.Second), "msg.send", protocol.MsgSend{
		Sender: req.From, Recipient: req.To, Type: req.Type, Body: req.Body,
		ParentID: req.ParentID, IdempotencyKey: req.IdempotencyKey, Files: out.Manifest(),
	})
	if errors.Is(err, errNotConnected) {
		return res, errors.New("files need a connection to the gateway; this machine is not connected now, so nothing was sent")
	}
	if err != nil {
		return res, err
	}
	var stored struct {
		Message protocol.Message `json:"message"`
	}
	if err := json.Unmarshal(raw, &stored); err != nil || stored.Message.ID == "" {
		return res, fmt.Errorf("the gateway answered msg.send without a message: %s", raw)
	}
	res.Message = stored.Message
	if len(stored.Message.Files) != len(out.Files) {
		return res, fmt.Errorf("message %s was stored without its file list: the gateway is older than this daemon; upgrade the gateway and send again", stored.Message.ID)
	}
	path, ip, receiver, note := d.fileRoute(ctx, stored.Message.To)
	if path == FilePathTaildrop && stored.Message.State == protocol.StateHeld {
		// The owner may refuse the message. Bytes on the gateway expire;
		// a Taildrop would sit in the receiver's Downloads for good.
		path, note = FilePathGateway, "the message waits for the owner's approval, so the files wait on the gateway"
	}
	var errs []error
	for _, f := range out.Files {
		sent, err := d.pushFile(ctx, stored.Message.ID, f, path, ip, receiver, note)
		res.Files = append(res.Files, sent)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", f.Entry.Name, err))
		}
	}
	for _, s := range res.Files {
		d.Logf("files sent: message=%s file=%s bytes=%d path=%s note=%q", stored.Message.ID, s.Entry.ID, s.Entry.Size, s.Path, s.Note)
	}
	return res, errors.Join(errs...)
}

// fileRules builds the path rules for a session of this machine: its project
// and worktree are the roots; agent-net's own folders are private.
func (d *Daemon) fileRules(session string, allowSecrets bool) (FileRules, error) {
	ls := d.sessionByID(session)
	if ls == nil {
		return FileRules{}, fmt.Errorf("session %s is not on this machine", session)
	}
	info := d.infoOf(ls)
	var roots []string
	for _, r := range []string{info.Repo.Root, info.Repo.Worktree} {
		if r != "" {
			roots = append(roots, r)
		}
	}
	if len(roots) == 0 && info.Cwd != "" {
		roots = append(roots, info.Cwd)
	}
	fc := d.fileConfig()
	cfgDir := d.cfg.NodeConfigDir
	if cfgDir == "" {
		cfgDir = ConfigDir()
	}
	private := []string{d.cfg.StateDir, cfgDir, d.cfg.GatewayData,
		filepath.Join(fc.Home, ".local", "state", "agent-net"),
		filepath.Join(fc.Home, ".local", "share", "agent-net"),
		filepath.Join(fc.Home, ".config", "agent-net")}
	return FileRules{Roots: roots, Home: fc.Home, Private: private, AllowSecrets: allowSecrets}, nil
}

// fileRoute picks the path for the files of a message to a session. It also
// returns the receiver's Tailscale address and node, and a note when
// Taildrop was not possible although the receiver reported an address.
func (d *Daemon) fileRoute(ctx context.Context, to string) (path, ip, receiver, note string) {
	raw, err := d.call(ctx, "query.agent", protocol.QueryAgent{SessionID: to})
	if err != nil {
		return FilePathGateway, "", "", "receiver not found: " + err.Error()
	}
	var a struct {
		Agent protocol.AgentView `json:"agent"`
	}
	if err := json.Unmarshal(raw, &a); err != nil || a.Agent.NodeID == "" {
		return FilePathGateway, "", "", "receiver has no machine"
	}
	receiver = a.Agent.NodeID
	if receiver == d.cfg.NodeID {
		return FilePathLocal, "", receiver, ""
	}
	raw, err = d.call(ctx, "query.nodes", nil)
	if err != nil {
		return FilePathGateway, "", receiver, ""
	}
	var nodes protocol.NodesInfo
	if json.Unmarshal(raw, &nodes) != nil {
		return FilePathGateway, "", receiver, ""
	}
	for _, n := range nodes.Nodes {
		if n.NodeID == receiver {
			ip = n.TailscaleIP
		}
	}
	if ip == "" {
		return FilePathGateway, "", receiver, ""
	}
	online, err := d.tailscale().peerOnline(ctx, ip)
	switch {
	case errors.Is(err, errNoTailscale):
		return FilePathGateway, "", receiver, ""
	case err != nil:
		return FilePathGateway, ip, receiver, "Taildrop not possible: " + err.Error()
	case !online:
		return FilePathGateway, ip, receiver, "Taildrop not possible: the receiver is not an online peer on this machine's tailnet"
	}
	return FilePathTaildrop, ip, receiver, ""
}

// pushFile sends one snapshot by the chosen path. A failed Taildrop falls
// back to the gateway, which keeps the bytes until the receiver is online.
func (d *Daemon) pushFile(ctx context.Context, msgID string, f OutgoingFile, path, ip, receiver, note string) (FileSent, error) {
	sent := FileSent{Entry: f.Entry, Path: path, Note: note}
	if f.Entry.Size == 0 {
		// Nothing to carry: the receiver writes an empty file from the
		// manifest alone.
		sent.Note = "empty file, nothing to send"
		return sent, nil
	}
	unique := protocol.TaildropName(msgID, f.Entry)
	switch path {
	case FilePathLocal:
		dir := filepath.Join(d.cfg.StateDir, fileLocalFolder)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return sent, err
		}
		return sent, os.Rename(f.Copy, filepath.Join(dir, unique))
	case FilePathTaildrop:
		named := filepath.Join(filepath.Dir(f.Copy), unique)
		err := os.Rename(f.Copy, named)
		if err == nil {
			err = d.tailscale().send(ctx, named, ip)
			if rerr := os.Rename(named, f.Copy); err == nil && rerr != nil {
				err = rerr
			}
			if err == nil {
				return sent, nil
			}
		}
		if ctx.Err() != nil {
			return sent, ctx.Err()
		}
		sent.Path, sent.Note = FilePathGateway, "Taildrop failed, sent through the gateway: "+err.Error()
	}
	return sent, d.uploadFile(ctx, f, unique, receiver)
}

// uploadFile sends a snapshot through the gateway in parts, named by its
// unique name so the receiver can find it with xfer.find.
func (d *Daemon) uploadFile(ctx context.Context, f OutgoingFile, unique, receiver string) error {
	if receiver == "" {
		return errors.New("the receiver's machine is unknown, so the gateway cannot hold the file for it")
	}
	file, err := os.Open(f.Copy)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = TransferUpload(ctx, d, TransferRequest{Receiver: receiver, Name: unique, Size: f.Entry.Size, SHA256: f.Entry.SHA256}, file)
	return err
}
