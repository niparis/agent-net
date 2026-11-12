package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/niparis/agent-net/internal/protocol"
)

// localMessageFile reads a page of a file that came with a message. Harnesses
// with no file tools, such as the CTO, read attachments this way. The reader
// is the bound session and must be the receiver: the daemon reads only that
// session's own inbox folder for that message, and only a file the message's
// manifest names.
func (d *Daemon) localMessageFile(ctx context.Context, lc *localConn, raw json.RawMessage) (any, error) {
	sid, err := lc.boundID()
	if err != nil {
		return nil, err
	}
	var req protocol.MessageFileRequest
	if err := json.Unmarshal(raw, &req); err != nil || req.MessageID == "" {
		return nil, errors.New("message_file needs message_id and file")
	}
	if req.MaxBytes == 0 {
		req.MaxBytes = protocol.MessageFileDefaultBytes
	}
	if req.MaxBytes < protocol.MessageFileMinBytes || req.MaxBytes > protocol.MessageFileMaxBytes {
		return nil, fmt.Errorf("max_bytes must be between %d and %d", protocol.MessageFileMinBytes, protocol.MessageFileMaxBytes)
	}
	if req.Offset < 0 {
		return nil, errors.New("offset cannot be negative")
	}
	data, err := d.call(ctx, "query.message", protocol.QueryMessage{MessageID: req.MessageID})
	if err != nil {
		return nil, err
	}
	var result protocol.MessageResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	m := result.Message
	if m.ID == "" {
		return nil, fmt.Errorf("unknown message %q", req.MessageID)
	}
	if m.From != sid && m.To != sid {
		return nil, errors.New("message is not in this session's conversations")
	}
	if m.To != sid {
		return nil, errors.New("only the receiver has the files of a message: this session is not the receiver")
	}
	var entry *protocol.FileEntry
	for i := range m.Files {
		if m.Files[i].Name == req.File {
			entry = &m.Files[i]
			break
		}
	}
	if entry == nil {
		return nil, fmt.Errorf("the message has no file named %q", req.File)
	}
	if !safePathElement(sid) || !safePathElement(m.ID) || protocol.CheckFileName(entry.Name) != nil {
		return nil, errors.New("this message cannot name a safe inbox path")
	}
	return readInboxFile(d.fileInboxDir(sid, m.ID), entry.Name, req.Offset, req.MaxBytes)
}

// readInboxFile reads one page of dir/name. The file must be a regular file
// that the resolved path still keeps inside dir: a link the sender could not
// plant, but a local process might, never leads out of the inbox.
func readInboxFile(dir, name string, offset int64, maxBytes int) (protocol.MessageFileResult, error) {
	path := filepath.Join(dir, name)
	info, err := os.Lstat(path)
	if err != nil {
		return protocol.MessageFileResult{}, fmt.Errorf("the file %q is not in the inbox: it may not have arrived, or it was deleted after %d days", name, int(protocol.FileRetention.Hours()/24))
	}
	if !info.Mode().IsRegular() {
		return protocol.MessageFileResult{}, fmt.Errorf("%q is not a regular file inside the message's inbox folder", name)
	}
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return protocol.MessageFileResult{}, err
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil || filepath.Dir(real) != realDir {
		return protocol.MessageFileResult{}, fmt.Errorf("%q is not a regular file inside the message's inbox folder", name)
	}
	f, err := os.Open(real)
	if err != nil {
		return protocol.MessageFileResult{}, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return protocol.MessageFileResult{}, err
	}
	if !fi.Mode().IsRegular() || !os.SameFile(info, fi) {
		return protocol.MessageFileResult{}, fmt.Errorf("%q changed while it was opened", name)
	}
	out := protocol.MessageFileResult{Name: name, Size: fi.Size(), Type: "binary"}
	sniff := make([]byte, fileTextSniffBytes)
	n, err := io.ReadFull(f, sniff)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return protocol.MessageFileResult{}, err
	}
	if !looksLikeText(sniff[:n], fi.Size()) {
		return out, nil
	}
	out.Type = "text"
	if offset > fi.Size() {
		return protocol.MessageFileResult{}, fmt.Errorf("offset %d is past the end of the file (%d bytes)", offset, fi.Size())
	}
	// Read one byte more than a page so the last character can be cut cleanly.
	buf := make([]byte, maxBytes+utf8.UTFMax)
	n, err = f.ReadAt(buf, offset)
	if err != nil && err != io.EOF {
		return protocol.MessageFileResult{}, err
	}
	buf = buf[:n]
	if offset < fi.Size() && !utf8.RuneStart(buf[0]) {
		return protocol.MessageFileResult{}, fmt.Errorf("offset %d is inside a character: use next_offset from the last page", offset)
	}
	end := len(buf)
	if end > maxBytes {
		end = maxBytes
		for end > 0 && !utf8.RuneStart(buf[end]) {
			end--
		}
	}
	out.Text = strings.ToValidUTF8(string(buf[:end]), "�")
	if next := offset + int64(end); next < fi.Size() {
		out.NextOffset = next
	}
	return out, nil
}
