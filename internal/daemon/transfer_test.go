package daemon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// TestTransferClientRoundTrip sends bytes from one daemon to another through
// the gateway in parts, which is what a file between machines and a skill
// above the 2 MB frame limit will use.
func TestTransferClientRoundTrip(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	sender := startNode(t, g, nodeOpts{})
	receiver := startNode(t, g, nodeOpts{})
	data := make([]byte, protocol.TransferPartSize+321)
	for i := range data {
		data[i] = byte(i*17 + 3)
	}
	sum := sha256.Sum256(data)
	req := TransferRequest{
		Receiver: receiver.cfg.NodeID,
		Name:     "report.html",
		Size:     int64(len(data)),
		SHA256:   hex.EncodeToString(sum[:]),
	}
	ctx, cancel := context.WithTimeout(context.Background(), scaled(20*time.Second))
	defer cancel()

	id, err := TransferUpload(ctx, sender.d, req, bytes.NewReader(data))
	if err != nil {
		t.Fatalf("TransferUpload: %v", err)
	}
	if id == "" {
		t.Fatal("no upload ID")
	}
	var got bytes.Buffer
	if err := TransferDownload(ctx, receiver.d, id, &got); err != nil {
		t.Fatalf("TransferDownload: %v", err)
	}
	if !bytes.Equal(got.Bytes(), data) {
		t.Fatal("downloaded bytes differ")
	}

	// Resuming the same upload re-reads the source and sends only the parts
	// the gateway does not hold.
	resumed, err := TransferUpload(ctx, sender.d, TransferRequest{
		Receiver: req.Receiver, Name: req.Name, Size: req.Size, SHA256: req.SHA256, UploadID: id,
	}, bytes.NewReader(data))
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if resumed != id {
		t.Fatalf("resume made upload %q, want %q", resumed, id)
	}

	if err := TransferConfirm(ctx, sender.d, id); err == nil {
		t.Fatal("the sender confirmed its own upload")
	}
	if err := TransferConfirm(ctx, receiver.d, id); err != nil {
		t.Fatalf("TransferConfirm: %v", err)
	}
	if err := TransferDownload(ctx, receiver.d, id, io.Discard); err == nil {
		t.Fatal("a confirmed upload was downloaded again")
	}
}
