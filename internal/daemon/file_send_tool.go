package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// fileSendDestinations names where each file of a sent message lands on the
// receiving machine, with the original path of each renamed file.
func fileSendDestinations(msg protocol.Message, sent []FileSent) []protocol.FileSendDestination {
	sources := map[string]string{}
	for _, f := range sent {
		if f.Source != "" {
			sources[f.Entry.ID] = f.Source
		}
	}
	out := make([]protocol.FileSendDestination, 0, len(msg.Files))
	for _, f := range msg.Files {
		out = append(out, protocol.FileSendDestination{
			Name:   f.Name,
			Path:   filepath.ToSlash(filepath.Join(fileInboxFolder, msg.To, msg.ID, f.Name)),
			Source: sources[f.ID],
		})
	}
	return out
}

// localFileSend binds the sender to the caller, never to an argument.
func (d *Daemon) localFileSend(ctx context.Context, lc *localConn, raw json.RawMessage) (any, error) {
	sid, err := lc.boundID()
	if err != nil {
		return nil, err
	}
	ls := d.sessionByID(sid)
	if ls == nil {
		return nil, errors.New("this method needs a local session")
	}
	if d.infoOf(ls).Worker != nil || d.workerBySession(sid) != nil {
		return nil, errors.New("workers send files with agent_handback")
	}
	var in protocol.FileSendRequest
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, errors.New("bad file_send params")
	}
	if strings.TrimSpace(in.To) == "" || strings.TrimSpace(in.Note) == "" {
		return nil, errors.New("file_send needs a recipient and a note")
	}
	to, err := d.resolveTo(ctx, in.To)
	if err != nil {
		return nil, err
	}
	sent, err := d.SendFiles(ctx, FileSend{From: sid, To: to, Body: in.Note, Paths: in.Paths, ConfineSymlinks: true})
	if sent.Message.ID == "" {
		return nil, err
	}
	result := protocol.FileSendResult{MessageID: sent.Message.ID, Files: fileSendDestinations(sent.Message, sent.Files)}
	if err != nil {
		result.Error = err.Error()
	}
	return result, nil
}
