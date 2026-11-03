package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// transferCaller is the part of the daemon a transfer needs: one gateway
// frame and its answer.
type transferCaller interface {
	call(ctx context.Context, typ string, params any) (json.RawMessage, error)
}

// TransferRequest describes one upload through the gateway. UploadID empty
// starts a new upload. A non-empty UploadID resumes it: only the parts the
// gateway says it does not have are sent, and Receiver, Size and SHA256 must
// match the upload that ID names.
type TransferRequest struct {
	Receiver string
	Name     string
	Size     int64
	SHA256   string
	UploadID string
}

// TransferUpload sends the whole reader to the gateway in parts and checks
// the whole hash. It returns the upload ID that TransferDownload needs. The
// reader must hold exactly Size bytes.
func TransferUpload(ctx context.Context, c transferCaller, req TransferRequest, r io.Reader) (string, error) {
	var state protocol.TransferState
	if req.UploadID == "" {
		raw, err := c.call(ctx, "xfer.begin", protocol.TransferBegin{
			Receiver: req.Receiver,
			Name:     req.Name,
			Size:     req.Size,
			SHA256:   req.SHA256,
		})
		if err != nil {
			return "", err
		}
		if err := json.Unmarshal(raw, &state); err != nil {
			return "", err
		}
	} else {
		raw, err := c.call(ctx, "xfer.status", protocol.TransferStatus{UploadID: req.UploadID})
		if err != nil {
			return "", err
		}
		if err := json.Unmarshal(raw, &state); err != nil {
			return "", err
		}
		if state.Receiver != req.Receiver || state.Size != req.Size || !strings.EqualFold(state.SHA256, req.SHA256) {
			return "", errors.New("the upload ID names a different file")
		}
	}
	if state.PartSize <= 0 || state.Parts <= 0 || state.Parts > 100 {
		return "", errors.New("the gateway returned an unusable part layout")
	}
	have := make(map[int]bool, len(state.Have))
	for _, i := range state.Have {
		have[i] = true
	}
	var read int64
	for i := 0; i < state.Parts; i++ {
		want := state.PartSize
		if i == state.Parts-1 {
			want = int(state.Size - int64(state.Parts-1)*int64(state.PartSize))
		}
		buf := make([]byte, want)
		n, err := io.ReadFull(r, buf)
		if n != want {
			return "", fmt.Errorf("part %d: read %d of %d bytes: %w", i, n, want, err)
		}
		read += int64(n)
		if have[i] {
			continue
		}
		sum := sha256.Sum256(buf)
		if _, err := c.call(ctx, "xfer.part", protocol.TransferPart{
			UploadID: state.UploadID,
			Index:    i,
			SHA256:   hex.EncodeToString(sum[:]),
			Data:     buf,
		}); err != nil {
			return "", err
		}
	}
	if read != req.Size {
		return "", fmt.Errorf("read %d bytes, want %d", read, req.Size)
	}
	if _, err := c.call(ctx, "xfer.finish", protocol.TransferFinish{UploadID: state.UploadID}); err != nil {
		return "", err
	}
	return state.UploadID, nil
}

// TransferDownload reads a finished upload and checks every part and the
// whole hash before it returns.
func TransferDownload(ctx context.Context, c transferCaller, uploadID string, w io.Writer) error {
	first, err := transferChunk(ctx, c, uploadID, 0)
	if err != nil {
		return err
	}
	if first.Parts <= 0 {
		return errors.New("the gateway returned an unusable part layout")
	}
	h := sha256.New()
	var total int64
	for i := 0; i < first.Parts; i++ {
		chunk := first
		if i > 0 {
			chunk, err = transferChunk(ctx, c, uploadID, i)
			if err != nil {
				return err
			}
		}
		if chunk.Index != i || chunk.Parts != first.Parts || chunk.Size != first.Size {
			return errors.New("the gateway changed the part layout during the download")
		}
		sum := sha256.Sum256(chunk.Data)
		if hex.EncodeToString(sum[:]) != chunk.SHA256 {
			return fmt.Errorf("part %d checksum does not match", i)
		}
		if _, err := w.Write(chunk.Data); err != nil {
			return err
		}
		h.Write(chunk.Data)
		total += int64(len(chunk.Data))
	}
	if total != first.Size {
		return fmt.Errorf("downloaded %d bytes, want %d", total, first.Size)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != first.TotalSHA256 {
		return errors.New("the whole sha256 hash does not match")
	}
	return nil
}

func transferChunk(ctx context.Context, c transferCaller, uploadID string, index int) (protocol.TransferChunk, error) {
	var chunk protocol.TransferChunk
	raw, err := c.call(ctx, "xfer.get", protocol.TransferGet{UploadID: uploadID, Index: index})
	if err != nil {
		return chunk, err
	}
	err = json.Unmarshal(raw, &chunk)
	return chunk, err
}

// TransferConfirm tells the gateway that the receiving machine has the whole
// file, so it can delete the stored parts.
func TransferConfirm(ctx context.Context, c transferCaller, uploadID string) error {
	_, err := c.call(ctx, "xfer.confirm", protocol.TransferConfirm{UploadID: uploadID})
	return err
}
